package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// handleSetup answers Pocket ID's initial admin setup until the visitor has signed up, because the seeded users already closed it on the instance
// It returns false for requests the instance should answer itself
func (s *server) handleSetup(w http.ResponseWriter, r *http.Request, sess *session) bool {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.signedUp {
		return false
	}

	switch r.Method {
	case http.MethodGet:
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		// The instance protects its own API against cross-site requests, but this endpoint never reaches it
		if err := s.csrf.Check(r); err != nil {
			jsonError(w, http.StatusForbidden, "Cross-origin requests are not allowed.")
			return true
		}
		if err := s.signUp(w, r, sess); err != nil {
			slog.Error("Failed to sign up the visitor", "host", r.Host, "error", err)
			jsonError(w, http.StatusInternalServerError, "Your account couldn't be created. Please try again.")
		}
	default:
		return false
	}
	return true
}

// signUp creates the visitor's admin account and signs them in with a login code, which answers like the setup endpoint: a session cookie and the user
func (s *server) signUp(w http.ResponseWriter, r *http.Request, sess *session) error {
	var input struct {
		Username  string  `json:"username"`
		Email     *string `json:"email"`
		FirstName string  `json:"firstName"`
		LastName  string  `json:"lastName"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&input); err != nil {
		jsonError(w, http.StatusBadRequest, "Invalid request")
		return nil
	}

	// Create the account, relaying validation errors like a taken username to the form
	var user created
	err := sess.call(r.Context(), http.MethodPost, "/users", map[string]any{
		"username":      input.Username,
		"email":         input.Email,
		"emailVerified": input.Email != nil,
		"firstName":     input.FirstName,
		"lastName":      input.LastName,
		"displayName":   strings.TrimSpace(input.FirstName + " " + input.LastName),
		"isAdmin":       true,
	}, &user)
	var apiErr apiError
	if errors.As(err, &apiErr) && apiErr.status < 500 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(apiErr.status)
		_, _ = w.Write(apiErr.body)
		return nil
	}
	if err != nil {
		return err
	}

	// Exchange a login code on the visitor's behalf so the audit log shows their browser and IP
	var code struct{ Token string }
	if err := sess.call(r.Context(), http.MethodPost, "/users/"+user.ID+"/one-time-access-token", struct{}{}, &code); err != nil {
		return err
	}
	body, err := json.Marshal(map[string]string{"token": code.Token})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, sess.url+"/api/one-time-access-token", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", r.UserAgent())
	req.Header.Set("X-Forwarded-For", s.clientIP(r))
	res, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	for _, c := range res.Header.Values("Set-Cookie") {
		w.Header().Add("Set-Cookie", c)
	}
	w.Header().Set("Content-Type", res.Header.Get("Content-Type"))
	w.WriteHeader(res.StatusCode)
	_, _ = io.Copy(w, res.Body)
	if res.StatusCode != http.StatusOK {
		return nil
	}
	sess.signedUp = true

	// Without the marker a restarted demo would offer this session's setup again
	if err := s.pool.markSignedUp(context.WithoutCancel(r.Context()), sess.containerID); err != nil {
		slog.Warn("Failed to mark the session as signed up", "container", sess.containerID, "error", err)
	}
	return nil
}
