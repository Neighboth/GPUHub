package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gpuhub/internal/audit"
	"gpuhub/internal/providers"
)

// ---------- jobs ----------

type createJobReq struct {
	Type               string   `json:"type"`
	Payload            string   `json:"payload"`
	GPUType            string   `json:"gpu_type"`
	GPUTypeFallback    []string `json:"gpu_type_fallback"`
	MinVRAMGB          int      `json:"min_vram_gb"`
	GPUCount           int      `json:"gpu_count"`
	MaxDurationSeconds int      `json:"max_duration_seconds"`
	ProviderPreference []string `json:"provider_preference"`
	MaxCostUSD         float64  `json:"max_cost_usd"`
	SessionRenew       bool     `json:"session_renew"`
}

func (s *Server) handleCreateJob(w http.ResponseWriter, r *http.Request) {
	var req createJobReq
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	// GPU fields are mandatory (spec §4).
	req.GPUType = strings.TrimSpace(req.GPUType)
	if req.GPUType == "" {
		writeError(w, http.StatusBadRequest, "gpu_type is required (e.g. \"T4\", \"A100\", \"any\")")
		return
	}
	if !providers.GPUTypeValid(req.GPUType) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown gpu_type %q - known: %s", req.GPUType, strings.Join(providers.KnownGPUTypes(), ", ")))
		return
	}
	for _, fb := range req.GPUTypeFallback {
		if !providers.GPUTypeValid(fb) {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("unknown gpu_type_fallback entry %q", fb))
			return
		}
	}
	if req.MinVRAMGB < 1 {
		writeError(w, http.StatusBadRequest, "min_vram_gb is required and must be >= 1 (e.g. 16, 24, 40, 80)")
		return
	}
	if req.GPUCount < 1 || req.GPUCount > 8 {
		writeError(w, http.StatusBadRequest, "gpu_count must be between 1 and 8")
		return
	}
	switch providers.JobType(req.Type) {
	case providers.JobScript, providers.JobNotebook, providers.JobDocker, providers.JobCommand:
	default:
		writeError(w, http.StatusBadRequest, "type must be one of script|notebook|docker|command")
		return
	}
	if strings.TrimSpace(req.Payload) == "" {
		writeError(w, http.StatusBadRequest, "payload is required (script content, notebook JSON/URL, docker image, or command)")
		return
	}
	if req.MaxDurationSeconds <= 0 {
		req.MaxDurationSeconds = 3600
	}
	if req.MaxDurationSeconds > 86400 {
		writeError(w, http.StatusBadRequest, "max_duration_seconds capped at 86400 (24h)")
		return
	}
	for _, p := range req.ProviderPreference {
		if providers.Lookup(p) == nil {
			writeError(w, http.StatusBadRequest, "unknown provider_preference entry: "+p)
			return
		}
	}
	pref, _ := json.Marshal(req.ProviderPreference)
	fallback, _ := json.Marshal(req.GPUTypeFallback)
	maxCost := int64(req.MaxCostUSD * 1_000_000)
	sessionRenew := 0
	if req.SessionRenew {
		sessionRenew = 1
	}
	res, err := s.db.ExecContext(r.Context(), `INSERT INTO jobs(type, payload, gpu_type, gpu_fallback, min_vram_gb, gpu_count, max_duration_seconds, provider_preference, max_cost_usd_micros, session_renew, status)
		VALUES(?,?,?,?,?,?,?,?,?,?,'queued')`, req.Type, req.Payload, req.GPUType, string(fallback), req.MinVRAMGB, req.GPUCount, req.MaxDurationSeconds, string(pref), maxCost, sessionRenew)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jobID, _ := res.LastInsertId()
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "job_create",
		fmt.Sprintf("job_id=%d type=%s gpu=%s min_vram_gb=%d gpu_count=%d", jobID, req.Type, req.GPUType, req.MinVRAMGB, req.GPUCount))
	if s.onJobCreated != nil {
		s.onJobCreated()
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": jobID, "status": "queued"})
}

type jobRow struct {
	ID              int64    `json:"id"`
	Type            string   `json:"type"`
	Payload         string   `json:"payload"`
	GPUType         string   `json:"gpu_type"`
	GPUTypeFallback []string `json:"gpu_type_fallback"`
	MinVRAMGB       int      `json:"min_vram_gb"`
	GPUCount        int      `json:"gpu_count"`
	MaxDuration     int      `json:"max_duration_seconds"`
	ProviderPref    []string `json:"provider_preference"`
	MaxCostMicros   int64    `json:"max_cost_usd_micros"`
	SessionRenew    bool     `json:"session_renew"`
	Status          string   `json:"status"`
	ProviderID      *string  `json:"provider_id"`
	KeyID           *int64   `json:"key_id"`
	RemoteID        *string  `json:"remote_id"`
	Attempts        int      `json:"attempts"`
	CostMicros      int64    `json:"cost_usd_micros"`
	FailReason      string   `json:"fail_reason"`
	Rejections      any      `json:"rejections"`
	Logs            string   `json:"logs,omitempty"`
	CreatedAt       string   `json:"created_at"`
	UpdatedAt       string   `json:"updated_at"`
	StartedAt       *string  `json:"started_at"`
	FinishedAt      *string  `json:"finished_at"`
}

func scanJob(row interface{ Scan(...any) error }) (jobRow, error) {
	var j jobRow
	var pref, rejections, fallback string
	var providerID, remoteID, started, finished sql.NullString
	var keyID sql.NullInt64
	var startedTime, finishedTime sql.NullTime
	var created, updated time.Time
	var renew int
	err := row.Scan(&j.ID, &j.Type, &j.Payload, &j.GPUType, &fallback, &j.MinVRAMGB, &j.GPUCount, &j.MaxDuration,
		&pref, &j.MaxCostMicros, &renew, &j.Status, &providerID, &keyID, &remoteID, &j.Attempts,
		&j.CostMicros, &j.FailReason, &rejections, &j.Logs, &created, &updated, &startedTime, &finishedTime)
	if err != nil {
		return j, err
	}
	j.SessionRenew = renew == 1
	_ = json.Unmarshal([]byte(pref), &j.ProviderPref)
	_ = json.Unmarshal([]byte(fallback), &j.GPUTypeFallback)
	_ = json.Unmarshal([]byte(rejections), &j.Rejections)
	if providerID.Valid {
		j.ProviderID = &providerID.String
	}
	if keyID.Valid {
		id := keyID.Int64
		j.KeyID = &id
	}
	if remoteID.Valid {
		j.RemoteID = &remoteID.String
	}
	j.CreatedAt = created.Format(time.RFC3339)
	j.UpdatedAt = updated.Format(time.RFC3339)
	if startedTime.Valid {
		s := startedTime.Time.Format(time.RFC3339)
		_ = started
		j.StartedAt = &s
	}
	if finishedTime.Valid {
		s := finishedTime.Time.Format(time.RFC3339)
		_ = finished
		j.FinishedAt = &s
	}
	return j, nil
}

const jobSelect = `SELECT id,type,payload,gpu_type,gpu_fallback,min_vram_gb,gpu_count,max_duration_seconds,
provider_preference,max_cost_usd_micros,session_renew,status,provider_id,key_id,remote_id,attempts,
cost_usd_micros,fail_reason,rejections,logs,created_at,updated_at,started_at,finished_at FROM jobs`

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := jobSelect
	args := []any{}
	if status != "" {
		q += ` WHERE status=?`
		args = append(args, status)
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.QueryContext(r.Context(), q, args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	var out []jobRow
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			continue
		}
		j.Logs = "" // omitted from list responses
		out = append(out, j)
	}
	if out == nil {
		out = []jobRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	j, err := scanJob(s.db.QueryRowContext(r.Context(), jobSelect+` WHERE id=?`, jobID))
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	// Attach attempts (failover history) and cost.
	attempts, _ := s.db.QueryContext(r.Context(), `SELECT a.id,a.provider_id,a.key_id,k.label,a.proxy_id,COALESCE(p.label,''),a.remote_id,a.status,a.error,a.started_at,a.finished_at
		FROM job_attempts a LEFT JOIN provider_keys k ON k.id=a.key_id LEFT JOIN proxies p ON p.id=a.proxy_id
		WHERE a.job_id=? ORDER BY a.id`, jobID)
	type attempt struct {
		ID         int64   `json:"id"`
		ProviderID string  `json:"provider_id"`
		KeyID      *int64  `json:"key_id"`
		KeyLabel   string  `json:"key_label"`
		ProxyID    *int64  `json:"proxy_id"`
		ProxyLabel string  `json:"proxy_label"`
		RemoteID   string  `json:"remote_id"`
		Status     string  `json:"status"`
		Error      string  `json:"error"`
		StartedAt  string  `json:"started_at"`
		FinishedAt *string `json:"finished_at"`
	}
	var atts []attempt
	for attempts.Next() {
		var a attempt
		var keyID, proxyID sql.NullInt64
		var started time.Time
		var finished sql.NullTime
		_ = attempts.Scan(&a.ID, &a.ProviderID, &keyID, &a.KeyLabel, &proxyID, &a.ProxyLabel, &a.RemoteID, &a.Status, &a.Error, &started, &finished)
		if keyID.Valid {
			id := keyID.Int64
			a.KeyID = &id
		}
		if proxyID.Valid {
			id := proxyID.Int64
			a.ProxyID = &id
		}
		a.StartedAt = started.Format(time.RFC3339)
		if finished.Valid {
			s := finished.Time.Format(time.RFC3339)
			a.FinishedAt = &s
		}
		atts = append(atts, a)
	}
	attempts.Close()
	if atts == nil {
		atts = []attempt{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"job": j, "attempts": atts})
}

func (s *Server) handleCancelJob(w http.ResponseWriter, r *http.Request) {
	jobID, err := parseID(chi.URLParam(r, "id"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid job id")
		return
	}
	var status string
	if err := s.db.QueryRowContext(r.Context(), `SELECT status FROM jobs WHERE id=?`, jobID).Scan(&status); err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}
	if status == "succeeded" || status == "failed" || status == "cancelled" || status == "unschedulable" || status == "timeout" {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "job already in terminal state: " + status})
		return
	}
	// Best-effort remote cancel.
	j, err := scanJob(s.db.QueryRowContext(r.Context(), jobSelect+` WHERE id=?`, jobID))
	if err == nil && j.ProviderID != nil && j.RemoteID != nil && j.KeyID != nil {
		if p := providers.Lookup(*j.ProviderID); p != nil {
			var enc string
			if err := s.db.QueryRowContext(r.Context(), `SELECT credential_enc FROM provider_keys WHERE id=?`, *j.KeyID).Scan(&enc); err == nil {
				if raw, err := s.box.Decrypt(enc); err == nil {
					ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
					_ = p.Cancel(ctx, providers.Credential{KeyID: *j.KeyID, Raw: raw}, "", providers.RemoteJobID(*j.RemoteID))
					cancel()
				}
			}
		}
	}
	_, _ = s.db.ExecContext(r.Context(), `UPDATE jobs SET status='cancelled', finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=?`, jobID)
	_, _ = s.db.ExecContext(r.Context(), `UPDATE job_attempts SET status='cancelled', finished_at=CURRENT_TIMESTAMP WHERE job_id=? AND status='running'`, jobID)
	audit.Log(r.Context(), s.db, userFrom(r.Context()).Username, "job_cancel", fmt.Sprintf("job_id=%d", jobID))
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}
