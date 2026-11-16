package main

import (
	"cmp"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
)

const (
	// launchPrefix is where the apps' launch URLs point on the demo's own host
	launchPrefix = "/apps/"
	// appPrefix is where the sample app runs on a session's host
	appPrefix = assetPrefix + "apps/"
	// sessionCookie remembers the visitor's session on the demo's own host
	sessionCookie = "demo_session"
	stateCookie   = "demo_app_state"
)

// clientID is the same in every template, so the sample app knows it without a lookup
func (a app) clientID() string {
	return uuid.NewSHA1(uuid.Nil, []byte(a.key)).String()
}

// launchable apps have a web UI, which the sample app stands in for
func (a app) launchable() bool {
	return a.host != "" || a.callbackURL != ""
}

func (a app) scopes() []string {
	s := []string{"openid", "profile", "email"}
	if a.groupsScope {
		s = append(s, "groups")
	}
	if a.offlineAccess {
		s = append(s, "offline_access")
	}
	return s
}

// clientSecret derives a client's secret from the instance's encryption key, which every instance made from the same template shares and keeps across restarts of the demo
func (i instance) clientSecret(clientID string) string {
	mac := hmac.New(sha256.New, []byte(i.encryptionKey))
	mac.Write([]byte(clientID))
	return hex.EncodeToString(mac.Sum(nil))
}

// launchApp sends a visitor who opened an app from their dashboard to the sample app on their session's host
func (s *server) launchApp(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimPrefix(r.URL.Path, launchPrefix)
	if c, err := r.Cookie(sessionCookie); err == nil && s.pool.session(c.Value) != nil {
		http.Redirect(w, r, s.pool.sessionURL(c.Value)+appPrefix+url.PathEscape(key), http.StatusFound)
		return
	}
	s.sessionEnded(w, r)
}

type appPage struct {
	Name, Description, ClientID, SignInURL, Error string
	HasLogo                                       bool
	User                                          *appUser
	Claims                                        []claim
}

type appUser struct {
	Name, Email, Picture string
}

// serveApp is a minimal OIDC client that signs the visitor in to one of the seeded apps and shows the claims it received
func (s *server) serveApp(w http.ResponseWriter, r *http.Request, id string) {
	sess := s.pool.session(id)
	if sess == nil {
		s.sessionEnded(w, r)
		return
	}
	key, action, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, appPrefix), "/")
	i := slices.IndexFunc(apps, func(a app) bool { return a.key == key && a.launchable() })
	if i < 0 || r.Method != http.MethodGet || (action != "" && action != "callback") {
		s.sendError(w, r, http.StatusNotFound, "Not found", messagePage{
			Title:      "Page not found",
			Text:       "There's no app here. Open one from the dashboard of your Pocket ID instance.",
			ActionText: "Go to the dashboard",
			ActionURL:  "/",
		})
		return
	}
	a := apps[i]
	page := appPage{Name: a.name, Description: a.description, ClientID: a.clientID(), HasLogo: a.preset != ""}
	appURL := s.pool.sessionURL(id) + appPrefix + a.key
	cookiePath := appPrefix + a.key + "/"

	// The landing page starts a new sign-in with its own state, which the callback checks
	if action == "" {
		state, verifier := randomHex(16), ""
		params := url.Values{
			"response_type": {"code"},
			"client_id":     {page.ClientID},
			"redirect_uri":  {appURL + "/callback"},
			"scope":         {strings.Join(a.scopes(), " ")},
			"state":         {state},
		}
		// Only apps seeded with PKCE send a challenge, because Pocket ID would otherwise suggest turning it on for the others
		if a.pkce {
			verifier = randomHex(32)
			challenge := sha256.Sum256([]byte(verifier))
			params.Set("code_challenge", base64.RawURLEncoding.EncodeToString(challenge[:]))
			params.Set("code_challenge_method", "S256")
		}
		http.SetCookie(w, &http.Cookie{
			Name:     stateCookie,
			Value:    state + "." + verifier,
			Path:     cookiePath,
			MaxAge:   600,
			HttpOnly: true,
			Secure:   s.cfg.baseURL.Scheme == "https",
			SameSite: http.SameSiteLaxMode,
		})
		page.SignInURL = "/authorize?" + params.Encode()
		s.render(w, http.StatusOK, s.app, page)
		return
	}

	// Show why the sign-in failed and let the visitor start over, and sign out the same way
	page.SignInURL = appURL
	fail := func(text string) {
		page.Error = text
		s.render(w, http.StatusBadRequest, s.app, page)
	}
	q := r.URL.Query()
	if q.Get("error") != "" {
		fail(cmp.Or(q.Get("error_description"), q.Get("error")))
		return
	}
	var state, verifier string
	if c, err := r.Cookie(stateCookie); err == nil {
		state, verifier, _ = strings.Cut(c.Value, ".")
	}
	if state == "" || state != q.Get("state") {
		fail("The sign-in expired or was started in another tab.")
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Path: cookiePath, MaxAge: -1})

	// Exchange the code directly with the instance, which makes the ID token trustworthy without checking its signature
	form := url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {q.Get("code")},
		"redirect_uri": {appURL + "/callback"},
	}
	if verifier != "" {
		form.Set("code_verifier", verifier)
	}
	claims, err := exchangeCode(r, sess.instance, page.ClientID, form)
	if err != nil {
		slog.Warn("Sample app sign-in failed", "session", id, "app", a.key, "error", err)
		fail(err.Error())
		return
	}
	str := func(key string) string {
		v, _ := claims[key].(string)
		return v
	}
	page.User = &appUser{Name: cmp.Or(str("name"), str("preferred_username"), str("sub")), Email: str("email"), Picture: str("picture")}
	for _, k := range slices.Sorted(maps.Keys(claims)) {
		v, ok := claims[k].(string)
		if !ok {
			b, _ := json.Marshal(claims[k])
			v = string(b)
		}
		page.Claims = append(page.Claims, claim{k, v})
	}
	s.render(w, http.StatusOK, s.app, page)
}

// exchangeCode redeems an authorization code and returns the claims of the ID token
func exchangeCode(r *http.Request, inst instance, clientID string, form url.Values) (map[string]any, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, inst.url+"/api/oidc/token", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, inst.clientSecret(clientID))
	res, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	var body struct {
		IDToken          string `json:"id_token"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		return nil, err
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the token request failed: %s", body.ErrorDescription)
	}
	parts := strings.Split(body.IDToken, ".")
	if len(parts) != 3 {
		return nil, fmt.Errorf("the token response has no ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims map[string]any
	return claims, json.Unmarshal(payload, &claims)
}
