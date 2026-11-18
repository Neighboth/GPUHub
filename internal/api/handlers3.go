package api

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gpuhub/internal/audit"
	"gpuhub/internal/proxy"
)

// notify stores an admin notification and (optionally) fires the configured webhook.
func (s *Server) notify(ctx context.Context, level, msg string) {
	_, _ = s.db.ExecContext(ctx, `INSERT INTO notifications(level, message) VALUES(?,?)`, level, msg)
	if s.cfg.NotifyWebhookURL != "" {
		go func() {
			body, _ := json.Marshal(map[string]string{"level": level, "message": msg, "source": "gpuhub"})
			c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(c, http.MethodPost, s.cfg.NotifyWebhookURL, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			_, _ = http.DefaultClient.Do(req)
		}()
	}
}

// ---------- proxies ----------

func (s *Server) handleListProxies(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT p.id, p.label, p.url, p.country, p.health_status, p.last_check_at, p.check_error, p.created_at,
		       k.id, k.label, k.provider_id
		FROM proxies p LEFT JOIN provider_keys k ON k.bound_proxy_id = p.id
		ORDER BY p.id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var label, encURL, country, health, checkErr string
		var lastCheck sql.NullTime
		var created time.Time
		var keyID sql.NullInt64
		var keyLabel, keyProvider string
		if err := rows.Scan(&id, &label, &encURL, &country, &health, &lastCheck, &checkErr, &created, &keyID, &keyLabel, &keyProvider); err != nil {
			continue
		}
		rawURL, _ := s.revealProxyURL(encURL)
		p := map[string]any{
			"id": id, "label": label, "url_masked": proxy.Redact(rawURL), "country": country,
			"health_status": health, "last_check_at": timeOrNull(lastCheck), "check_error": checkErr, "created_at": created,
			"bound_key_id": nil, "bound_key_label": nil, "bound_provider_id": nil,
		}
		if keyID.Valid {
			p["bound_key_id"] = keyID.Int64
			p["bound_key_label"] = keyLabel
			p["bound_provider_id"] = keyProvider
		}
		out = append(out, p)
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxies": out})
}

func (s *Server) handleCreateProxy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label   string `json:"label"`
		URL     string `json:"url"`
		Country string `json:"country"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	req.URL = strings.TrimSpace(req.URL)
	if req.Label == "" || req.URL == "" {
		writeError(w, http.StatusBadRequest, "label and url are required")
		return
	}
	if _, err := proxy.ParseProxyURL(req.URL); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	stored, err := s.storeProxyURL(req.URL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "encrypt failed")
		return
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO proxies(label, url, country) VALUES(?,?,?)`, req.Label, stored, req.Country)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	proxyID, _ := res.LastInsertId()
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "proxy_create", fmt.Sprintf("proxy_id=%d label=%s", proxyID, req.Label))
	writeJSON(w, http.StatusCreated, map[string]any{"id": proxyID, "label": req.Label, "url_masked": proxy.Redact(req.URL), "country": req.Country})
}

func (s *Server) handleUpdateProxy(w http.ResponseWriter, r *http.Request) {
	proxyID, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid proxy id")
		return
	}
	var req struct {
		Label   *string `json:"label"`
		URL     *string `json:"url"`
		Country *string `json:"country"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	var sets []string
	var args []any
	if req.Label != nil {
		sets = append(sets, "label=?")
		args = append(args, *req.Label)
	}
	if req.URL != nil {
		if _, err := proxy.ParseProxyURL(*req.URL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		stored, err := s.storeProxyURL(*req.URL)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encrypt failed")
			return
		}
		sets = append(sets, "url=?")
		args = append(args, stored)
		// Invalidate cached transports of ALL keys bound to this proxy (URL changed).
		rows, _ := s.db.QueryContext(r.Context(), `SELECT id FROM provider_keys WHERE bound_proxy_id=?`, proxyID)
		var keyIDs []int64
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			keyIDs = append(keyIDs, id)
		}
		rows.Close()
		for _, id := range keyIDs {
			s.proxies.InvalidateTransport(id)
		}
	}
	if req.Country != nil {
		sets = append(sets, "country=?")
		args = append(args, *req.Country)
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	args = append(args, proxyID)
	if _, err := s.db.ExecContext(r.Context(), `UPDATE proxies SET `+strings.Join(sets, ",")+` WHERE id=?`, args...); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "proxy_update", fmt.Sprintf("proxy_id=%d fields=%v", proxyID, sets))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteProxy(w http.ResponseWriter, r *http.Request) {
	proxyID, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid proxy id")
		return
	}
	var label string
	if err := s.db.QueryRowContext(r.Context(), `SELECT label FROM proxies WHERE id=?`, proxyID).Scan(&label); err != nil {
		writeError(w, http.StatusNotFound, "proxy not found")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(r.Context(), `SELECT id, label FROM provider_keys WHERE bound_proxy_id=?`, proxyID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var affected [][2]string
	for rows.Next() {
		var id int64
		var kl string
		_ = rows.Scan(&id, &kl)
		affected = append(affected, [2]string{fmt.Sprint(id), kl})
	}
	rows.Close()
	// Keys bound to this proxy become proxy-less and are DISABLED. No auto rebind (spec §6).
	if _, err := tx.ExecContext(r.Context(), `UPDATE provider_keys SET bound_proxy_id=NULL, active=0 WHERE bound_proxy_id=?`, proxyID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM proxies WHERE id=?`, proxyID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, a := range affected {
		id, _ := parseID(a[0])
		s.proxies.InvalidateTransport(id)
		s.notify(r.Context(), "critical", fmt.Sprintf("Proxy #%d (%s) was deleted. Key %q (id=%s) lost its sticky proxy binding and was DISABLED. It was NOT rebound automatically — assign a proxy via PATCH /api/v1/keys/%s/proxy.", proxyID, label, a[1], a[0], a[0]))
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "proxy_delete",
		fmt.Sprintf("proxy_id=%d label=%s disabled_keys=%v", proxyID, label, affected))
	writeJSON(w, http.StatusOK, map[string]any{"status": "deleted", "disabled_keys": affected})
}

func (s *Server) handleTestProxy(w http.ResponseWriter, r *http.Request) {
	proxyID, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid proxy id")
		return
	}
	var encURL string
	if err := s.db.QueryRowContext(r.Context(), `SELECT url FROM proxies WHERE id=?`, proxyID).Scan(&encURL); err != nil {
		writeError(w, http.StatusNotFound, "proxy not found")
		return
	}
	rawURL, err := s.revealProxyURL(encURL)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt failed")
		return
	}
	status, checkErr := s.proxies.TestProxy(r.Context(), rawURL, s.cfg.ProxyHealthURL, s.cfg.ProxyHealthTimeout)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE proxies SET health_status=?, last_check_at=CURRENT_TIMESTAMP, check_error=? WHERE id=?`, status, checkErr, proxyID)
	writeJSON(w, http.StatusOK, map[string]any{"ok": status == "healthy", "health_status": status, "error": checkErr})
}
