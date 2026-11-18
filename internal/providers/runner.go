package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// Runner is GPUHub's universal bridge for providers that have NO public job API
// (Kaggle, Colab, Studio Lab, Modal-via-modal-deploy, Lightning Studios, or your own
// server/PC/home GPU).
//
// How it works:
//  1. The user deploys the self-contained runner (examples/runner.py) INSIDE any
//     environment that gives them a GPU - e.g. a Kaggle/Colab cell, a Modal function,
//     a Lightning studio, a home lab with an RTX card, a VPS with GPU passthrough.
//  2. The runner authenticates callers with a shared token and exposes:
//       GET  /health          → {"status":"ok","gpus":[{"type":"T4","vram_gb":16}]}
//       POST /jobs            → {"id":"r-123","status":"queued"}
//       GET  /jobs/{id}       → {"id":"...","state":"running|succeeded|failed|timeout|cancelled","cost_usd_micros":0}
//       GET  /jobs/{id}/logs  → text/plain
//       POST /jobs/{id}/cancel
//  3. The user pastes the runner's public URL (ngrok/cloudflared tunnel, or LAN URL)
//     into GPUHub as this provider's credential: {"base_url":"...","token":"..."}
//
// The runner reads GPUHUB_ATTEMPT / GPUHUB_RESUME envs to support checkpoint-resume
// across 9-12h provider session limits (see examples/runner.py + README).

type runnerAdapter struct {
	id, name string
	caps     Capabilities
}

func NewRunner() *runnerAdapter {
	return &runnerAdapter{id: "runner", name: "Self-hosted Runner", caps: Capabilities{
		GPUs: []GPU{
			gpu("T4", 16, 8), gpu("L4", 24, 8), gpu("A10", 24, 8), gpu("L40S", 48, 8),
			gpu("A100", 40, 8), gpu("A100", 80, 8), gpu("H100", 80, 8),
			gpu("RTX4090", 24, 8), gpu("RTX6000Pro", 96, 8), gpu("MI300X", 192, 8), gpu("Gaudi2", 96, 8),
		},
		MaxDurationHours: 24, Frameworks: []string{"pytorch", "tensorflow", "jax", "any"},
		Notebooks: false, Docker: true, SSH: true,
		FreeTierNotes: "Any machine with a GPU where you deploy examples/runner.py - Kaggle/Colab cells, Modal deploy, home RTX server, cloud VMs",
	}}
}

func (r *runnerAdapter) ID() string                 { return r.id }
func (r *runnerAdapter) Name() string               { return r.name }
func (r *runnerAdapter) Capabilities() Capabilities { return r.caps }

type runnerCred struct {
	baseURL string
	token   string
}

func runnerParseCred(raw string) (runnerCred, error) {
	var c runnerCred
	p, err := ParseCredential(raw)
	if err != nil {
		return c, err
	}
	// Credential JSON shape: {"base_url": "...", "token": "..."}
	if p.BaseURL != "" {
		return runnerCred{baseURL: strings.TrimRight(p.BaseURL, "/"), token: p.Token}, nil
	}
	// Fallback: {"url": "...", "token": "..."} or "url token" opaque split not supported
	// Fallback: "base_url=token" not supported either; require explicit JSON.
	if u, ok := p.Extra["base_url"]; ok && u != "" {
		tok := p.Extra["token"]
		if tok == "" {
			tok = p.Token
		}
		return runnerCred{baseURL: strings.TrimRight(u, "/"), token: tok}, nil
	}
	return c, fmt.Errorf("runner credential must be JSON {\"base_url\":\"https://...\",\"token\":\"...\"} (or {\"url\":...,\"token\":...}) - got token-only credential")
}

func (r *runnerAdapter) do(ctx context.Context, cred Credential, method, path string, body any) ([]byte, error) {
	c, err := runnerParseCred(cred.Raw)
	if err != nil {
		return nil, err
	}
	hc, err := proxyClient(ctx, cred.KeyID)
	if err != nil {
		return nil, err
	}
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 401 {
		return b, &HTTPError{Status: 401, Body: string(b), Err: fmt.Errorf("runner token rejected (401)")}
	}
	if resp.StatusCode >= 400 {
		return b, &HTTPError{Status: resp.StatusCode, Body: string(b), Err: fmt.Errorf("runner HTTP %d", resp.StatusCode)}
	}
	return b, nil
}

func (r *runnerAdapter) ValidateKey(ctx context.Context, cred Credential, _ string) error {
	b, err := r.do(ctx, cred, http.MethodGet, "/health", nil)
	if err != nil {
		return err
	}
	var probe map[string]any
	if err := json.Unmarshal(b, &probe); err != nil {
		return fmt.Errorf("runner /health returned non-JSON: %s", truncate(string(b), 200))
	}
	if st, _ := probe["status"].(string); st != "ok" {
		return fmt.Errorf("runner reports status=%q (expected ok)", st)
	}
	return nil
}

func (r *runnerAdapter) ListGPUs(ctx context.Context, cred Credential, _ string) ([]GPU, error) {
	b, err := r.do(ctx, cred, http.MethodGet, "/health", nil)
	if err != nil {
		return r.caps.GPUs, nil
	}
	var probe struct {
		GPUs []struct {
			Type   string `json:"type"`
			VRAMGB int    `json:"vram_gb"`
		} `json:"gpus"`
	}
	_ = json.Unmarshal(b, &probe)
	if len(probe.GPUs) == 0 {
		return r.caps.GPUs, nil
	}
	out := make([]GPU, 0, len(probe.GPUs))
	for _, g := range probe.GPUs {
		out = append(out, GPU{Type: g.Type, VRAMGB: g.VRAMGB, Count: 1, MaxCount: 1})
	}
	return out, nil
}

func (r *runnerAdapter) Quota(ctx context.Context, cred Credential, _ string) (Quota, error) {
	// Runner is self-hosted: no provider quota to report.
	return Quota{Raw: "self-hosted runner - no provider quota (your hardware)"}, nil
}

func (r *runnerAdapter) Submit(ctx context.Context, cred Credential, _ string, job JobSpec) (RemoteJobID, error) {
	env := map[string]string{
		"GPUHUB_JOB_ID":  fmt.Sprint(ctxValueJobID(ctx)),
		"GPUHUB_ATTEMPT": fmt.Sprint(ctxValueAttempt(ctx)),
	}
	b, err := r.do(ctx, cred, http.MethodPost, "/jobs", map[string]any{
		"type": string(job.Type), "payload": job.Payload,
		"gpu_type": job.GPUType, "min_vram_gb": job.MinVRAMGB, "gpu_count": job.GPUCount,
		"max_duration_seconds": int(job.MaxDuration.Seconds()), "env": env,
	})
	if err != nil {
		return "", err
	}
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &probe); err != nil || probe.ID == "" {
		return "", fmt.Errorf("runner /jobs response missing id: %s", truncate(string(b), 200))
	}
	return RemoteJobID(probe.ID), nil
}

func (r *runnerAdapter) Status(ctx context.Context, cred Credential, _ string, id RemoteJobID) (JobStatus, error) {
	b, err := r.do(ctx, cred, http.MethodGet, "/jobs/"+string(id), nil)
	if err != nil {
		return JobStatus{}, err
	}
	var probe struct {
		State         string `json:"state"`
		Message       string `json:"message"`
		CostUSDMicros int64  `json:"cost_usd_micros"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		return JobStatus{}, fmt.Errorf("runner status non-JSON: %s", truncate(string(b), 200))
	}
	return JobStatus{State: normalizeState(probe.State), Message: probe.Message, Cost: Money(probe.CostUSDMicros), Raw: string(b)}, nil
}

func (r *runnerAdapter) Logs(ctx context.Context, cred Credential, _ string, id RemoteJobID) (io.Reader, error) {
	c, err := runnerParseCred(cred.Raw)
	if err != nil {
		return nil, err
	}
	hc, err := proxyClient(ctx, cred.KeyID)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/jobs/"+string(id)+"/logs", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, Body: "logs fetch failed", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	return resp.Body, nil
}

func (r *runnerAdapter) Cancel(ctx context.Context, cred Credential, _ string, id RemoteJobID) error {
	_, err := r.do(ctx, cred, http.MethodPost, "/jobs/"+string(id)+"/cancel", json.RawMessage(`{}`))
	return err
}

func (r *runnerAdapter) Cost(ctx context.Context, cred Credential, _ string, id RemoteJobID) (Money, error) {
	st, err := r.Status(ctx, cred, "", id)
	return st.Cost, err
}

// ctxValueJobID / ctxValueAttempt let the scheduler propagate job id & attempt count via context.
// Falls back to empty strings when unset (direct calls in tests).
type ctxKeyJobID struct{}
type ctxKeyAttempt struct{}

func WithJobContext(ctx context.Context, jobID int64, attempt int) context.Context {
	return context.WithValue(context.WithValue(ctx, ctxKeyJobID{}, jobID), ctxKeyAttempt{}, attempt)
}

func ctxValueJobID(ctx context.Context) int64 {
	v, _ := ctx.Value(ctxKeyJobID{}).(int64)
	return v
}

func ctxValueAttempt(ctx context.Context) int {
	v, _ := ctx.Value(ctxKeyAttempt{}).(int)
	return v
}

func init() {
	registerBuiltin(func() []Provider { return []Provider{NewRunner()} })
}
