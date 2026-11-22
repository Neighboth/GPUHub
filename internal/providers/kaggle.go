package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Kaggle Notebooks adapter — drives Kaggle's official kernel API (the same one
// `kaggle kernels push` uses). No scraping required.
//
// Credentials: {"token":"KGAT_...","username":"thesalihsimsek"} or just {"token":"KGAT_..."}.
// If username is omitted it is parsed from the pushed kernel's URL (/code/<user>/<slug>).
//
// Endpoints used (verified live 2026-11):
//
//	POST https://www.kaggle.com/api/v1/competitions/list?page=1   (credential check)
//	POST https://www.kaggle.com/api/v1/kernels/push               (submit; works with GPU=T4x2)
//	POST https://api.kaggle.com/v1/kernels.KernelsApiService/GetKernelSessionStatus   (status)
//	POST https://api.kaggle.com/v1/kernels.KernelsApiService/ListKernelSessionOutput (logs)
//	POST https://api.kaggle.com/v1/kernels.KernelsApiService/CancelKernelSession      (cancel)
//	POST https://api.kaggle.com/v1/kernels.KernelsApiService/GetAcceleratorQuotaStatistics (quota)
type kaggleAdapter struct{}

func (k *kaggleAdapter) ID() string   { return "kaggle" }
func (k *kaggleAdapter) Name() string { return "Kaggle Notebooks" }

func (k *kaggleAdapter) Capabilities() Capabilities {
	return Capabilities{
		// Ordered by preference: GPU first, then TPU, then CPU. The scheduler tries
		// GPUType + GPUTypeFallback in this order; quota is tracked per accelerator.
		GPUs: []GPU{
			gpu("T4", 16, 2), // free GPU: 30h/week
			gpu("TPU", 0, 1), // free TPU v3-8: 20h/week (VRAM 0 = N/A)
			gpu("CPU", 0, 4), // always available fallback (no accelerator quota)
		},
		MaxDurationHours: 12,
		Frameworks:       []string{"pytorch", "tensorflow", "jax"},
		Notebooks:        true, Docker: false, SSH: false,
		FreeTierNotes: "GPU T4 x2: 30h/week -> TPU v3-8: 20h/week -> CPU: unlimited. 12h session cap; phone-verified.",
	}
}

type kaggleCred struct {
	Token    string `json:"token"`
	Username string `json:"username,omitempty"`
}

func kaggleParseCred(raw string) (kaggleCred, error) {
	p, err := ParseCredential(raw)
	if err != nil {
		return kaggleCred{}, err
	}
	token := firstToken(p)
	if token == "" {
		return kaggleCred{}, fmt.Errorf("kaggle credential must include a token ({\"token\":\"KGAT_...\"})")
	}
	return kaggleCred{Token: token, Username: p.Username}, nil
}

func (k *kaggleAdapter) client(ctx context.Context, keyID int64) (*http.Client, error) {
	return proxyClient(ctx, keyID)
}

func (k *kaggleAdapter) doJSON(ctx context.Context, cred Credential, method, url string, body any, out any) (int, error) {
	c, err := kaggleParseCred(cred.Raw)
	if err != nil {
		return 0, err
	}
	hc, err := k.client(ctx, cred.KeyID)
	if err != nil {
		return 0, err
	}
	var rdr io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rdr)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "kaggle-api-client/2.0")
	req.Header.Set("Authorization", "Bearer "+c.Token)
	resp, err := hc.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return resp.StatusCode, err
	}
	if resp.StatusCode == http.StatusTooManyRequests {
		return resp.StatusCode, &HTTPError{Status: 429, Body: string(b), Err: ErrQuota}
	}
	if resp.StatusCode >= 400 {
		return resp.StatusCode, &HTTPError{Status: resp.StatusCode, Body: string(b), Err: fmt.Errorf("kaggle HTTP %d", resp.StatusCode)}
	}
	if out != nil && len(strings.TrimSpace(string(b))) > 0 {
		_ = json.Unmarshal(b, out)
	}
	return resp.StatusCode, nil
}

func (k *kaggleAdapter) ValidateKey(ctx context.Context, cred Credential, _ string) error {
	_, err := k.doJSON(ctx, cred, http.MethodGet, "https://www.kaggle.com/api/v1/competitions/list?page=1", nil, nil)
	return err
}

func (k *kaggleAdapter) ListGPUs(ctx context.Context, cred Credential, _ string) ([]GPU, error) {
	return k.Capabilities().GPUs, nil
}

// RemoteID format used on the job row: "user/slug".
func splitKaggleRef(id RemoteJobID) (user, slug string, ok bool) {
	parts := strings.Split(strings.Trim(string(id), "/"), "/")
	user = parts[0]
	if len(parts) >= 2 {
		slug = parts[1]
	}
	// Accept full URLs like https://www.kaggle.com/code/user/slug[/vN]
	if strings.HasPrefix(string(id), "http") {
		i := strings.Index(string(id), "/code/")
		if i >= 0 {
			rest := strings.TrimPrefix(string(id)[i:], "/code/")
			segs := strings.Split(rest, "/")
			if len(segs) >= 2 {
				return segs[0], segs[1], true
			}
		}
		return "", "", false
	}
	if user == "" || slug == "" {
		return "", "", false
	}
	return user, slug, true
}

func (k *kaggleAdapter) Submit(ctx context.Context, cred Credential, _ string, job JobSpec) (RemoteJobID, error) {
	// No username needed for submission: kernels/push authenticates with the Bearer token alone.
	// (Previously this called resolveUsername which pushed a throwaway probe kernel on every submit — removed.)
	title := fmt.Sprintf("gpuhub-%d", time.Now().Unix())
	// Accelerator selection follows the job's matched GPU type (GPU -> TPU -> CPU).
	// The scheduler passes the winning fallback type in GPUType after matching.
	enableGPU := job.GPUType == "" || (job.GPUType != "TPU" && job.GPUType != "CPU")
	enableTPU := job.GPUType == "TPU"
	body := map[string]any{
		"newTitle":       title,
		"text":           scriptForJob(job),
		"language":       "python",
		"kernelType":     "script",
		"isPrivate":      true,
		"enableGPU":      enableGPU,
		"enableTPU":      enableTPU,
		"enableInternet": true,
	}
	var probe struct {
		Error      string `json:"error"`
		URL        string `json:"url"`
		Ref        string `json:"ref"`
		KernelID   int64  `json:"kernelId"`
		Version    int    `json:"versionNumber"`
		HasVersion bool   `json:"hasVersionNumber"`
	}
	if _, err := k.doJSON(ctx, cred, http.MethodPost, "https://www.kaggle.com/api/v1/kernels/push", body, &probe); err != nil {
		return "", err
	}
	if probe.Error != "" {
		return "", fmt.Errorf("kaggle kernel push failed: %s", probe.Error)
	}
	u := probe.URL
	if u == "" {
		u = probe.Ref
	}
	// URL: https://www.kaggle.com/code/<user>/<slug>
	i := strings.Index(u, "/code/")
	if i < 0 {
		return "", fmt.Errorf("kaggle kernel push returned unparsable ref %q", u)
	}
	parts := strings.Split(strings.TrimPrefix(u[i:], "/code/"), "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("kaggle kernel push returned unparsable ref %q", u)
	}
	return RemoteJobID(parts[0] + "/" + parts[1]), nil
}

// scriptForJob wraps the job payload into a kaggle-runnable python script.
// GPUHub payload types:
//
//	script   → the payload IS the script
//	command  → payload is run as a shell command via subprocess
//	notebook → payload is a path or URL to an .ipynb (nbconvert --execute)
//	docker   → unsupported here (no docker on kaggle) — will error at script level
func scriptForJob(job JobSpec) string {
	switch job.Type {
	case JobScript:
		return job.Payload
	case JobCommand:
		payload, _ := json.Marshal(job.Payload)
		return fmt.Sprintf("import subprocess\nsubprocess.run(%s, shell=True, check=False)\n", payload)
	case JobNotebook:
		payload, _ := json.Marshal(job.Payload)
		return fmt.Sprintf(
			"import urllib.request, subprocess\n"+
				"nb = %s\n"+
				"if nb.startswith('http://') or nb.startswith('https://'):\n"+
				"    src = nb\n"+
				"else:\n"+
				"    src = open(nb).read() if False else nb\n"+
				"urllib.request.urlretrieve(src, '/tmp/job.ipynb') if src.startswith('http') else open('/tmp/job.ipynb','w').write(src)\n"+
				"subprocess.run(['jupyter','nbconvert','--to','notebook','--execute','--inplace','/tmp/job.ipynb'])\n", payload)
	case JobDocker:
		payload, _ := json.Marshal(job.Payload)
		return fmt.Sprintf("print('docker payloads are not supported on Kaggle; got: %s')\n", string(payload))
	default:
		return job.Payload
	}
}

// resolveUsername returns the Kaggle username for the credential, failing loudly if absent.
func (k *kaggleAdapter) resolveUsername(ctx context.Context, cred Credential) (string, error) {
	c, err := kaggleParseCred(cred.Raw)
	if err != nil {
		return "", err
	}
	if c.Username != "" {
		return c.Username, nil
	}
	// Fall back to pushing a trivial private kernel and parsing the username from the ref.
	body := map[string]any{
		"newTitle":   fmt.Sprintf("gpuhub-probe-%d", time.Now().Unix()),
		"text":       "print('gpuhub cred probe')\n",
		"language":   "python",
		"kernelType": "script",
		"isPrivate":  true,
		"enableGPU":  false,
	}
	var probe struct {
		URL string `json:"url"`
		Ref string `json:"ref"`
	}
	if _, err := k.doJSON(ctx, cred, http.MethodPost, "https://www.kaggle.com/api/v1/kernels/push", body, &probe); err != nil {
		return "", fmt.Errorf("kaggle credential needs a username or a valid token: %w", err)
	}
	u := probe.URL
	if u == "" {
		u = probe.Ref
	}
	i := strings.Index(u, "/code/")
	if i >= 0 {
		rest := strings.TrimPrefix(u[i:], "/code/")
		if j := strings.Index(rest, "/"); j > 0 {
			return rest[:j], nil
		}
	}
	return "", fmt.Errorf("could not determine kaggle username from %q — set it in the credential as {\"username\":\"...\"}", u)
}

func (k *kaggleAdapter) Status(ctx context.Context, cred Credential, _ string, id RemoteJobID) (JobStatus, error) {
	user, slug, ok := splitKaggleRef(id)
	if !ok {
		// Fall back to username resolution.
		u, err := k.resolveUsername(ctx, cred)
		if err != nil {
			return JobStatus{}, err
		}
		user = u
		slug = string(id)
	}
	var probe struct {
		Status         string `json:"status"`
		FailureMessage string `json:"failureMessage"`
	}
	_, err := k.doJSON(ctx, cred, http.MethodPost,
		"https://api.kaggle.com/v1/kernels.KernelsApiService/GetKernelSessionStatus",
		map[string]string{"userName": user, "kernelSlug": slug}, &probe)
	if err != nil {
		// Empty body => kernel never ran yet (queued).
		if e, ok := err.(*HTTPError); ok && e.Status == 404 {
			return JobStatus{State: "queued"}, nil
		}
		return JobStatus{}, err
	}
	if probe.Status == "" {
		return JobStatus{State: "queued", Raw: string(bOf(probe))}, nil
	}
	state := map[string]string{
		"STATUS_UNSPECIFIED": "queued",
		"QUEUED":             "queued",
		"STARTING":           "queued",
		"RUNNING":            "running",
		"COMPLETE":           "succeeded",
		"COMPLETED":          "succeeded",
		"ERROR":              "failed",
		"FAILED":             "failed",
		"COMPILE_ERROR":      "failed",
		"CANCELLED":          "cancelled",
		"RESTARTING":         "queued",
	}[strings.ToUpper(probe.Status)]
	if state == "" {
		state = "unknown"
	}
	msg := probe.FailureMessage
	if msg == "" {
		msg = "Kaggle kernel durumu: " + strings.ToUpper(probe.Status)
	}
	return JobStatus{State: state, Message: msg, Raw: probe.Status}, nil
}

func bOf(v any) []byte { b, _ := json.Marshal(v); return b }

func (k *kaggleAdapter) Logs(ctx context.Context, cred Credential, _ string, id RemoteJobID) (io.Reader, error) {
	user, slug, ok := splitKaggleRef(id)
	if !ok {
		u, err := k.resolveUsername(ctx, cred)
		if err != nil {
			return nil, err
		}
		user, slug = u, string(id)
	}
	var probe struct {
		Log   string `json:"log"`
		Files []any  `json:"files,omitempty"`
	}
	_, err := k.doJSON(ctx, cred, http.MethodPost,
		"https://api.kaggle.com/v1/kernels.KernelsApiService/ListKernelSessionOutput",
		map[string]string{"userName": user, "kernelSlug": slug}, &probe)
	if err != nil {
		return nil, err
	}
	// The log blob is a JSON array of {stream_name, time, data} events; decode it as-is.
	var sb strings.Builder
	var events []struct {
		Stream string  `json:"stream_name"`
		Time   float64 `json:"time"`
		Data   string  `json:"data"`
	}
	if err := json.Unmarshal([]byte(probe.Log), &events); err == nil {
		for _, ev := range events {
			sb.WriteString(ev.Data)
		}
		if sb.Len() == 0 {
		return strings.NewReader("[Kaggle VM hazırlanıyor/çalışıyor — Kaggle script kernel logları çıktı ürettikçe veya tamamlanınca görünür]"), nil
	}
	return strings.NewReader(sb.String()), nil
	}
	// Fallback: tolerate stray non-array framing.
	for _, line := range strings.Split(probe.Log, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimPrefix(line, "[")
		line = strings.TrimSuffix(line, "]")
		if line == "" {
			continue
		}
		var ev struct {
			Stream string `json:"stream_name"`
			Data   string `json:"data"`
		}
		_ = json.Unmarshal([]byte(strings.TrimPrefix(line, ",")), &ev)
		if ev.Data != "" {
			sb.WriteString(ev.Data)
			continue
		}
		sb.WriteString(line)
		sb.WriteByte('\n')
	}
	if sb.Len() == 0 {
		return strings.NewReader("[Kaggle VM hazırlanıyor/çalışıyor — Kaggle script kernel logları çıktı ürettikçe veya tamamlanınca görünür]"), nil
	}
	return strings.NewReader(sb.String()), nil
}

func (k *kaggleAdapter) Cancel(ctx context.Context, cred Credential, _ string, id RemoteJobID) error {
	user, slug, ok := splitKaggleRef(id)
	if !ok {
		u, err := k.resolveUsername(ctx, cred)
		if err != nil {
			return err
		}
		user, slug = u, string(id)
	}
	_, err := k.doJSON(ctx, cred, http.MethodPost,
		"https://api.kaggle.com/v1/kernels.KernelsApiService/CancelKernelSession",
		map[string]string{"userName": user, "kernelSlug": slug}, nil)
	return err
}

func (k *kaggleAdapter) Cost(ctx context.Context, cred Credential, _ string, id RemoteJobID) (Money, error) {
	return 0, nil
}

func (k *kaggleAdapter) Quota(ctx context.Context, cred Credential, _ string) (Quota, error) {
	var probe struct {
		QuotaRefreshTime string `json:"quotaRefreshTime"`
		GpuQuota         struct {
			TimeUsed         string `json:"timeUsed"`
			TimeReserved     string `json:"timeReserved"`
			TotalTimeAllowed string `json:"totalTimeAllowed"`
		} `json:"gpuQuota"`
		TpuQuota struct {
			TimeUsed         string `json:"timeUsed"`
			TimeReserved     string `json:"timeReserved"`
			TotalTimeAllowed string `json:"totalTimeAllowed"`
		} `json:"tpuQuota"`
	}
	_, err := k.doJSON(ctx, cred, http.MethodPost,
		"https://api.kaggle.com/v1/kernels.KernelsApiService/GetAcceleratorQuotaStatistics",
		map[string]string{}, &probe)
	if err != nil {
		return Quota{Raw: "quota introspection not available", GPUPercents: map[string]float64{"T4": 0, "TPU": 0, "CPU": 0}}, nil
	}
	pct := func(used, total string) float64 {
		u, t := parseKaggleDura(used).Seconds(), parseKaggleDura(total).Seconds()
		if t <= 0 {
			return 0
		}
		return u / t
	}
	gpuPct := pct(probe.GpuQuota.TimeUsed, probe.GpuQuota.TotalTimeAllowed)
	tpuPct := pct(probe.TpuQuota.TimeUsed, probe.TpuQuota.TotalTimeAllowed)
	q := Quota{
		DailyUsed:   int(parseKaggleDura(probe.GpuQuota.TimeUsed).Seconds()),
		DailyLimit:  int(parseKaggleDura(probe.GpuQuota.TotalTimeAllowed).Seconds()),
		Percent:     gpuPct, // primary = GPU (scheduler fallback reads GPUPercents)
		ResetsAt:    probe.QuotaRefreshTime,
		GPUPercents: map[string]float64{"T4": gpuPct, "TPU": tpuPct, "CPU": 0},
		Raw: fmt.Sprintf("GPU %s/%s (%.0f%%) | TPU %s/%s (%.0f%%) | CPU ∞; refresh %s",
			probe.GpuQuota.TimeUsed, probe.GpuQuota.TotalTimeAllowed, gpuPct*100,
			probe.TpuQuota.TimeUsed, probe.TpuQuota.TotalTimeAllowed, tpuPct*100, probe.QuotaRefreshTime),
	}
	return q, nil
}

func parseKaggleDura(s string) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	d, err := time.ParseDuration(s)
	if err == nil {
		return d
	}
	// Kaggle returns strings like "108000s", "18.754s", "1h30m0s".
	var secs float64
	var unit string
	fmt.Sscanf(s, "%f%s", &secs, &unit)
	switch unit {
	case "s":
		return time.Duration(secs * float64(time.Second))
	case "ms":
		return time.Duration(secs * float64(time.Millisecond))
	case "m":
		return time.Duration(secs * float64(time.Minute))
	case "h":
		return time.Duration(secs * float64(time.Hour))
	}
	return 0
}

func init() {
	registerBuiltin(func() []Provider { return []Provider{&kaggleAdapter{}} })
}
