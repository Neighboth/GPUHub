package api

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gpuhub/internal/audit"
	"gpuhub/internal/proxy"
)

func parseID(s string) (int64, error) { return strconv.ParseInt(s, 10, 64) }

func truncateStr(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// Proxy URLs may contain user:pass - they are stored encrypted ("enc:" prefix).
func (s *Server) storeProxyURL(raw string) (string, error) {
	enc, err := s.box.Encrypt(raw)
	if err != nil {
		return "", err
	}
	return "enc:" + enc, nil
}

func (s *Server) revealProxyURL(stored string) (string, error) {
	if strings.HasPrefix(stored, "enc:") {
		return s.box.Decrypt(strings.TrimPrefix(stored, "enc:"))
	}
	return stored, nil // legacy plaintext
}

func maskSecret(raw string) string {
	if len(raw) <= 4 {
		return strings.Repeat("•", 8)
	}
	return "••••••••" + raw[len(raw)-4:]
}

// ---------- keys ----------

func (s *Server) handleListKeys(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `
		SELECT k.id, k.provider_id, k.label, k.credential_enc, k.bound_proxy_id, COALESCE(p.label,''), COALESCE(p.url,''),
		       k.active, k.daily_quota, k.weekly_quota, k.usage_today, k.usage_week,
		       k.priority, k.weight, k.last_used_at, k.error_count, k.consecutive_errors, k.cooldown_until, k.created_at
		FROM provider_keys k LEFT JOIN proxies p ON p.id = k.bound_proxy_id
		ORDER BY k.provider_id, k.priority, k.id`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	var out []map[string]any
	for rows.Next() {
		var id int64
		var providerID, label, credEnc, proxyLabel, proxyEnc string
		var proxyID sql.NullInt64
		var active, dquota, wquota, utoday, uweek, priority, weight, errCount, consec int
		var lastUsed, cooldown sql.NullTime
		var created time.Time
		if err := rows.Scan(&id, &providerID, &label, &credEnc, &proxyID, &proxyLabel, &proxyEnc,
			&active, &dquota, &wquota, &utoday, &uweek, &priority, &weight, &lastUsed, &errCount, &consec, &cooldown, &created); err != nil {
			continue
		}
		tail := ""
		if raw, err := s.box.Decrypt(credEnc); err == nil {
			tail = maskSecret(raw)
		} else {
			tail = "••••••••(corrupt)"
		}
		proxyInfo := map[string]any(nil)
		if proxyID.Valid {
			rawURL, _ := s.revealProxyURL(proxyEnc)
			proxyInfo = map[string]any{"id": proxyID.Int64, "label": proxyLabel, "url_masked": proxy.Redact(rawURL)}
		}
		var boundProxyOut any
		if proxyID.Valid {
			id := proxyID.Int64
			boundProxyOut = id
		}
		out = append(out, map[string]any{
			"id": id, "provider_id": providerID, "label": label, "credential_masked": tail,
			"bound_proxy": proxyInfo, "bound_proxy_id": boundProxyOut, "bound_proxy_label": proxyInfoLabel(proxyInfo),
			"active": active == 1, "daily_quota": dquota, "weekly_quota": wquota,
			"usage_today": utoday, "usage_week": uweek, "priority": priority, "weight": weight,
			"last_used_at": timeOrNull(lastUsed), "error_count": errCount, "consecutive_errors": consec,
			"cooldown_until": timeOrNull(cooldown), "created_at": created,
		})
	}
	if out == nil {
		out = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

func proxyInfoLabel(m map[string]any) any {
	if m == nil {
		return nil
	}
	return m["label"]
}

func timeOrNull(t sql.NullTime) any {
	if !t.Valid {
		return nil
	}
	return t.Time
}

func (s *Server) handleUpdateKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := parseID(chi.URLParam(r, "key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	var req struct {
		Label       *string `json:"label"`
		DailyQuota  *int    `json:"daily_quota"`
		WeeklyQuota *int    `json:"weekly_quota"`
		Priority    *int    `json:"priority"`
		Weight      *int    `json:"weight"`
		Active      *bool   `json:"active"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sets := []string{}
	args := []any{}
	add := func(col string, v any) { sets = append(sets, col+"=?"); args = append(args, v) }
	if req.Label != nil {
		add("label", *req.Label)
	}
	if req.DailyQuota != nil {
		add("daily_quota", *req.DailyQuota)
	}
	if req.WeeklyQuota != nil {
		add("weekly_quota", *req.WeeklyQuota)
	}
	if req.Priority != nil {
		add("priority", *req.Priority)
	}
	if req.Weight != nil {
		add("weight", *req.Weight)
	}
	if req.Active != nil {
		v := 0
		if *req.Active {
			v = 1
		}
		add("active", v)
		// Manual re-activate clears cooldown state.
		if *req.Active {
			add("consecutive_errors", 0)
			add("cooldown_until", nil)
		}
	}
	if len(sets) == 0 {
		writeError(w, http.StatusBadRequest, "nothing to update")
		return
	}
	args = append(args, keyID)
	res, err := s.db.ExecContext(r.Context(), `UPDATE provider_keys SET `+strings.Join(sets, ",")+` WHERE id=?`, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "key_update", fmt.Sprintf("key_id=%d fields=%v", keyID, sets))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := parseID(chi.URLParam(r, "key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	var label string
	if err := tx.QueryRowContext(r.Context(), `SELECT label FROM provider_keys WHERE id=?`, keyID).Scan(&label); err != nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	_, _ = tx.ExecContext(r.Context(), `UPDATE jobs SET key_id=NULL WHERE key_id=?`, keyID)
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM provider_keys WHERE id=?`, keyID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.proxies.InvalidateTransport(keyID)
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "key_delete", fmt.Sprintf("key_id=%d label=%s", keyID, label))
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

type reassignReq struct {
	ProxyID int64 `json:"proxy_id"` // 0 = unbind (key becomes direct egress)
	Force   bool  `json:"force"`    // admin approval to steal the proxy from another key
}

func (s *Server) handleReassignProxy(w http.ResponseWriter, r *http.Request) {
	keyID, err := parseID(chi.URLParam(r, "key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	var req reassignReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	username := userFrom(r.Context()).Username
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	var keyLabel, oldProxyLabel string
	var oldProxyID sql.NullInt64
	if err := tx.QueryRowContext(r.Context(), `SELECT label, bound_proxy_id FROM provider_keys WHERE id=?`, keyID).Scan(&keyLabel, &oldProxyID); err != nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	if oldProxyID.Valid {
		_ = tx.QueryRowContext(r.Context(), `SELECT label FROM proxies WHERE id=?`, oldProxyID.Int64).Scan(&oldProxyLabel)
	} else {
		oldProxyLabel = "(none)"
	}
	var newProxyLabel string
	if req.ProxyID != 0 {
		if err := tx.QueryRowContext(r.Context(), `SELECT label FROM proxies WHERE id=?`, req.ProxyID).Scan(&newProxyLabel); err != nil {
			writeError(w, http.StatusBadRequest, "proxy does not exist")
			return
		}
	} else {
		newProxyLabel = "(direct - no proxy)"
	}
	var boundKeyID sql.NullInt64
	var boundKeyLabel string
	stolen := false
	if req.ProxyID != 0 {
		if err := tx.QueryRowContext(r.Context(), `SELECT id, label FROM provider_keys WHERE bound_proxy_id=? AND id<>?`, req.ProxyID, keyID).Scan(&boundKeyID, &boundKeyLabel); err == nil {
			if !req.Force {
				writeJSON(w, http.StatusConflict, map[string]any{
					"error":           "proxy already bound to another key",
					"bound_key_id":    boundKeyID.Int64,
					"bound_key_label": boundKeyLabel,
					"hint":            "resubmit with force=true to detach it from that key (it will be disabled) and rebind here",
				})
				return
			}
			// Admin-approved steal: detach + disable previous owner.
			if _, err := tx.ExecContext(r.Context(), `UPDATE provider_keys SET bound_proxy_id=NULL, active=0 WHERE id=?`, boundKeyID.Int64); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			stolen = true
		}
	} else {
		// Unbind: key stays active but switches to direct egress (sticky binding removed).
		if _, err := tx.ExecContext(r.Context(), `UPDATE provider_keys SET bound_proxy_id=NULL, active=1 WHERE id=?`, keyID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if err := tx.Commit(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		s.proxies.InvalidateTransport(keyID)
		audit.Log(r.Context(), s.db, username, "proxy_unbind",
			fmt.Sprintf("key_id=%d key=%q from_proxy=%q - key now uses direct egress", keyID, keyLabel, oldProxyLabel))
		s.notify(r.Context(), "info", fmt.Sprintf("Key %q (id=%d) was unbound from proxy %q by %s - now uses direct egress.", keyLabel, keyID, oldProxyLabel, username))
		writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "key_id": keyID, "proxy_id": 0, "direct": true})
		return
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE provider_keys SET bound_proxy_id=?, active=1 WHERE id=?`, req.ProxyID, keyID); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			writeError(w, http.StatusConflict, "proxy already bound to another key (1:1 sticky binding)")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if boundKeyID.Valid {
		s.proxies.InvalidateTransport(boundKeyID.Int64)
		if stolen {
			s.notify(r.Context(), "warn", fmt.Sprintf("Proxy #%d forcibly reassigned from key %q (id=%d) to key %q (id=%d) by %s. Previous key disabled - manual reassignment required.",
				req.ProxyID, boundKeyLabel, boundKeyID.Int64, keyLabel, keyID, username))
			audit.Log(r.Context(), s.db, username, "proxy_reassign_force",
				fmt.Sprintf("proxy_id=%d from_key_id=%d from_key=%q to_key_id=%d to_key=%q", req.ProxyID, boundKeyID.Int64, boundKeyLabel, keyID, keyLabel))
		}
	}
	s.proxies.InvalidateTransport(keyID)
	audit.Log(r.Context(), s.db, username, "proxy_reassign",
		fmt.Sprintf("key_id=%d key=%q proxy_id=%d proxy=%q from_proxy=%q", keyID, keyLabel, req.ProxyID, newProxyLabel, oldProxyLabel))
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "key_id": keyID, "proxy_id": req.ProxyID})
}

func (s *Server) handleRevealKey(w http.ResponseWriter, r *http.Request) {
	keyID, err := parseID(chi.URLParam(r, "key_id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid key id")
		return
	}
	var enc string
	var label string
	if err := s.db.QueryRowContext(r.Context(), `SELECT credential_enc, label FROM provider_keys WHERE id=?`, keyID).Scan(&enc, &label); err != nil {
		writeError(w, http.StatusNotFound, "key not found")
		return
	}
	raw, err := s.box.Decrypt(enc)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "decrypt failed (wrong master key?)")
		return
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "key_reveal", fmt.Sprintf("key_id=%d label=%s", keyID, label))
	writeJSON(w, http.StatusOK, map[string]string{"credential": raw})
}
