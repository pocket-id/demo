package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"math/rand/v2"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"time"
)

// defaultPolicyID is the built-in claim mapping policy every instance starts with
const defaultPolicyID = "00000000-0000-0000-0000-000000000000"

// seedIDs holds the IDs the API assigned, in the order of the dataset
type seedIDs struct {
	groups map[string]string
	people []string
	apps   []string
}

type created struct {
	ID string `json:"id"`
}

type claim struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// seedEntities creates everything that should go through the server's own logic, like validation, logo storage and generated secrets
func seedEntities(ctx context.Context, inst instance, rng *rand.Rand) (seedIDs, error) {
	ids := seedIDs{groups: map[string]string{}}

	for _, g := range groups {
		var res created
		if err := inst.call(ctx, http.MethodPost, "/user-groups", map[string]string{"name": g.key, "friendlyName": g.name}, &res); err != nil {
			return ids, err
		}
		ids.groups[g.key] = res.ID
		if claims, ok := groupClaims[g.key]; ok {
			if err := inst.call(ctx, http.MethodPut, "/custom-claims/user-group/"+res.ID, claimList(claims), nil); err != nil {
				return ids, err
			}
		}
	}

	portraits := map[bool][]int{false: rng.Perm(98), true: rng.Perm(98)}
	for i, p := range people {
		id, err := createUser(ctx, inst, p, i, ids.groups)
		if err != nil {
			return ids, err
		}
		ids.people = append(ids.people, id)

		// Most people have a profile picture, the rest show their initials
		if rng.Float64() < 0.72 {
			n := portraits[p.female][0] + 1
			portraits[p.female] = portraits[p.female][1:]
			gender := map[bool]string{false: "men", true: "women"}[p.female]
			img, err := download(ctx, fmt.Sprintf("https://randomuser.me/api/portraits/%s/%d.jpg", gender, n))
			if err == nil {
				err = inst.upload(ctx, http.MethodPut, "/users/"+id+"/profile-picture", "avatar.jpg", "image/jpeg", img)
			}
			if err != nil {
				slog.Warn("Skipped profile picture", "user", p.first+" "+p.last, "error", err)
			}
		}
	}

	policies, err := createPolicies(ctx, inst)
	if err != nil {
		return ids, err
	}

	appIDs := map[string]string{}
	for _, a := range apps {
		id, err := createClient(ctx, inst, a, ids.groups, policies)
		if err != nil {
			return ids, err
		}
		ids.apps = append(ids.apps, id)
		appIDs[a.key] = id
	}

	for _, a := range resourceAPIs {
		if err := createResourceAPI(ctx, inst, a, appIDs); err != nil {
			return ids, err
		}
	}
	return ids, nil
}

func createUser(ctx context.Context, inst instance, p person, index int, groupIDs map[string]string) (string, error) {
	username := p.username()
	var groupList []string
	for _, key := range p.allGroups() {
		groupList = append(groupList, groupIDs[key])
	}
	body := map[string]any{
		"username":      username,
		"email":         username + "@" + domain,
		"emailVerified": !p.pending,
		"firstName":     p.first,
		"lastName":      p.last,
		"displayName":   p.first + " " + p.last,
		"isAdmin":       p.admin,
		"disabled":      p.leftDaysAgo > 0,
		"userGroupIds":  groupList,
	}
	if p.locale != "" {
		body["locale"] = p.locale
	}
	var res created
	if err := inst.call(ctx, http.MethodPost, "/users", body, &res); err != nil {
		return "", err
	}

	claims := []claim{
		{"employee_id", fmt.Sprintf("KL-%04d", 1000+index)},
		{"job_title", p.title},
		{"department", p.department},
		{"office", p.city},
	}
	return res.ID, inst.call(ctx, http.MethodPut, "/custom-claims/user/"+res.ID, claims, nil)
}

func createPolicies(ctx context.Context, inst instance) (map[string]string, error) {
	type mapping = map[string]any
	var builtin struct {
		ClaimMappings []mapping `json:"claimMappings"`
	}
	if err := inst.call(ctx, http.MethodGet, "/oidc/claim-mapping-policies/"+defaultPolicyID, nil, &builtin); err != nil {
		return nil, err
	}
	pick := func(keep func(name string) bool) []mapping {
		return slices.DeleteFunc(slices.Clone(builtin.ClaimMappings), func(m mapping) bool { return !keep(m["claimName"].(string)) })
	}
	extra := func(name, sourceType, value string, accessToken bool) mapping {
		return mapping{"claimName": name, "sourceType": sourceType, "sourceValue": value, "scope": []string{"profile"}, "accessToken": accessToken, "idToken": true, "userInfo": true}
	}

	specs := []struct {
		key, name string
		mappings  []mapping
	}{
		{"grafana", "Grafana roles", append(
			pick(func(name string) bool { return name != "*" }),
			extra("grafana_role", "custom_claim", "grafana_role", true),
			extra("org", "static", "kestrel-labs", false),
		)},
		{"minimal", "Minimal (email only)", pick(func(name string) bool {
			return slices.Contains([]string{"email", "email_verified", "name", "preferred_username"}, name)
		})},
	}
	ids := map[string]string{}
	for _, s := range specs {
		var res created
		if err := inst.call(ctx, http.MethodPost, "/oidc/claim-mapping-policies", map[string]any{"name": s.name, "isDefault": false, "claimMappings": s.mappings}, &res); err != nil {
			return nil, err
		}
		ids[s.key] = res.ID
	}
	return ids, nil
}

func createClient(ctx context.Context, inst instance, a app, groupIDs, policies map[string]string) (string, error) {
	baseURL := ""
	if a.host != "" {
		baseURL = "https://" + a.host + "." + domain
	}
	callbackURL := a.callbackURL
	switch {
	case callbackURL != "":
	case baseURL != "":
		callbackURL = baseURL + "/oauth2/callback"
	default:
		callbackURL = "http://localhost:8085/callback"
	}
	launchURL := a.launchURL
	if launchURL == "" {
		launchURL = baseURL
	}
	logoutURLs := []string{}
	if baseURL != "" {
		logoutURLs = append(logoutURLs, baseURL+"/logout")
	}

	body := map[string]any{
		"name":                     a.name,
		"description":              a.description,
		"callbackURLs":             []string{callbackURL},
		"logoutCallbackURLs":       logoutURLs,
		"isPublic":                 a.public,
		"pkceEnabled":              a.public || a.pkce,
		"requiresReauthentication": a.reauth,
		"skipConsent":              a.skipConsent,
		"isGroupRestricted":        a.groups != nil,
		"claimMappingPolicyId":     policies[a.policy],
		"credentials":              map[string]any{},
		"disabled":                 a.disabledDaysAgo > 0,
	}
	if launchURL != "" {
		body["launchURL"] = launchURL
	}
	var res created
	if err := inst.call(ctx, http.MethodPost, "/oidc/clients", body, &res); err != nil {
		return "", err
	}

	if a.preset != "" {
		if err := uploadLogos(ctx, inst, res.ID, a.preset); err != nil {
			slog.Warn("Skipped logo", "app", a.name, "error", err)
		}
	}
	if a.groups != nil {
		var allowed []string
		for _, key := range a.groups {
			allowed = append(allowed, groupIDs[key])
		}
		if err := inst.call(ctx, http.MethodPut, "/oidc/clients/"+res.ID+"/allowed-user-groups", map[string]any{"userGroupIds": allowed}, nil); err != nil {
			return "", err
		}
	}
	return res.ID, nil
}

// uploadLogos uploads the preset's logos separately because a failed download while creating a client would still leave it behind
func uploadLogos(ctx context.Context, inst instance, clientID, preset string) error {
	type logoPreset struct {
		Reference   string `json:"reference"`
		LogoURL     string `json:"logoUrl"`
		DarkLogoURL string `json:"darkLogoUrl"`
	}
	var presets []logoPreset
	if err := inst.call(ctx, http.MethodGet, "/oidc/logo-presets?search="+url.QueryEscape(preset), nil, &presets); err != nil {
		return err
	}
	i := slices.IndexFunc(presets, func(p logoPreset) bool { return p.Reference == preset })
	if i < 0 {
		return fmt.Errorf("no logo preset %q", preset)
	}

	var errs []error
	for light, logoURL := range map[string]string{"true": presets[i].LogoURL, "false": presets[i].DarkLogoURL} {
		if logoURL == "" {
			continue
		}
		svg, err := download(ctx, logoURL)
		if err == nil {
			err = inst.upload(ctx, http.MethodPost, "/oidc/clients/"+clientID+"/logo?light="+light, path.Base(logoURL), "image/svg+xml", svg)
		}
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func createResourceAPI(ctx context.Context, inst instance, a resourceAPI, appIDs map[string]string) error {
	var res created
	if err := inst.call(ctx, http.MethodPost, "/apis", map[string]string{"name": a.name, "resource": a.resource}, &res); err != nil {
		return err
	}

	type permissionBody struct {
		Key         string `json:"key"`
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	var permissions []permissionBody
	for _, p := range a.permissions {
		permissions = append(permissions, permissionBody{p.key, p.name, p.description})
	}
	var withPermissions struct {
		Permissions []struct {
			ID  string `json:"id"`
			Key string `json:"key"`
		} `json:"permissions"`
	}
	if err := inst.call(ctx, http.MethodPut, "/apis/"+res.ID+"/permissions", map[string]any{"permissions": permissions}, &withPermissions); err != nil {
		return err
	}
	permissionIDs := map[string]string{}
	for _, p := range withPermissions.Permissions {
		permissionIDs[p.Key] = p.ID
	}
	idsOf := func(keys []string) []string {
		ids := []string{}
		for _, k := range keys {
			ids = append(ids, permissionIDs[k])
		}
		return ids
	}

	for _, appKey := range slices.Sorted(maps.Keys(a.grants)) {
		g := a.grants[appKey]
		err := inst.call(ctx, http.MethodPut, "/apis/"+res.ID+"/clients/"+appIDs[appKey], map[string]any{
			"userDelegatedAccess":        len(g.delegated) > 0,
			"clientAccess":               len(g.client) > 0,
			"userDelegatedPermissionIds": idsOf(g.delegated),
			"clientPermissionIds":        idsOf(g.client),
		}, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

func claimList(claims map[string]string) []claim {
	var list []claim
	for _, key := range slices.Sorted(maps.Keys(claims)) {
		list = append(list, claim{key, claims[key]})
	}
	return list
}

// download retries because jsDelivr and randomuser.me occasionally refuse a request under load
func download(ctx context.Context, url string) (body []byte, err error) {
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * time.Second):
			}
		}
		if body, err = get(ctx, url); err == nil {
			return body, nil
		}
	}
	return nil, err
}

func get(ctx context.Context, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "pocket-id-demo")
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: %s", url, res.Status)
	}
	return io.ReadAll(res.Body)
}

var usernameReplacer = strings.NewReplacer("á", "a", "ã", "a", "é", "e", "ê", "e", "í", "i", "ó", "o", "ü", "u", "ç", "c", "'", "", " ", "")

func (p person) username() string {
	return usernameReplacer.Replace(strings.ToLower(p.first + "." + p.last))
}

// allGroups adds the office group of people who live in an office city
func (p person) allGroups() []string {
	if _, ok := offices[p.city]; ok {
		return append(slices.Clone(p.groups), "office-"+strings.ToLower(p.city))
	}
	return p.groups
}
