package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"gpuhub/internal/audit"
	"gpuhub/internal/auth"
	"gpuhub/internal/config"
	"gpuhub/internal/crypto"
	"gpuhub/internal/proxy"
)

type Server struct {
	cfg     *config.Config
	db      *sql.DB
	box     *crypto.Box
	authSvc *auth.Service
	proxies *proxy.Manager
	static  fs.FS // web/dist, may be nil
	router  *chi.Mux

	loginRL *rateLimiter
	apiRL   *rateLimiter

	onJobCreated func()
}

func (s *Server) SetOnJobCreated(fn func()) { s.onJobCreated = fn }

func NewServer(cfg *config.Config, db *sql.DB, box *crypto.Box, authSvc *auth.Service, proxyMgr *proxy.Manager, static fs.FS) *Server {
	s := &Server{
		cfg: cfg, db: db, box: box, authSvc: authSvc, proxies: proxyMgr, static: static,
		loginRL: newRateLimiter(10, time.Minute),
		apiRL:   newRateLimiter(600, time.Minute),
	}
	s.router = s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.router }

// SeedProviders inserts missing provider catalog rows; disabled-by-default providers get enabled=0.
func (s *Server) SeedProviders(ctx context.Context) {
	// Remove catalog rows for adapters that no longer exist (e.g. providers removed
	// in the recurring-free-tier audit). Their keys are deleted too, via FK cascade.
	registered := map[string]bool{}
	for _, p := range providersAll() {
		registered[p.ID()] = true
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM providers`)
	if err == nil {
		var stale []string
		for rows.Next() {
			var id string
			_ = rows.Scan(&id)
			if !registered[id] {
				stale = append(stale, id)
			}
		}
		rows.Close()
		for _, id := range stale {
			_, _ = s.db.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
		}
	}

	for _, p := range providersAll() {
		enabled := 1
		if providersDefaultDisabled(p.ID()) {
			enabled = 0
		}
		_, _ = s.db.ExecContext(ctx, `INSERT INTO providers(id,name,enabled) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET name=excluded.name`,
			p.ID(), p.Name(), enabled)
	}
}

// BootstrapAdmin creates the .env-configured admin on first boot if no users exist.
func (s *Server) BootstrapAdmin(ctx context.Context) (created bool, err error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, nil
	}
	if s.cfg.AdminPassword == "" {
		return false, nil // needs_setup flow via UI
	}
	hash, err := auth.HashPassword(s.cfg.AdminPassword)
	if err != nil {
		return false, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO users(username, password_hash, role) VALUES(?,?,?)`,
		s.cfg.AdminUsername, hash, "admin")
	if err != nil {
		return false, err
	}
	audit.Log(ctx, s.db, "system", "admin_created", "admin bootstrapped from ADMIN_USERNAME/ADMIN_PASSWORD")
	return true, nil
}

func (s *Server) routes() *chi.Mux {
	r := chi.NewRouter()
	r.Use(middleware.Recoverer)
	r.Use(middleware.Compress(5))
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("Referrer-Policy", "same-origin")
			w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self' ws: wss:")
			next.ServeHTTP(w, req)
		})
	})

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if !s.apiRL.allow(clientIP(req)) {
					writeError(w, http.StatusTooManyRequests, "rate limit exceeded")
					return
				}
				next.ServeHTTP(w, req)
			})
		})
		r.Use(RequireXMLHttpRequest)

		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/refresh", s.handleRefresh)
		r.Post("/auth/setup", s.handleSetup)
		r.Get("/auth/status", s.handleAuthStatus)

		// Authenticated routes
		r.Group(func(r chi.Router) {
			r.Use(s.authMiddleware)

			r.Post("/auth/logout", s.handleLogout)
			r.Get("/auth/me", s.handleMe)

			r.Get("/gpu-types", s.handleGPUTypes)

			r.Get("/providers", s.handleListProviders)
			r.Get("/providers/colab/oauth-url", s.handleColabOAuthURL)
			r.Patch("/providers/{id}", s.handleUpdateProvider)
			r.Post("/providers/{id}/keys", s.handleCreateKey)
			r.Post("/providers/{id}/test", s.handleTestProvider)
			r.Get("/providers/{id}/quota", s.handleProviderQuota)

			r.Get("/keys", s.handleListKeys)
			r.Patch("/keys/{key_id}", s.handleUpdateKey)
			r.Patch("/keys/{key_id}/proxy", s.adminOnly(s.handleReassignProxy))
			r.Delete("/keys/{key_id}", s.adminOnly(s.handleDeleteKey))
			r.Post("/keys/{key_id}/test", s.handleTestKey)
			r.Post("/keys/{key_id}/reveal", s.adminOnly(s.handleRevealKey))

			r.Get("/proxies", s.handleListProxies)
			r.Post("/proxies", s.handleCreateProxy)
			r.Patch("/proxies/{id}", s.handleUpdateProxy)
			r.Delete("/proxies/{id}", s.adminOnly(s.handleDeleteProxy))
			r.Post("/proxies/{id}/test", s.handleTestProxy)

			r.Post("/jobs", s.handleCreateJob)
			r.Get("/jobs", s.handleListJobs)
			r.Get("/jobs/{id}", s.handleGetJob)
			r.Get("/jobs/{id}/logs", s.handleJobLogs) // WebSocket upgrade; token via query param
			r.Post("/jobs/{id}/cancel", s.handleCancelJob)

			r.Get("/dashboard/summary", s.handleDashboardSummary)

			r.Get("/audit", s.handleAudit)
			r.Get("/notifications", s.handleNotifications)
			r.Post("/notifications/{id}/read", s.handleNotificationRead)

			r.Get("/settings", s.handleGetSettings)
			r.Patch("/settings", s.adminOnly(s.handleUpdateSettings))
			r.Post("/settings/rotate-master-key", s.adminOnly(s.handleRotateMasterKey))
			r.Post("/settings/test-webhook", s.adminOnly(s.handleTestWebhook))
		})
	})

	// OpenAPI spec + Swagger UI
	r.Get("/docs", s.handleSwaggerUI)
	r.Get("/openapi.yaml", s.handleOpenAPISpec)

	// SPA
	r.NotFound(s.handleSPA)
	return r
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := ""
		_, after, ok := strings.Cut(r.Header.Get("Authorization"), " ")
		if ok && strings.HasPrefix(strings.ToLower(r.Header.Get("Authorization")), "bearer") {
			tok = after
		}
		if tok == "" {
			// WebSocket convenience: /api/v1/jobs/{id}/logs?token=...
			if t := r.URL.Query().Get("token"); t != "" {
				tok = t
			}
		}
		if tok == "" {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}
		claims, err := s.authSvc.ParseAccess(tok)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "invalid or expired token")
			return
		}
		ctx := context.WithValue(r.Context(), ctxKeyUser, claims)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type ctxKey string

const ctxKeyUser ctxKey = "user"

func userFrom(ctx context.Context) *auth.Claims {
	c, _ := ctx.Value(ctxKeyUser).(*auth.Claims)
	return c
}

func (s *Server) adminOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u := userFrom(r.Context())
		if u == nil || u.Role != "admin" {
			writeError(w, http.StatusForbidden, "admin role required")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decodeJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if s.static == nil {
		http.Error(w, "GPUHub backend is running, but the frontend was not built. See README (web/ directory, npm run build).", http.StatusServiceUnavailable)
		return
	}
	p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if p == "" {
		p = "index.html"
	}
	f, err := s.static.Open(p)
	if err != nil {
		// SPA fallback: serve index.html for client routes
		f, err = s.static.Open("index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	defer f.Close()
	stat, _ := f.Stat()
	ext := strings.ToLower(path.Ext(p))
	ctype := map[string]string{
		".html":  "text/html; charset=utf-8",
		".js":    "text/javascript; charset=utf-8",
		".css":   "text/css; charset=utf-8",
		".svg":   "image/svg+xml",
		".png":   "image/png",
		".ico":   "image/x-icon",
		".json":  "application/json",
		".woff2": "font/woff2",
	}[ext]
	if ctype != "" {
		w.Header().Set("Content-Type", ctype)
	}
	if ext == ".html" {
		w.Header().Set("Cache-Control", "no-cache")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeContent(w, r, p, stat.ModTime(), mustReadSeeker(f))
}

func mustReadSeeker(f fs.File) io.ReadSeeker {
	if rs, ok := f.(io.ReadSeeker); ok {
		return rs
	}
	b, _ := io.ReadAll(f)
	return bytes.NewReader(b)
}

func (s *Server) handleSwaggerUI(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(`<!doctype html><html><head><title>GPUHub API docs</title><link rel="stylesheet" href="https://unpkg.com/swagger-ui-dist@5/swagger-ui.css"></head>
<body><div id="ui"></div><script src="https://unpkg.com/swagger-ui-dist@5/swagger-ui-bundle.js"></script>
<script>SwaggerUIBundle({url: '/openapi.yaml', dom_id: '#ui'});</script></body></html>`))
}

func (s *Server) handleOpenAPISpec(w http.ResponseWriter, r *http.Request) {
	p := os.Getenv("GPUHUB_OPENAPI_PATH")
	if p == "" {
		p = "docs/openapi.yaml"
	}
	b, err := os.ReadFile(p)
	if err != nil {
		writeError(w, http.StatusNotFound, "openapi.yaml not found at "+p)
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	_, _ = w.Write(b)
}

var _ = log.Printf
