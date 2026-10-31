package main

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/moby/moby/client"
)

//go:embed web
var webFS embed.FS

type config struct {
	baseURL         *url.URL
	port            string
	image           string
	network         string
	sessionDuration time.Duration
	maxSessions     int
	trustProxy      bool
	// rateLimitDemos is how many demos an IP can start in a row, zero turns the limit off
	rateLimitDemos int
	// rateLimitInterval is how long an IP waits for each further demo
	rateLimitInterval time.Duration
}

func main() {
	if err := run(); err != nil {
		slog.Error("Demo stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	docker, err := client.New(client.FromEnv)
	if err != nil {
		return fmt.Errorf("connect to Docker: %w", err)
	}
	defer docker.Close()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	p := newPool(cfg, docker)
	if err := p.ensureNetwork(ctx); err != nil {
		return fmt.Errorf("set up network %q: %w", cfg.network, err)
	}

	p.adopt(ctx)

	srv, err := newServer(cfg, p)
	if err != nil {
		return err
	}
	var workers sync.WaitGroup
	workers.Go(func() { p.keepTemplateFresh(ctx) })
	workers.Go(func() { p.sweep(ctx) })

	// Requests inherit the shutdown signal, so a demo that is still starting gets removed instead of leaking
	// There is no WriteTimeout because it would cut off long-lived proxied responses like websockets
	httpServer := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           srv,
		BaseContext:       func(net.Listener) context.Context { return ctx },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	shutdown := make(chan struct{})
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		close(shutdown)
	}()

	slog.Info("Listening", "port", cfg.port, "baseURL", cfg.baseURL.String())
	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
		<-shutdown
	}

	// Running sessions stay up for the next run to adopt, while starting ones and the seeder clean up after themselves before this returns
	stop()
	workers.Wait()
	return err
}

func loadConfig() (config, error) {
	baseURL, err := url.Parse(env("BASE_URL", "http://localhost:8080"))
	if err != nil || baseURL.Host == "" {
		return config{}, fmt.Errorf("BASE_URL must be an absolute URL like https://demo.pocket-id.org")
	}
	sessionDuration, err := time.ParseDuration(env("SESSION_DURATION", "1h"))
	if err != nil {
		return config{}, fmt.Errorf("SESSION_DURATION: %w", err)
	}
	maxSessions, err := strconv.Atoi(env("MAX_SESSIONS", "30"))
	if err != nil {
		return config{}, fmt.Errorf("MAX_SESSIONS: %w", err)
	}
	rateLimitDemos, err := strconv.Atoi(env("RATE_LIMIT_DEMOS", "5"))
	if err != nil {
		return config{}, fmt.Errorf("RATE_LIMIT_DEMOS: %w", err)
	}
	rateLimitInterval, err := time.ParseDuration(env("RATE_LIMIT_INTERVAL", "15m"))
	if err != nil {
		return config{}, fmt.Errorf("RATE_LIMIT_INTERVAL: %w", err)
	}
	return config{
		baseURL:         baseURL,
		port:            env("PORT", "8080"),
		image:           env("POCKET_ID_IMAGE", "ghcr.io/pocket-id/pocket-id:next-v3"),
		network:         env("DOCKER_NETWORK", "pocket-id-demo"),
		sessionDuration: sessionDuration,
		maxSessions:     maxSessions,
		trustProxy:      env("TRUST_PROXY", "false") == "true",

		rateLimitDemos:    rateLimitDemos,
		rateLimitInterval: rateLimitInterval,
	}, nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

type server struct {
	cfg        config
	pool       *pool
	proxy      *httputil.ReverseProxy
	csrf       *http.CrossOriginProtection
	static     http.Handler
	index      *template.Template
	message    *template.Template
	cssVersion string
}

type messagePage struct {
	Title, Text, ActionText, ActionURL string
}

func newServer(cfg config, p *pool) (*server, error) {
	css, err := webFS.ReadFile("web/static/main.css")
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(css)
	static, err := fs.Sub(webFS, "web/static")
	if err != nil {
		return nil, err
	}
	s := &server{
		cfg:        cfg,
		pool:       p,
		csrf:       http.NewCrossOriginProtection(),
		static:     http.StripPrefix(assetPrefix, http.FileServerFS(static)),
		index:      template.Must(template.ParseFS(webFS, "web/base.html", "web/index.html")),
		message:    template.Must(template.ParseFS(webFS, "web/base.html", "web/message.html")),
		cssVersion: hex.EncodeToString(sum[:4]),
	}
	s.proxy = &httputil.ReverseProxy{Rewrite: s.rewrite, ErrorHandler: s.proxyError}
	return s, nil
}

// assetPrefix is served on session hosts too, so the error pages there are styled, and Pocket ID itself doesn't use it
const assetPrefix = "/_demo/"

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, assetPrefix) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		s.static.ServeHTTP(w, r)
		return
	}
	if id, ok := s.sessionID(r.Host); ok {
		s.proxySession(w, r, id)
		return
	}

	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		s.render(w, http.StatusOK, s.index, map[string]string{"Duration": formatDuration(s.cfg.sessionDuration)})
	case r.URL.Path == "/start" && r.Method == http.MethodPost:
		s.start(w, r)
	default:
		s.sendError(w, r, http.StatusNotFound, "Not found", messagePage{
			Title:      "Page not found",
			Text:       "There's nothing here. Start a demo to try Pocket ID.",
			ActionText: "Go to the demo",
			ActionURL:  "/",
		})
	}
}

// sessionID returns the session ID of a host like "<id>.demo.pocket-id.org"
func (s *server) sessionID(host string) (string, bool) {
	id, ok := strings.CutSuffix(strings.ToLower(host), "."+strings.ToLower(s.cfg.baseURL.Host))
	return id, ok && isSessionID(id)
}

func (s *server) start(w http.ResponseWriter, r *http.Request) {
	// Other sites could otherwise start demos from their visitors' browsers and use up the slots and rate limits
	if err := s.csrf.Check(r); err != nil {
		jsonError(w, http.StatusForbidden, "Cross-origin requests are not allowed.")
		return
	}
	setupURL, err := s.pool.start(r.Context(), s.clientIP(r))
	if err == nil {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"url": setupURL})
		return
	}

	var pubErr publicError
	if errors.As(err, &pubErr) {
		jsonError(w, pubErr.status, pubErr.message)
		return
	}
	slog.Error("Failed to start a demo", "error", err)
	jsonError(w, http.StatusInternalServerError, "The demo couldn't be started. Please try again later.")
}

// jsonError answers in the error format of Pocket ID's API, which both the start page and the app read
func jsonError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

type targetKey struct{}

func (s *server) proxySession(w http.ResponseWriter, r *http.Request, id string) {
	if sess := s.pool.session(id); sess != nil {
		if r.URL.Path == "/api/signup/setup" && s.handleSetup(w, r, sess) {
			return
		}
		s.proxy.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), targetKey{}, sess.target)))
		return
	}
	s.sendError(w, r, http.StatusNotFound, "Session not found", messagePage{
		Title:      "This demo has ended",
		Text:       fmt.Sprintf("Demo instances are deleted after %s, together with everything in them. Start a new one to keep exploring Pocket ID.", formatDuration(s.cfg.sessionDuration)),
		ActionText: "Start a new demo",
		ActionURL:  s.cfg.baseURL.String(),
	})
}

func (s *server) rewrite(pr *httputil.ProxyRequest) {
	pr.SetURL(pr.In.Context().Value(targetKey{}).(*url.URL))
	pr.Out.Host = pr.In.Host
	if s.cfg.trustProxy {
		pr.Out.Header["X-Forwarded-For"] = pr.In.Header["X-Forwarded-For"]
	}
	pr.SetXForwarded()
	pr.Out.Header.Set("X-Forwarded-Proto", s.cfg.baseURL.Scheme)
}

func (s *server) proxyError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Warn("Proxy request failed", "host", r.Host, "error", err)
	s.sendError(w, r, http.StatusBadGateway, "Unable to connect to the instance", messagePage{
		Title:      "The demo isn't responding",
		Text:       "Your Pocket ID instance didn't answer in time. Start a new demo to try again.",
		ActionText: "Start a new demo",
		ActionURL:  s.cfg.baseURL.String(),
	})
}

// sendError shows browsers a branded page and answers everything else, like the app's API requests, in plain text
func (s *server) sendError(w http.ResponseWriter, r *http.Request, status int, message string, page messagePage) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		s.render(w, status, s.message, page)
		return
	}
	http.Error(w, message, status)
}

func (s *server) render(w http.ResponseWriter, status int, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	err := tmpl.ExecuteTemplate(w, "base", struct {
		CSSVersion string
		Page       any
	}{s.cssVersion, data})
	if err != nil {
		slog.Error("Failed to render page", "error", err)
	}
}

func (s *server) clientIP(r *http.Request) string {
	if s.cfg.trustProxy {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			ip, _, _ := strings.Cut(forwarded, ",")
			return strings.TrimSpace(ip)
		}
	}
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return ip
}

// formatDuration formats a duration for people, like "10 minutes" or "1 hour"
func formatDuration(d time.Duration) string {
	minutes := int(d.Round(time.Minute).Minutes())
	if minutes >= 60 && minutes%60 == 0 {
		return plural(minutes/60, "hour")
	}
	return plural(minutes, "minute")
}

func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}
	return fmt.Sprintf("%d %ss", n, unit)
}
