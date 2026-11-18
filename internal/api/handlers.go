package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gpuhub/internal/audit"
	"gpuhub/internal/auth"
	"gpuhub/internal/providers"
)

func providersAll() []providers.Provider                     { return providers.All() }
func providersDefaultDisabled(id string) bool                { return providers.DefaultDisabled[id] }
func mustWriteJSON(w http.ResponseWriter, status int, v any) { writeJSON(w, status, v) }

// ---------- auth ----------

type loginReq struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !s.loginRL.allow(clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "too many login attempts")
		return
	}
	var req loginReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var id int64
	var hash, role string
	err := s.db.QueryRowContext(r.Context(), `SELECT id, password_hash, role FROM users WHERE username=?`, req.Username).Scan(&id, &hash, &role)
	if err != nil {
		_ = auth.CompareOrDummy("", req.Password) // uniform timing
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	if err := auth.CompareOrDummy(hash, req.Password); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	access, err := s.authSvc.IssueAccess(id, req.Username, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	refresh, refreshHash, err := auth.NewRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	expires := time.Now().Add(auth.RefreshTokenTTL)
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO refresh_tokens(user_id, token_hash, expires_at) VALUES(?,?,?)`, id, refreshHash, expires)
	audit.Log(r.Context(), s.db, req.Username, "login", "user logged in")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": access, "refresh_token": refresh, "expires_in": int(auth.AccessTokenTTL.Seconds()),
		"user": map[string]string{"username": req.Username, "role": role},
	})
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req refreshReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	hash := auth.HashToken(req.RefreshToken)
	var userID int64
	var username, role string
	var expires time.Time
	err := s.db.QueryRowContext(r.Context(),
		`SELECT rt.user_id, u.username, u.role, rt.expires_at FROM refresh_tokens rt JOIN users u ON u.id=rt.user_id WHERE rt.token_hash=?`,
		hash).Scan(&userID, &username, &role, &expires)
	if err != nil || time.Now().After(expires) {
		writeError(w, http.StatusUnauthorized, "refresh token invalid or expired")
		return
	}
	// Rotate refresh token.
	_, _ = s.db.ExecContext(r.Context(), `DELETE FROM refresh_tokens WHERE token_hash=?`, hash)
	newRefresh, newHash, err := auth.NewRefreshToken()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `INSERT INTO refresh_tokens(user_id, token_hash, expires_at) VALUES(?,?,?)`,
		userID, newHash, time.Now().Add(auth.RefreshTokenTTL))
	access, err := s.authSvc.IssueAccess(userID, username, role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"access_token": access, "refresh_token": newRefresh, "expires_in": int(auth.AccessTokenTTL.Seconds())})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RefreshToken string `json:"refresh_token"`
	}
	_ = decodeJSON(r, &req)
	if req.RefreshToken != "" {
		_, _ = s.db.ExecContext(r.Context(), `DELETE FROM refresh_tokens WHERE token_hash=?`, auth.HashToken(req.RefreshToken))
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "logout", "")
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u := userFrom(r.Context())
	writeJSON(w, http.StatusOK, map[string]string{"username": u.Username, "role": u.Role})
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	var count int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&count)
	writeJSON(w, http.StatusOK, map[string]any{"needs_setup": count == 0})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var count int
	_ = s.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM users`).Scan(&count)
	if count > 0 {
		writeError(w, http.StatusConflict, "admin already exists")
		return
	}
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if len(req.Username) < 3 || len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "username ≥3 chars, password ≥8 chars")
		return
	}
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash failed")
		return
	}
	_, err = s.db.ExecContext(r.Context(), `INSERT INTO users(username, password_hash, role) VALUES(?,?,'admin')`, req.Username, hash)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create failed")
		return
	}
	audit.Log(r.Context(), s.db, "system", "admin_created", "admin created via first-run wizard: "+req.Username)
	writeJSON(w, http.StatusCreated, map[string]string{"status": "ok"})
}

func (s *Server) handleGPUTypes(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"gpu_types": providers.KnownGPUTypes(), "vram_gb": providers.GPUCatalog})
}

// ---------- providers ----------

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT p.id, p.enabled, p.config, p.created_at,
			(SELECT COUNT(*) FROM provider_keys k WHERE k.provider_id=p.id) AS key_count,
			(SELECT COUNT(*) FROM provider_keys k WHERE k.provider_id=p.id AND k.active=1) AS active_keys
		FROM providers p`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	type row struct {
		ID         string `json:"id"`
		Enabled    bool   `json:"enabled"`
		Config     string `json:"config"`
		KeyCount   int    `json:"key_count"`
		ActiveKeys int    `json:"active_keys"`
	}
	byID := map[string]*row{}
	for rows.Next() {
		var id, cfg string
		var enabled int
		var createdAt time.Time
		var kc, ak int
		if err := rows.Scan(&id, &enabled, &cfg, &createdAt, &kc, &ak); err != nil {
			continue
		}
		byID[id] = &row{ID: id, Enabled: enabled == 1, Config: cfg, KeyCount: kc, ActiveKeys: ak}
	}
	var out []map[string]any
	for _, p := range providers.All() {
		caps := p.Capabilities()
		row, ok := byID[p.ID()]
		enabled := true
		keyCount, activeKeys := 0, 0
		if ok {
			enabled = row.Enabled
			keyCount = row.KeyCount
			activeKeys = row.ActiveKeys
		}
		out = append(out, map[string]any{
			"id": p.ID(), "name": p.Name(), "enabled": enabled,
			"capabilities": caps, "key_count": keyCount, "active_keys": activeKeys,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *Server) handleUpdateProvider(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Enabled *bool           `json:"enabled"`
		Config  json.RawMessage `json:"config"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if providers.Lookup(id) == nil {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	if req.Enabled != nil {
		v := 0
		if *req.Enabled {
			v = 1
		}
		_, _ = s.db.ExecContext(r.Context(), `UPDATE providers SET enabled=? WHERE id=?`, v, id)
		audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "provider_toggle", fmt.Sprintf("provider=%s enabled=%v", id, *req.Enabled))
	}
	if len(req.Config) > 0 {
		var probe any
		if err := json.Unmarshal(req.Config, &probe); err != nil {
			writeError(w, http.StatusBadRequest, "config must be valid JSON")
			return
		}
		_, _ = s.db.ExecContext(r.Context(), `UPDATE providers SET config=? WHERE id=?`, string(req.Config), id)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type createKeyReq struct {
	Label        string `json:"label"`
	Credential   string `json:"credential"`
	BoundProxyID int64  `json:"bound_proxy_id"`
	DailyQuota   int    `json:"daily_quota"`
	WeeklyQuota  int    `json:"weekly_quota"`
	Priority     int    `json:"priority"`
	Weight       int    `json:"weight"`
}

func (s *Server) handleCreateKey(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	if providers.Lookup(providerID) == nil {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	var req createKeyReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.Credential) == "" {
		writeError(w, http.StatusBadRequest, "credential is required")
		return
	}
	if req.BoundProxyID == 0 {
		writeError(w, http.StatusBadRequest, "bound_proxy_id is required — every key must have a permanent sticky proxy binding")
		return
	}
	if req.Priority == 0 {
		req.Priority = 100
	}
	if req.Weight <= 0 {
		req.Weight = 1
	}
	if req.Label == "" {
		req.Label = providerID + "-key"
	}
	enc, err := s.box.Encrypt(req.Credential)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encrypt failed")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	var proxyLabel string
	if err := tx.QueryRowContext(r.Context(), `SELECT label FROM proxies WHERE id=?`, req.BoundProxyID).Scan(&proxyLabel); err != nil {
		writeError(w, http.StatusBadRequest, "bound_proxy_id does not exist")
		return
	}
	var boundTo sql.NullInt64
	var boundLabel string
	row := tx.QueryRowContext(r.Context(), `SELECT id, label FROM provider_keys WHERE bound_proxy_id=?`, req.BoundProxyID)
	if err := row.Scan(&boundTo, &boundLabel); err == nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":           "proxy already bound to another key (1:1 sticky binding)",
			"bound_key_id":    boundTo.Int64,
			"bound_key_label": boundLabel,
			"hint":            "choose a different proxy, or reassign via PATCH /keys/{key_id}/proxy with force=true (admin approval)",
		})
		return
	}
	res, err := tx.ExecContext(r.Context(), `INSERT INTO provider_keys(provider_id,label,credential_enc,bound_proxy_id,active,daily_quota,weekly_quota,priority,weight)
		VALUES(?,?,?,?,1,?,?,?,?)`, providerID, req.Label, enc, req.BoundProxyID, req.DailyQuota, req.WeeklyQuota, req.Priority, req.Weight)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "proxy already bound to another key (1:1 sticky binding)")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	keyID, _ := res.LastInsertId()
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "key_create",
		fmt.Sprintf("provider=%s key_id=%d label=%s proxy_id=%d", providerID, keyID, req.Label, req.BoundProxyID))
	writeJSON(w, http.StatusCreated, map[string]any{"id": keyID, "provider_id": providerID, "label": req.Label, "bound_proxy_id": req.BoundProxyID})
}

func (s *Server) testKey(ctx context.Context, keyID int64, providerID string) error {
	var enc string
	if err := s.db.QueryRowContext(ctx, `SELECT credential_enc FROM provider_keys WHERE id=?`, keyID).Scan(&enc); err != nil {
		return err
	}
	raw, err := s.box.Decrypt(enc)
	if err != nil {
		return err
	}
	p := providers.Lookup(providerID)
	if p == nil {
		return fmt.Errorf("unknown provider")
	}
	var proxyID sql.NullInt64
	var proxyURL string
	_ = s.db.QueryRowContext(ctx, `SELECT bound_proxy_id FROM provider_keys WHERE id=?`, keyID).Scan(&proxyID)
	if proxyID.Valid {
		var encURL string
		_ = s.db.QueryRowContext(ctx, `SELECT url FROM proxies WHERE id=?`, proxyID.Int64).Scan(&encURL)
		proxyURL, _ = s.revealProxyURL(encURL)
	}
	testCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return p.ValidateKey(testCtx, providers.Credential{KeyID: keyID, Raw: raw}, proxyURL)
}

func (s *Server) handleTestKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := parseID(chi.URLParam(r, "key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	var providerID string
	if err := s.db.QueryRowContext(r.Context(), `SELECT provider_id FROM provider_keys WHERE id=?`, keyID).Scan(&providerID); err != nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if err := s.testKey(r.Context(), keyID, providerID); err != nil {
		audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "key_test", fmt.Sprintf("key_id=%d result=fail err=%s", keyID, truncateStr(err.Error(), 300)))
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "key_test", fmt.Sprintf("key_id=%d result=ok", keyID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTestProvider(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	var req struct {
		KeyID *int64 `json:"key_id"`
	}
	_ = decodeJSON(r, &req)
	q := `SELECT id FROM provider_keys WHERE provider_id=? AND active=1`
	args := []any{providerID}
	if req.KeyID != nil {
		q += ` AND id=?`
		args = append(args, *req.KeyID)
	}
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var keyIDs []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		keyIDs = append(keyIDs, id)
	}
	rows.Close()
	if len(keyIDs) == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": "no active keys for provider"})
		return
	}
	results := []map[string]any{}
	for _, id := range keyIDs {
		err := s.testKey(r.Context(), id, providerID)
		e := ""
		if err != nil {
			e = err.Error()
		}
		results = append(results, map[string]any{"key_id": id, "ok": err == nil, "error": e})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (s *Server) handleProviderQuota(w http.ResponseWriter, r *http.Request) {
	providerID := chi.URLParam(r, "id")
	p := providers.Lookup(providerID)
	if p == nil {
		writeError(w, http.StatusNotFound, "unknown provider")
		return
	}
	rows, err := s.db.QueryContext(r.Context(), `SELECT id,label,credential_enc,bound_proxy_id FROM provider_keys WHERE provider_id=?`, providerID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	type keyRef struct {
		id      int64
		label   string
		credEnc string
		proxyID sql.NullInt64
	}
	var keys []keyRef
	for rows.Next() {
		var k keyRef
		_ = rows.Scan(&k.id, &k.label, &k.credEnc, &k.proxyID)
		keys = append(keys, k)
	}
	rows.Close()
	out := []map[string]any{}
	for _, k := range keys {
		raw, err := s.box.Decrypt(k.credEnc)
		if err != nil {
			out = append(out, map[string]any{"key_id": k.id, "label": k.label, "error": "decrypt failed"})
			continue
		}
		proxyURL := ""
		if k.proxyID.Valid {
			var encURL string
			_ = s.db.QueryRowContext(r.Context(), `SELECT url FROM proxies WHERE id=?`, k.proxyID.Int64).Scan(&encURL)
			proxyURL, _ = s.revealProxyURL(encURL)
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		q, err := p.Quota(ctx, providers.Credential{KeyID: k.id, Raw: raw}, proxyURL)
		cancel()
		if err != nil {
			out = append(out, map[string]any{"key_id": k.id, "label": k.label, "error": err.Error()})
			continue
		}
		out = append(out, map[string]any{"key_id": k.id, "label": k.label, "quota": q})
	}
	writeJSON(w, http.StatusOK, map[string]any{"provider_id": providerID, "keys": out})
}
