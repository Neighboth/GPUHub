package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"gpuhub/internal/proxy"
)

// apiAdapter is the shared implementation for REST-ish provider APIs.
// Per-provider constructors below set base URL, auth scheme, paths and (de)serialization.
// Where a provider has no public compute API (NOTE in README + constructor comment),
// endpoints are best-effort reconstructions from public documentation / SDKs.
type apiAdapter struct {
	id       string
	name     string
	caps     Capabilities
	baseURL  string
	auth     func(p CredentialPayload) (string, string) // header name, header value
	validate string
	gpus     string
	quota    string
	submit   string
	status   func(id string) string
	logs     func(id string) string
	cancel   func(id string) string
	cost     func(id string) string

	submitBody    func(job JobSpec) any
	parseRemoteID func([]byte) (RemoteJobID, error)
	parseStatus   func([]byte) JobStatus
}

func (a *apiAdapter) ID() string   { return a.id }
func (a *apiAdapter) Name() string { return a.name }
func (a *apiAdapter) Capabilities() Capabilities {
	return a.caps
}

// WithBaseURL points the adapter at a mock server. Test-only helper.
func (a *apiAdapter) WithBaseURL(url string) *apiAdapter {
	a.baseURL = url
	return a
}

// proxyClientFactory is set by the application bootstrap (cmd/gpuhub) to the
// sticky-binding transport cache. Defaulted to direct (proxy-less) in tests via SetProxyClientFactory.
var proxyClientFactory = func(ctx context.Context, keyID int64) (*http.Client, error) {
	return &http.Client{Timeout: 60 * time.Second}, nil
}

func proxyClient(ctx context.Context, keyID int64) (*http.Client, error) {
	return proxyClientFactory(ctx, keyID)
}

// SetProxyClientFactory wires the sticky transport cache. Called once at startup.
func SetProxyClientFactory(f func(ctx context.Context, keyID int64) (*http.Client, error)) {
	proxyClientFactory = f
}

// BindProxyManager wires the proxy manager (used by cmd/gpuhub).
func BindProxyManager(m *proxy.Manager) {
	proxyClientFactory = m.ClientForKey
}

func (a *apiAdapter) client(ctx context.Context, keyID int64) (*http.Client, error) {
	return proxyClient(ctx, keyID)
}

func (a *apiAdapter) headers(ctx context.Context, cred Credential) (map[string]string, error) {
	p, err := ParseCredential(cred.Raw)
	if err != nil {
		return nil, err
	}
	k, v := a.auth(p)
	h := map[string]string{"Accept": "application/json"}
	h[k] = v
	return h, nil
}

func (a *apiAdapter) do(ctx context.Context, cred Credential, proxyURL string, method, url string, body any) ([]byte, int, error) {
	c, err := a.client(ctx, cred.KeyID)
	if err != nil {
		return nil, 0, err
	}
	h, err := a.headers(ctx, cred)
	if err != nil {
		return nil, 0, err
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		h["Content-Type"] = "application/json"
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return nil, 0, err
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return b, resp.StatusCode, &HTTPError{Status: 429, Body: string(b), Err: ErrQuota}
	}
	if resp.StatusCode >= 400 {
		return b, resp.StatusCode, &HTTPError{Status: resp.StatusCode, Body: string(b), Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	return b, resp.StatusCode, nil
}

func (a *apiAdapter) ValidateKey(ctx context.Context, cred Credential, proxyURL string) error {
	if a.validate == "" {
		// No cheap profile endpoint; treat well-formed credential as valid locally.
		_, err := ParseCredential(cred.Raw)
		return err
	}
	_, code, err := a.do(ctx, cred, proxyURL, http.MethodGet, a.baseURL+a.validate, nil)
	_ = code
	return err
}

func (a *apiAdapter) ListGPUs(ctx context.Context, cred Credential, proxyURL string) ([]GPU, error) {
	if a.gpus != "" {
		b, _, err := a.do(ctx, cred, proxyURL, http.MethodGet, a.baseURL+a.gpus, nil)
		if err == nil {
			var gpus []GPU
			if json.Unmarshal(b, &gpus) == nil && len(gpus) > 0 {
				return gpus, nil
			}
		}
		// Fall through to declared capabilities - many free tiers report no live inventory.
	}
	return a.caps.GPUs, nil
}

func (a *apiAdapter) Quota(ctx context.Context, cred Credential, proxyURL string) (Quota, error) {
	if a.quota != "" {
		b, _, err := a.do(ctx, cred, proxyURL, http.MethodGet, a.baseURL+a.quota, nil)
		if err == nil {
			var q Quota
			if json.Unmarshal(b, &q) == nil {
				if q.Percent == 0 {
					q.Percent = computeQuotaPercent(q.DailyUsed, q.DailyLimit, q.WeeklyUsed, q.WeeklyLimit)
				}
				return q, nil
			}
			return Quota{Raw: string(b)}, nil
		} else if he, ok := err.(*HTTPError); ok && he.Status == 404 {
			return Quota{Raw: "quota endpoint not exposed by provider"}, nil
		} else {
			return Quota{}, err
		}
	}
	return Quota{Raw: "quota endpoint not exposed by provider"}, nil
}

func (a *apiAdapter) Submit(ctx context.Context, cred Credential, proxyURL string, job JobSpec) (RemoteJobID, error) {
	if a.submit == "" {
		return "", fmt.Errorf("%s: job submission via API is not supported by this provider (see README NOTE); use Provider UI and report the run id manually", a.name)
	}
	body := any(nil)
	if a.submitBody != nil {
		body = a.submitBody(job)
	} else {
		body = map[string]any{
			"type": string(job.Type), "payload": job.Payload,
			"gpu_type": job.GPUType, "gpu_count": job.GPUCount, "min_vram_gb": job.MinVRAMGB,
			"max_duration_seconds": int(job.MaxDuration.Seconds()),
		}
	}
	b, _, err := a.do(ctx, cred, proxyURL, http.MethodPost, a.baseURL+a.submit, body)
	if err != nil {
		return "", err
	}
	if a.parseRemoteID != nil {
		return a.parseRemoteID(b)
	}
	return RemoteJobID(extractFirstString(b, "id", "job_id", "run_id", "runId", "name")), nil
}

func (a *apiAdapter) Status(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) (JobStatus, error) {
	if a.status == nil {
		return JobStatus{State: "unknown", Raw: "status endpoint not available"}, nil
	}
	b, _, err := a.do(ctx, cred, proxyURL, http.MethodGet, a.baseURL+a.status(string(id)), nil)
	if err != nil {
		return JobStatus{}, err
	}
	if a.parseStatus != nil {
		return a.parseStatus(b), nil
	}
	state := strings.ToLower(extractFirstString(b, "state", "status", "phase"))
	return JobStatus{State: normalizeState(state), Raw: string(b), Cost: Money(extractFirstInt64(b, "cost_usd_micros", "cost_micros", "costUsdMicros"))}, nil
}

func (a *apiAdapter) Logs(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) (io.Reader, error) {
	if a.logs == nil {
		return strings.NewReader("(provider does not expose a logs API; see README NOTE)\n"), nil
	}
	c, err := a.client(ctx, cred.KeyID)
	if err != nil {
		return nil, err
	}
	h, err := a.headers(ctx, cred)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+a.logs(string(id)), nil)
	if err != nil {
		return nil, err
	}
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, Body: "logs fetch failed", Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
	}
	return resp.Body, nil // caller closes
}

func (a *apiAdapter) Cancel(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) error {
	if a.cancel == nil {
		return fmt.Errorf("%s: cancel API not available (see README NOTE)", a.name)
	}
	_, _, err := a.do(ctx, cred, proxyURL, http.MethodPost, a.baseURL+a.cancel(string(id)), nil)
	return err
}

func (a *apiAdapter) Cost(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) (Money, error) {
	if a.cost == nil {
		st, err := a.Status(ctx, cred, proxyURL, id)
		if err != nil {
			return 0, err
		}
		return st.Cost, nil
	}
	b, _, err := a.do(ctx, cred, proxyURL, http.MethodGet, a.baseURL+a.cost(string(id)), nil)
	if err != nil {
		return 0, err
	}
	return Money(extractFirstInt64(b, "cost_usd_micros", "cost_micros", "costUsdMicros", "total_cost_micros")), nil
}

// --- shared helpers ---

type HTTPError struct {
	Status int
	Body   string
	Err    error
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("provider HTTP %d: %s", e.Status, truncate(e.Body, 200))
}
func (e *HTTPError) Unwrap() error { return e.Err }

var ErrQuota = fmt.Errorf("provider quota exhausted (429)")

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func computeQuotaPercent(du, dl, wu, wl int) float64 {
	p := 0.0
	if dl > 0 {
		p = maxF(p, float64(du)/float64(dl))
	}
	if wl > 0 {
		p = maxF(p, float64(wu)/float64(wl))
	}
	return p
}

func maxF(a, b float64) float64 {
	if a > b {
		return a
	}
	return b
}

func extractFirstString(b []byte, keys ...string) string {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := lookupPath(m, k); ok {
			switch t := v.(type) {
			case string:
				if t != "" {
					return t
				}
			case float64:
				return fmt.Sprintf("%.0f", t)
			}
		}
	}
	return ""
}

func extractFirstInt64(b []byte, keys ...string) int64 {
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return 0
	}
	for _, k := range keys {
		if v, ok := lookupPath(m, k); ok {
			switch t := v.(type) {
			case float64:
				return int64(t)
			case string:
				var i int64
				fmt.Sscan(t, &i)
				return i
			}
		}
	}
	return 0
}

// lookupPath supports dotted keys ("metadata.name") one level deep.
func lookupPath(m map[string]any, key string) (any, bool) {
	if v, ok := m[key]; ok {
		return v, true
	}
	parts := strings.SplitN(key, ".", 2)
	if len(parts) == 2 {
		if sub, ok := m[parts[0]].(map[string]any); ok {
			return sub[parts[1]], sub[parts[1]] != nil
		}
	}
	return nil, false
}

func normalizeState(s string) string {
	switch s {
	case "queued", "pending", "created", "accepted", "waiting", "provisioning":
		return "queued"
	case "running", "active", "started", "in_progress", "executing":
		return "running"
	case "succeeded", "success", "completed", "done", "finished":
		return "succeeded"
	case "failed", "error", "errored", "crashed":
		return "failed"
	case "cancelled", "canceled", "stopped", "killed", "aborted":
		return "cancelled"
	case "timeout", "timed_out", "expired":
		return "timeout"
	default:
		return "unknown"
	}
}

func bearer(p CredentialPayload) (string, string) { return "Authorization", "Bearer " + firstToken(p) }
func tokenHeader(header string) func(CredentialPayload) (string, string) {
	return func(p CredentialPayload) (string, string) { return header, firstToken(p) }
}

// kaggleAuth: KGAT_ tokens are used as Bearer, legacy kaggle.json uses Basic auth.
func kaggleAuth(p CredentialPayload) (string, string) {
	token := firstToken(p)
	if strings.HasPrefix(token, "KGAT_") {
		return "Authorization", "Bearer " + token
	}
	if p.Username != "" && p.Key != "" {
		return "Authorization", "Basic " + basicEncode(p.Username, p.Key)
	}
	return "Authorization", "Bearer " + token
}
func basicAuth(p CredentialPayload) (string, string) {
	return "Authorization", "Basic " + basicEncode(p.Username, p.Key)
}

func firstToken(p CredentialPayload) string {
	if p.Token != "" {
		return p.Token
	}
	if p.Key != "" {
		return p.Key
	}
	return ""
}

func basicEncode(u, p string) string {
	return base64.StdEncoding.EncodeToString([]byte(u + ":" + p))
}
