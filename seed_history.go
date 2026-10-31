package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	mrand "math/rand/v2"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type member struct {
	person
	id      string
	created time.Time
	// left is zero for people who still work at the company
	left    time.Time
	devices [2]string
	groups  map[string]bool
}

type seededApp struct {
	app
	id      string
	created time.Time
}

type auditLog struct {
	at            time.Time
	event, ip, ua string
	userID, city  string
	data          map[string]string
}

type passkey struct {
	at           time.Time
	name, aaguid string
	userID       string
	credentialID []byte
	transports   []string
	synced       bool
}

// history generates the activity of the company's people over the last weeks, which the API can't backdate
type history struct {
	rng          *mrand.Rand
	now          time.Time
	companyStart time.Time
	windowStart  time.Time
	groupDates   map[string]time.Time
	members      []*member
	clients      []seededApp

	logs       []auditLog
	passkeys   []passkey
	authorized map[[2]string]time.Time
}

func writeHistory(dbPath string, ids seedIDs, rng *mrand.Rand, now time.Time) error {
	h := newHistory(ids, rng, now.UTC().Truncate(time.Second))
	for _, m := range h.members {
		if !m.pending {
			h.addActivity(m)
		}
	}
	h.addOneOffs()
	return h.write(dbPath, ids)
}

func newHistory(ids seedIDs, rng *mrand.Rand, now time.Time) *history {
	h := &history{
		rng:          rng,
		now:          now,
		companyStart: now.AddDate(0, 0, -companyAgeDays),
		windowStart:  now.AddDate(0, 0, -historyDays),
		groupDates:   map[string]time.Time{},
		authorized:   map[[2]string]time.Time{},
	}

	for i, g := range groups {
		if g.daysAgo > 0 {
			h.groupDates[g.key] = now.AddDate(0, 0, -g.daysAgo)
		} else {
			h.groupDates[g.key] = h.companyStart.AddDate(0, 0, i*3+rng.IntN(3)).Add(h.hours(9))
		}
	}

	// People join in list order over the company's lifetime, slowly at first, and recent hires on their own dates
	span := float64(companyAgeDays - historyDays - 5)
	for i, p := range people {
		m := &member{person: p, id: ids.people[i], devices: p.devices, groups: map[string]bool{}}
		if p.hiredDaysAgo > 0 {
			m.created = now.AddDate(0, 0, -p.hiredDaysAgo).Add(-time.Hour - h.hours(6) - h.minutes(60))
		} else {
			days := int(span*math.Pow(float64(i)/float64(len(people)), 1.3)) + rng.IntN(7)
			m.created = h.companyStart.AddDate(0, 0, days).Add(h.hours(9) + h.minutes(60))
		}
		if p.leftDaysAgo > 0 {
			m.left = now.AddDate(0, 0, -p.leftDaysAgo).Add(-time.Hour - h.hours(5))
		}
		if m.devices[0] == "" {
			m.devices = deviceSets[rng.IntN(len(deviceSets))]
		}
		for _, g := range p.allGroups() {
			m.groups[g] = true
		}
		h.members = append(h.members, m)
	}

	for i, a := range apps {
		c := seededApp{app: a, id: ids.apps[i]}
		if a.createdDaysAgo > 0 {
			c.created = now.AddDate(0, 0, -a.createdDaysAgo).Add(-time.Hour - h.hours(6))
		} else {
			c.created = h.companyStart.AddDate(0, 0, 2+rng.IntN(329)).Add(h.hours(10))
		}
		h.clients = append(h.clients, c)
	}
	return h
}

func (h *history) addActivity(m *member) {
	end := h.now
	if !m.left.IsZero() {
		end = m.left
	}
	start := maxTime(h.windowStart, m.created)
	desktop, phone := userAgents[m.devices[0]], userAgents[m.devices[1]]
	homeOrOffice := homeIP(m.city, m.id)
	if office, ok := offices[m.city]; ok {
		homeOrOffice = office
	}

	// New hires sign in with a one-time link from IT before they create their first passkey
	if !m.created.Before(h.windowStart) {
		h.log(m, "TOKEN_SIGN_IN", m.created.Add(10*time.Minute+h.minutes(80)), homeOrOffice, desktop, m.city, map[string]string{"newBrowser": "true"})
	}
	for _, pk := range h.addPasskeys(m) {
		if !pk.at.Before(h.windowStart) {
			h.log(m, "PASSKEY_ADDED", pk.at, homeOrOffice, desktop, m.city, map[string]string{"credentialID": hex.EncodeToString(pk.credentialID), "passkeyName": pk.name})
		}
	}

	var accessible []seededApp
	for _, c := range h.clients {
		if c.groups == nil || slices.ContainsFunc(c.groups, func(g string) bool { return m.groups[g] }) {
			accessible = append(accessible, c)
		}
	}

	// Long-standing people authorized most of their apps before the history starts
	seen := map[string]bool{}
	if m.created.Before(h.windowStart) {
		for _, c := range accessible {
			if c.created.Before(h.windowStart) && h.rng.Float64() < 0.6+c.usage/2 {
				seen[c.key] = true
				h.authorized[[2]string{m.id, c.id}] = maxTime(m.created, c.created).AddDate(0, 0, 1+h.rng.IntN(30))
			}
		}
	}

	offset := time.Duration(cities[m.city].utcOffset) * time.Hour
	var vacationStart time.Time
	if h.rng.Float64() < 0.55 {
		vacationStart = h.windowStart.AddDate(0, 0, h.rng.IntN(historyDays))
	}
	vacationEnd := vacationStart.AddDate(0, 0, []int{5, 7, 10, 14}[h.rng.IntN(4)])

	// Each active day has one to three sign-in sessions during working hours, and each session uses some of the person's apps
	for day := start.Truncate(24 * time.Hour); day.Before(end); day = day.AddDate(0, 0, 1) {
		weekday := day.Weekday() != time.Saturday && day.Weekday() != time.Sunday
		onVacation := !day.Before(vacationStart) && day.Before(vacationEnd)
		active := map[bool]float64{true: 0.9, false: 0.07}[weekday] * map[bool]float64{true: 0.04, false: 1}[onVacation]
		if h.rng.Float64() >= active {
			continue
		}

		_, hasOffice := offices[m.city]
		inOffice := weekday && hasOffice && h.rng.Float64() < 0.6
		city, ipSalt := m.city, m.id
		if !onVacation && h.rng.Float64() < 0.02 && m.city != "Zurich" {
			city, ipSalt = "Zurich", m.id+"Zurich"
		}
		sessions := 1
		if r := h.rng.Float64(); r > 0.93 {
			sessions = 3
		} else if r > 0.65 {
			sessions = 2
		}

		for s := range sessions {
			at := day.Add(8*time.Hour - offset + time.Duration(float64(s)*3.5*float64(time.Hour)) + h.hours(3) + h.minutes(60))
			if at.Before(start) || !at.Before(end) {
				continue
			}
			mobile := h.rng.Float64() < map[bool]float64{true: 0.12, false: 0.35}[weekday]
			ua, ip := desktop, homeIP(city, ipSalt)
			switch {
			case mobile:
				ua, ip = phone, fmt.Sprintf("%s.%d.%d", cities[city].ipPrefix, 1+h.rng.IntN(254), 2+h.rng.IntN(253))
			case inOffice && city == m.city:
				ip = offices[city]
			}
			h.log(m, "SIGN_IN", at, ip, ua, city, nil)

			for _, c := range accessible {
				chance := c.usage / float64(sessions)
				if mobile {
					chance *= 0.4
				}
				if c.created.After(at) || h.rng.Float64() > chance {
					continue
				}
				usedAt := at.Add(h.minutes(200))
				if !usedAt.Before(end) {
					continue
				}
				event := "CLIENT_AUTHORIZATION"
				if c.deviceCode {
					event = "DEVICE_CODE_AUTHORIZATION"
				}
				if !seen[c.key] {
					event = "NEW_" + event
				}
				h.log(m, event, usedAt, ip, ua, city, map[string]string{"clientName": c.name})
				seen[c.key] = true
				key := [2]string{m.id, c.id}
				h.authorized[key] = maxTime(h.authorized[key], usedAt)
			}
		}
	}
}

// addPasskeys gives everyone one or two passkeys, synced ones for most people and hardware keys for the infrastructure crowd
func (h *history) addPasskeys(m *member) []passkey {
	kinds := m.passkeys
	if kinds == nil {
		fallback := "icloud"
		if strings.HasPrefix(m.devices[0], "win") {
			fallback = "windows"
		}
		if m.devices[1] == "iphone" && h.rng.Float64() < 0.6 {
			kinds = []string{"icloud"}
		} else {
			kinds = []string{pick(h.rng, "google", "1password", "bitwarden", "chrome_mac", fallback)}
		}
		if h.rng.Float64() < 0.3 {
			kinds = append(kinds, pick(h.rng, "yubikey", "1password", "google"))
		}
	}

	var added []passkey
	for i, kind := range slices.Compact(kinds) {
		a := authenticators[kind]
		at := m.created.Add(3*time.Minute + h.minutes(22))
		if i > 0 {
			at = m.created.AddDate(0, 0, 1+h.rng.IntN(max(1, int(h.now.Sub(m.created).Hours()/24)-2)))
		}
		if !m.left.IsZero() {
			at = minTime(at, m.left.AddDate(0, 0, -1))
		}
		credentialID := make([]byte, map[bool]int{true: 32, false: 64}[a.synced])
		_, _ = rand.Read(credentialID)
		pk := passkey{at: at, name: a.name, aaguid: a.aaguid, userID: m.id, credentialID: credentialID, transports: a.transports, synced: a.synced}
		h.passkeys = append(h.passkeys, pk)
		added = append(added, pk)
	}
	return added
}

func (h *history) addOneOffs() {
	var withPasskeys, inZurich []*member
	for _, m := range h.members {
		if m.pending || !m.left.IsZero() {
			continue
		}
		withPasskeys = append(withPasskeys, m)
		if m.city == "Zurich" && m.created.Before(h.windowStart) {
			inZurich = append(inZurich, m)
		}
	}

	// A few people replaced a lost or retired passkey
	h.rng.Shuffle(len(withPasskeys), func(i, j int) { withPasskeys[i], withPasskeys[j] = withPasskeys[j], withPasskeys[i] })
	for _, m := range withPasskeys[:4] {
		at := h.now.AddDate(0, 0, -3-h.rng.IntN(historyDays-8)).Add(-time.Hour - h.hours(7))
		credentialID := make([]byte, 32)
		_, _ = rand.Read(credentialID)
		name := pick(h.rng, "YubiKey 5 NFC", "Old MacBook Pro", "Samsung Galaxy S23")
		h.log(m, "PASSKEY_REMOVED", at, homeIP(m.city, m.id), userAgents[m.devices[0]], m.city, map[string]string{"credentialID": hex.EncodeToString(credentialID), "passkeyName": name})
	}

	// A few people in Zurich forgot their phone and signed in with a one-time code from IT
	h.rng.Shuffle(len(inZurich), func(i, j int) { inZurich[i], inZurich[j] = inZurich[j], inZurich[i] })
	for _, m := range inZurich[:3] {
		at := h.now.AddDate(0, 0, -2-h.rng.IntN(58)).Add(-2*time.Hour - h.hours(4))
		h.log(m, "TOKEN_SIGN_IN", at, offices["Zurich"], userAgents[m.devices[0]], "Zurich", map[string]string{"newBrowser": "true"})
	}
}

func (h *history) log(m *member, event string, at time.Time, ip, ua, city string, data map[string]string) {
	h.logs = append(h.logs, auditLog{at: at, event: event, ip: ip, ua: ua, userID: m.id, city: city, data: data})
}

func (h *history) write(dbPath string, ids seedIDs) error {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	exec := func(query string, args ...any) {
		if err == nil {
			_, err = tx.Exec(query, args...)
			if err != nil {
				err = fmt.Errorf("%s: %w", query, err)
			}
		}
	}

	// Backdate what the API created a minute ago
	var admins []*member
	for _, m := range h.members {
		updated := m.created.AddDate(0, 0, h.rng.IntN(41))
		if !m.left.IsZero() {
			updated = m.left
		}
		exec("UPDATE users SET created_at = ?, updated_at = ? WHERE id = ?", m.created.Unix(), minTime(updated, h.now).Unix(), m.id)
		if m.admin {
			admins = append(admins, m)
		}
	}
	for key, at := range h.groupDates {
		exec("UPDATE user_groups SET created_at = ?, updated_at = ? WHERE id = ?", at.Unix(), at.Unix(), ids.groups[key])
	}
	for _, c := range h.clients {
		creator := admins[h.rng.IntN(len(admins))]
		exec("UPDATE oidc_clients SET created_at = ?, created_by_id = ? WHERE id = ?", c.created.Unix(), creator.id, c.id)
	}
	apisCreated := h.companyStart.AddDate(0, 0, 200).Unix()
	exec("UPDATE apis SET created_at = ?, updated_at = ?", apisCreated, h.now.AddDate(0, 0, -9).Unix())
	exec("UPDATE api_permissions SET created_at = ?", apisCreated)
	exec("UPDATE custom_claims SET created_at = (SELECT created_at FROM users WHERE users.id = custom_claims.user_id) WHERE user_id IS NOT NULL")
	exec("UPDATE custom_claims SET created_at = (SELECT created_at FROM user_groups g WHERE g.id = custom_claims.user_group_id) WHERE user_group_id IS NOT NULL")
	exec("UPDATE oidc_claim_mapping_policies SET created_at = ? WHERE id != ?", h.companyStart.AddDate(0, 0, 260).Unix(), defaultPolicyID)

	// Insert the generated activity
	for _, pk := range h.passkeys {
		publicKey := make([]byte, 77)
		_, _ = rand.Read(publicKey)
		transports, _ := json.Marshal(pk.transports)
		exec("INSERT INTO webauthn_credentials (id, created_at, name, credential_id, public_key, attestation_type, transport, user_id, backup_eligible, backup_state, aaguid) VALUES (?,?,?,?,?,?,?,?,?,?,?)",
			uuid.NewString(), pk.at.Unix(), pk.name, pk.credentialID, publicKey, "none", transports, pk.userID, pk.synced, pk.synced, pk.aaguid)
	}
	for _, l := range h.logs {
		data, _ := json.Marshal(l.data)
		if l.data == nil {
			data = []byte("{}")
		}
		exec("INSERT INTO audit_logs (id, created_at, event, ip_address, user_agent, data, user_id, country, city) VALUES (?,?,?,?,?,?,?,?,?)",
			uuid.NewString(), l.at.Unix(), l.event, l.ip, l.ua, data, l.userID, cities[l.city].country, l.city)
	}
	scopes := map[string][]byte{}
	for _, c := range h.clients {
		s := []string{"openid", "profile", "email"}
		if c.groupsScope {
			s = append(s, "groups")
		}
		if c.offlineAccess {
			s = append(s, "offline_access")
		}
		scopes[c.id], _ = json.Marshal(s)
	}
	for key, at := range h.authorized {
		exec("INSERT INTO user_authorized_oidc_clients (scope, user_id, client_id, last_used_at) VALUES (?,?,?,?)", scopes[key[1]], key[0], key[1], at.Unix())
	}

	if err != nil {
		return err
	}
	return tx.Commit()
}

// homeIP gives everyone a stable address per city, so their sign-ins from home share an IP
func homeIP(city, salt string) string {
	f := fnv.New64a()
	f.Write([]byte(salt))
	r := mrand.New(mrand.NewPCG(f.Sum64(), 0))
	return fmt.Sprintf("%s.%d.%d", cities[city].ipPrefix, 1+r.IntN(254), 2+r.IntN(253))
}

func (h *history) hours(n int) time.Duration   { return time.Duration(h.rng.IntN(n)) * time.Hour }
func (h *history) minutes(n int) time.Duration { return time.Duration(h.rng.IntN(n)) * time.Minute }

func pick(rng *mrand.Rand, options ...string) string { return options[rng.IntN(len(options))] }

func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
