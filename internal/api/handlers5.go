package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"gpuhub/internal/audit"
	"gpuhub/internal/crypto"
)

// ---------- dashboard ----------

func (s *Server) handleDashboardSummary(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var running, queued int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status='running'`).Scan(&running)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status IN ('queued','scheduling')`).Scan(&queued)
	var succ24, fail24 int
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status='succeeded' AND finished_at >= datetime('now','-1 day')`).Scan(&succ24)
	_ = s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status IN ('failed','timeout') AND finished_at >= datetime('now','-1 day')`).Scan(&fail24)
	var costToday int64
	_ = s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(cost_usd_micros),0) FROM jobs WHERE date(finished_at)=date('now')`).Scan(&costToday)

	// 14-day cost series
	rows, err := s.db.QueryContext(ctx, `SELECT date(finished_at), COALESCE(SUM(cost_usd_micros),0)
		FROM jobs WHERE finished_at >= datetime('now','-14 day') GROUP BY date(finished_at) ORDER BY 1`)
	type dayCost struct {
		Date      string `json:"date"`
		USDMicros int64  `json:"usd_micros"`
	}
	series := []dayCost{}
	if err == nil {
		for rows.Next() {
			var d dayCost
			_ = rows.Scan(&d.Date, &d.USDMicros)
			series = append(series, d)
		}
		rows.Close()
	}

	// Provider quota utilization (avg over keys with limits)
	qrows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.name,
		       COALESCE(SUM(k.usage_today),0), COALESCE(SUM(k.daily_quota),0),
		       COUNT(k.id), COALESCE(SUM(k.active),0)
		FROM providers p LEFT JOIN provider_keys k ON k.provider_id=p.id
		GROUP BY p.id, p.name HAVING COUNT(k.id) > 0 ORDER BY p.name`)
	type provQuota struct {
		ID           string  `json:"id"`
		Name         string  `json:"name"`
		QuotaPercent float64 `json:"quota_percent"`
		KeyCount     int     `json:"key_count"`
		ActiveKeys   int     `json:"active_keys"`
	}
	providersSum := []provQuota{}
	if err == nil {
		for qrows.Next() {
			var p provQuota
			var used, limit int
			_ = qrows.Scan(&p.ID, &p.Name, &used, &limit, &p.KeyCount, &p.ActiveKeys)
			if limit > 0 {
				p.QuotaPercent = float64(used) / float64(limit) * 100
			}
			providersSum = append(providersSum, p)
		}
		qrows.Close()
	}

	// Recent errors
	errows_, err := s.db.QueryContext(ctx, `SELECT ts, actor, action, detail FROM audit_log WHERE action LIKE '%fail%' OR action LIKE '%error%' ORDER BY id DESC LIMIT 5`)
	type errRow struct {
		TS     string `json:"ts"`
		Actor  string `json:"actor"`
		Action string `json:"action"`
		Detail string `json:"detail"`
	}
	recentErrors := []errRow{}
	if err == nil {
		for errows_.Next() {
			var e errRow
			var ts time.Time
			_ = errows_.Scan(&ts, &e.Actor, &e.Action, &e.Detail)
			e.TS = ts.Format(time.RFC3339)
			recentErrors = append(recentErrors, e)
		}
		errows_.Close()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"running": running, "queued": queued,
		"succeeded_24h": succ24, "failed_24h": fail24,
		"cost_today_usd_micros": costToday,
		"cost_series":           series,
		"providers":             providersSum,
		"recent_errors":         recentErrors,
	})
}

// ---------- audit ----------

func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := 200
	rows, err := s.db.QueryContext(r.Context(), `SELECT ts, actor, action, detail FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]string{}
	for rows.Next() {
		var ts time.Time
		var actor, action, detail string
		_ = rows.Scan(&ts, &actor, &action, &detail)
		out = append(out, map[string]string{"ts": ts.Format(time.RFC3339), "actor": actor, "action": action, "detail": detail})
	}
	writeJSON(w, http.StatusOK, map[string]any{"audit": out})
}

// ---------- notifications ----------

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	rows, err := s.db.QueryContext(r.Context(), `SELECT id, ts, level, message, read FROM notifications ORDER BY id DESC LIMIT 100`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var ts time.Time
		var level, message string
		var read int
		_ = rows.Scan(&id, &ts, &level, &message, &read)
		out = append(out, map[string]any{"id": id, "ts": ts.Format(time.RFC3339), "level": level, "message": message, "read": read == 1})
	}
	writeJSON(w, http.StatusOK, map[string]any{"notifications": out})
}

func (s *Server) handleNotificationRead(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid id")
		return
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE notifications SET read=1 WHERE id=?`, id)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---------- settings ----------

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	st := map[string]string{}
	rows, err := s.db.QueryContext(r.Context(), `SELECT key, value FROM settings`)
	if err == nil {
		for rows.Next() {
			var k, v string
			_ = rows.Scan(&k, &v)
			st[k] = v
		}
		rows.Close()
	}
	hw := getOr(st, "quota_high_watermark", fmt.Sprintf("%.2f", s.cfg.QuotaHighWatermark))
	var hwFloat float64
	fmt.Sscanf(hw, "%f", &hwFloat)
	webhook := getOr(st, "notify_webhook_url", s.cfg.NotifyWebhookURL)
	writeJSON(w, http.StatusOK, map[string]any{
		"rotation_strategy":            getOr(st, "rotation_strategy", s.cfg.RotationStrategy),
		"scheduler_strategy":           getOr(st, "scheduler_strategy", s.cfg.SchedulerStrategy),
		"quota_high_watermark":         hw,
		"quota_high_watermark_percent": hwFloat * 100,
		"max_retries":                  getOr(st, "max_retries", fmt.Sprintf("%d", s.cfg.MaxRetries)),
		"notify_webhook_url":           maskURL(webhook),
		"notifications_webhook_url":    maskURL(webhook),
		"webhook_url":                  maskURL(webhook),
		"proxy_health_interval":        fmt.Sprintf("%s", s.cfg.ProxyHealthInterval),
	})
}

func maskURL(u string) string {
	if u == "" {
		return ""
	}
	if len(u) > 24 {
		return u[:12] + "…" + u[len(u)-4:]
	}
	return "set"
}

func getOr(m map[string]string, k, def string) string {
	if v, ok := m[k]; ok {
		return v
	}
	return def
}

func parseFloatStrict(s string) (float64, error) {
	var f float64
	n, err := fmt.Sscanf(strings.TrimSpace(s), "%f", &f)
	_ = n
	return f, err
}

func rawToString(v json.RawMessage) string {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return strings.TrimSpace(s)
	}
	var f float64
	if err := json.Unmarshal(v, &f); err == nil {
		if f == float64(int64(f)) {
			return fmt.Sprintf("%d", int64(f))
		}
		return fmt.Sprintf("%f", f)
	}
	var b bool
	if err := json.Unmarshal(v, &b); err == nil {
		if b {
			return "true"
		}
		return "false"
	}
	return ""
}

func (s *Server) handleUpdateSettings(w http.ResponseWriter, r *http.Request) {
	var rawReq map[string]json.RawMessage
	if err := decodeJSON(r, &rawReq); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req := map[string]string{}
	for k, v := range rawReq {
		req[k] = rawToString(v)
	}
	allowed := map[string]bool{
		"rotation_strategy": true, "scheduler_strategy": true,
		"quota_high_watermark": true, "quota_high_watermark_percent": true,
		"max_retries": true, "notify_webhook_url": true,
		"notifications_webhook_url": true, "webhook_url": true,
	}
	validStrategies := map[string]bool{"round_robin": true, "least_used": true, "most_quota": true, "weighted": true}
	// Normalize frontend aliases onto canonical keys.
	if v, ok := req["quota_high_watermark_percent"]; ok {
		if f, err := parseFloatStrict(v); err == nil {
			if f > 1 { // given as percent, e.g. 90
				f = f / 100
			}
			req["quota_high_watermark"] = fmt.Sprintf("%.2f", f)
		}
	}
	if v, ok := req["notifications_webhook_url"]; ok && v != "" {
		req["notify_webhook_url"] = v
	}
	if v, ok := req["webhook_url"]; ok && v != "" {
		req["notify_webhook_url"] = v
	}
	for k, v := range req {
		if !allowed[k] {
			continue
		}
		if k == "quota_high_watermark_percent" || k == "notifications_webhook_url" || k == "webhook_url" {
			continue // already normalized above
		}
		if (k == "rotation_strategy" || k == "scheduler_strategy") && !validStrategies[v] {
			writeError(w, http.StatusBadRequest, k+" must be one of round_robin|least_used|most_quota|weighted")
			return
		}
		_, _ = s.db.ExecContext(r.Context(), `INSERT INTO settings(key, value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, v)
		if k == "rotation_strategy" {
			s.cfg.RotationStrategy = v
		}
		if k == "scheduler_strategy" {
			s.cfg.SchedulerStrategy = v
		}
		if k == "notify_webhook_url" {
			s.cfg.NotifyWebhookURL = v
		}
		if k == "quota_high_watermark" {
			if f, err := parseFloatStrict(v); err == nil {
				s.cfg.QuotaHighWatermark = f
			}
		}
		if k == "max_retries" {
			var n int
			if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
				s.cfg.MaxRetries = n
			}
		}
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "settings_update", fmt.Sprintf("keys=%v", keysOf(req)))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func keysOf(m map[string]string) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

// handleRotateMasterKey re-encrypts every provider credential and stored proxy URL
// with a new master key. The env var must be updated and the service restarted afterwards.
func (s *Server) handleRotateMasterKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CurrentMasterKey string `json:"current_master_key"`
		NewMasterKey     string `json:"new_master_key"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if req.CurrentMasterKey != s.cfg.MasterKey {
		writeError(w, http.StatusForbidden, "current_master_key does not match the running master key")
		return
	}
	if len(req.NewMasterKey) < 16 {
		writeError(w, http.StatusBadRequest, "new_master_key must be at least 16 characters")
		return
	}
	fromBox, err := crypto.NewBox(req.CurrentMasterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	toBox, err := crypto.NewBox(req.NewMasterKey)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	rowCount := 0
	rows, err := tx.QueryContext(r.Context(), `SELECT id, credential_enc FROM provider_keys`)
	if err == nil {
		type rec struct {
			id  int64
			enc string
		}
		var recs []rec
		for rows.Next() {
			var rc rec
			_ = rows.Scan(&rc.id, &rc.enc)
			recs = append(recs, rc)
		}
		rows.Close()
		for _, rc := range recs {
			plain, err := fromBox.Decrypt(rc.enc)
			if err != nil {
				_ = rows.Close()
				writeError(w, http.StatusInternalServerError, "decrypt failed for key id="+fmt.Sprint(rc.id))
				return
			}
			newEnc, err := toBox.Encrypt(plain)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if _, err := tx.ExecContext(r.Context(), `UPDATE provider_keys SET credential_enc=? WHERE id=?`, newEnc, rc.id); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			rowCount++
		}
	}
	// Proxy URLs prefixed with "enc:"
	prows, err := tx.QueryContext(r.Context(), `SELECT id, url FROM proxies WHERE url LIKE 'enc:%'`)
	if err == nil {
		type prec struct {
			id  int64
			url string
		}
		var precs []prec
		for prows.Next() {
			var p prec
			_ = prows.Scan(&p.id, &p.url)
			precs = append(precs, p)
		}
		prows.Close()
		for _, p := range precs {
			plain, err := fromBox.Decrypt(p.url[len("enc:"):])
			if err != nil {
				writeError(w, http.StatusInternalServerError, "proxy url decrypt failed")
				return
			}
			newEnc, err := toBox.Encrypt(plain)
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if _, err := tx.ExecContext(r.Context(), `UPDATE proxies SET url=? WHERE id=?`, "enc:"+newEnc, p.id); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
	}
	if err := tx.Commit(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "master_key_rotated", fmt.Sprintf("rows_re_encrypted=%d", rowCount))
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok", "keys_re_encrypted": rowCount,
		"next_step": "Set GPUHUB_MASTER_KEY to the new value in .env (and your secret manager), then restart GPUHub.",
	})
}

//

// ---------- WebSocket job logs ----------

var upgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 8192,
	CheckOrigin:     func(r *http.Request) bool { return true }, // same-origin SPA behind same host; token-gated
}

func (s *Server) handleJobLogs(w http.ResponseWriter, r *http.Request) {
	jobID, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return // gorilla already wrote the error response
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(24 * time.Hour))
	last := ""
	// Announce the connection immediately so clients see activity even with empty logs.
	{
		var logs, status string
		err := s.db.QueryRowContext(r.Context(), `SELECT logs, status FROM jobs WHERE id=?`, jobID).Scan(&logs, &status)
		if err == nil {
			if logs != "" {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(logs))
				last = logs
			} else {
				_ = conn.WriteMessage(websocket.TextMessage, []byte("[gpuhub] connected to job "+fmt.Sprint(jobID)+" (status: "+status+"). Waiting for output...\n"))
			}
		} else {
			_ = conn.WriteMessage(websocket.TextMessage, []byte("[gpuhub] job not found\n"))
			return
		}
	}
	deadline := time.Now().Add(6 * time.Hour)
	for time.Now().Before(deadline) {
		var logs, status string
		err := s.db.QueryRowContext(r.Context(), `SELECT logs, status FROM jobs WHERE id=?`, jobID).Scan(&logs, &status)
		if err == nil && logs != last {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(logs))
			last = logs
		}
		if err == nil {
			switch status {
			case "succeeded", "failed", "cancelled", "timeout", "unschedulable":
				_ = conn.WriteMessage(websocket.TextMessage, []byte("\n[gpuhub] job finished with status: "+status))
				return
			}
		}
		// Detect client disconnect cheaply: a ping frame roundtrip would be ideal; use short read deadline trick instead.
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		time.Sleep(1 * time.Second)
	}
}
