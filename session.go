package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
	"golang.org/x/time/rate"
)

type publicError struct {
	status  int
	message string
}

func (e publicError) Error() string { return e.message }

type session struct {
	instance
	target *url.URL

	mu sync.Mutex
	// signedUp is set once the visitor created their admin account, from then on the instance answers setup requests itself
	signedUp bool
}

type pool struct {
	cfg           config
	docker        *client.Client
	encryptionKey string
	// template is the seeded /app/data directory as a tar archive with a "data/" prefix
	template atomic.Pointer[[]byte]
	// building is set while the seeder container exists, so the sweep leaves it alone
	building atomic.Bool

	mu sync.Mutex
	// sessions holds every running session, or nil while it is still starting
	sessions map[string]*session
	limiters map[string]*rate.Limiter
}

func newPool(cfg config, docker *client.Client) *pool {
	return &pool{
		cfg:    cfg,
		docker: docker,
		// Every instance shares the key because the template's encrypted signing keys were created with it
		encryptionKey: randomHex(32),
		sessions:      map[string]*session{},
		limiters:      map[string]*rate.Limiter{},
	}
}

// start runs a new instance from the template and returns the URL of its setup page
func (p *pool) start(ctx context.Context, ip string) (string, error) {
	tmpl := p.template.Load()
	if tmpl == nil {
		return "", publicError{http.StatusServiceUnavailable, "The demo is getting ready, try again in a minute."}
	}
	id, err := p.reserve(ip)
	if err != nil {
		return "", err
	}

	appURL := p.sessionURL(id)
	expires := time.Now().Add(p.cfg.sessionDuration)
	inst, err := p.startInstance(ctx, id, appURL, *tmpl, expires)
	if err != nil {
		p.release(id)
		return "", err
	}
	if err := p.publish(id, inst, false, expires); err != nil {
		p.removeContainer(inst.containerID)
		p.release(id)
		return "", err
	}

	slog.Info("Session started", "session", id, "ip", ip)
	return appURL + "/setup", nil
}

// publish makes a session reachable and schedules its end
func (p *pool) publish(id string, inst instance, signedUp bool, expires time.Time) error {
	target, err := url.Parse(inst.url)
	if err != nil {
		return err
	}
	p.mu.Lock()
	p.sessions[id] = &session{instance: inst, target: target, signedUp: signedUp}
	p.mu.Unlock()
	time.AfterFunc(time.Until(expires), func() {
		slog.Info("Session ended", "session", id)
		p.removeContainer(inst.containerID)
		p.release(id)
	})
	return nil
}

// adopt takes over the sessions of a previous run, so updating the demo doesn't end them, and removes every other demo container
func (p *pool) adopt(ctx context.Context) {
	list, err := p.docker.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: make(client.Filters).Add("label", sessionLabel)})
	if err != nil {
		slog.Error("Failed to list demo containers", "error", err)
		return
	}
	for _, c := range list.Items {
		id := c.Labels[sessionLabel]
		if err := p.adoptContainer(ctx, c); err != nil {
			slog.Info("Removing container", "session", id, "reason", err)
			p.removeContainer(c.ID)
			continue
		}
		slog.Info("Adopted session", "session", id)
	}
}

func (p *pool) adoptContainer(ctx context.Context, c container.Summary) error {
	id := c.Labels[sessionLabel]
	expires, err := time.Parse(time.RFC3339, c.Labels[expiresLabel])
	switch {
	case !isSessionID(id) || err != nil:
		return errors.New("not a session")
	case !time.Now().Before(expires):
		return errors.New("expired")
	case c.State != container.StateRunning:
		return errors.New("not running")
	}

	inst, err := p.inspectInstance(ctx, c.ID)
	if err != nil {
		return err
	}
	signedUp, err := p.isSignedUp(ctx, c.ID)
	if err != nil {
		return err
	}
	return p.publish(id, inst, signedUp, expires)
}

// reserve claims a session slot and ID so concurrent starts can't exceed the limits
func (p *pool) reserve(ip string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.sessions) >= p.cfg.maxSessions {
		return "", publicError{http.StatusServiceUnavailable, "Maximum number of sessions reached. Please try again later."}
	}
	if p.cfg.rateLimitDemos > 0 {
		key := rateLimitKey(ip)
		limiter := p.limiters[key]
		if limiter == nil {
			limiter = rate.NewLimiter(rate.Every(p.cfg.rateLimitInterval), p.cfg.rateLimitDemos)
			p.limiters[key] = limiter
		}
		if !limiter.Allow() {
			return "", publicError{http.StatusTooManyRequests, "Your IP has reached the limit of demos. Please try again later."}
		}
	}

	for {
		id := randomHex(4)
		if _, taken := p.sessions[id]; !taken {
			p.sessions[id] = nil
			return id, nil
		}
	}
}

// sweep removes containers no session owns anymore, like ones whose removal failed, and forgets limiters that are full again
func (p *pool) sweep(ctx context.Context) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		containers, err := p.listContainers(ctx)
		if err != nil {
			slog.Error("Failed to list demo containers", "error", err)
			continue
		}
		var orphans []string
		p.mu.Lock()
		for containerID, id := range containers {
			if _, owned := p.sessions[id]; !owned && (id != templateID || !p.building.Load()) {
				orphans = append(orphans, containerID)
			}
		}
		for key, limiter := range p.limiters {
			if limiter.Tokens() >= float64(p.cfg.rateLimitDemos) {
				delete(p.limiters, key)
			}
		}
		p.mu.Unlock()

		for _, containerID := range orphans {
			slog.Warn("Removing orphaned container", "container", containerID, "session", containers[containerID])
			p.removeContainer(containerID)
		}
	}
}

// rateLimitKey groups IPv6 addresses by their /64 because one subscriber usually gets a whole /64 and could otherwise use a new address for every demo
func rateLimitKey(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	addr = addr.Unmap()
	if addr.Is4() {
		return addr.String()
	}
	return netip.PrefixFrom(addr.WithZone(""), 64).Masked().String()
}

func (p *pool) release(id string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.sessions, id)
}

func (p *pool) session(id string) *session {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessions[id]
}

func (p *pool) sessionURL(id string) string {
	return p.cfg.baseURL.Scheme + "://" + id + "." + p.cfg.baseURL.Host
}

func isSessionID(s string) bool {
	if len(s) != 8 {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
