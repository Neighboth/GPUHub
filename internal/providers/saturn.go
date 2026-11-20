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

// Saturn Cloud adapter — drives Saturn Cloud's SuperHub API.
// Host: https://app.community.saturnenterprise.io for the free community hosted instance.
//
// Endpoints used (verified live against a real Saturn account, 2026-11):
//
//	GET  /api/workspaces              (credential check; returns {workspaces:[...]})
//	GET  /api/workspaces/info         (server options: available instance sizes incl. T4/A10G/A6000/A100/H100/B300)
//	POST /api/jobs                    (submit a Job: image + instance_size + start_script)
//	GET  /api/jobs/{id}               (status)
//	GET  /api/jobs/{id}/logs          (logs = last lines of stdout)
//	POST /api/jobs/{id}/start         (start)
//	POST /api/jobs/{id}/stop          (cancel)
//
// NOTE: free-tier 150 hours are deducted from your Saturn account on every Job run.
// GPUHub never auto-starts a GPU Job unless you explicitly call Submit with a GPU image.
type saturnAdapter struct{}

func NewSaturn() *saturnAdapter { return &saturnAdapter{} }

func (s *saturnAdapter) ID() string            { return "saturn" }
func (s *saturnAdapter) Name() string          { return "Saturn Cloud" }
func (s *saturnAdapter) Capabilities() Capabilities {
	// Prices and availability change; these are the free-tier compatible shapes
	// verified on the community hosted instance (AWS-backed).
	return Capabilities{
		GPUs: []GPU{
			gpu("T4", 16, 1),   // g4dnxlarge $0.795/h
			gpu("A10G", 24, 1), // g5xlarge $1.50/h (community A10G display)
			gpu("A6000", 48, 1),
			gpu("V100", 32, 1),
			gpu("A100", 40, 1),
		},
		MaxDurationHours: 0, // free tier hours cap by account, not by instance
		Frameworks:       []string{"pytorch", "tensorflow", "jax"},
		Notebooks:        true, Docker: true, SSH: true,
		FreeTierNotes:    "Free Hosted: 150 GPU+CPU hours/month shared pool (T4 class only); JupyterLab + Dask; region=US East 2; idle auto-shutdown",
	}
}

type saturnCred struct {
	Token   string
	BaseURL string
}

func saturnParseCred(raw string) (saturnCred, error) {
	p, err := ParseCredential(raw)
	if err != nil {
		return saturnCred{}, err
	}
	token := firstToken(p)
	if token == "" {
		return saturnCred{}, fmt.Errorf("saturn credential must include a token")
	}
	base := strings.TrimRight(firstNonEmpty(p.BaseURL, p.Extra["base_url"], p.Extra["url"]), "/")
	if base == "" {
		base = "https://app.community.saturnenterprise.io"
	}
	if o, ok := BaseURLOverride("saturn"); ok {
		base = o
	}
	return saturnCred{Token: token, BaseURL: base}, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (s *saturnAdapter) do(ctx context.Context, cred Credential, method, path string, body any) ([]byte, error) {
	c, err := saturnParseCred(cred.Raw)
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
		rdr = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, rdr)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "saturn-python/2026.6.3")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == 429 {
		return b, &HTTPError{Status: 429, Body: string(b), Err: ErrQuota}
	}
	if resp.StatusCode >= 400 {
		return b, &HTTPError{Status: resp.StatusCode, Body: string(b), Err: fmt.Errorf("saturn HTTP %d", resp.StatusCode)}
	}
	return b, nil
}

func (s *saturnAdapter) ValidateKey(ctx context.Context, cred Credential, _ string) error {
	_, err := s.do(ctx, cred, http.MethodGet, "/api/workspaces", nil)
	return err
}

func (s *saturnAdapter) ListGPUs(ctx context.Context, cred Credential, _ string) ([]GPU, error) {
	return s.Capabilities().GPUs, nil
}

func (s *saturnAdapter) Quota(ctx context.Context, cred Credential, _ string) (Quota, error) {
	// Saturn doesn't expose credit balance via this API branch. Default Hosted Free
	// accounts get 150 GPU+CPU hours/month deducted from usage; quota appears in web UI/billing.
	// GPUHub meters local usage to 90% watermark as a proxy.
	return Quota{Raw: "quota not exposed by Saturn API — see Saturn UI → Usage. 150 free GPU+CPU hours/month; GPUHub meters locally"}, nil
}

func (s *saturnAdapter) Submit(ctx context.Context, cred Credential, _ string, job JobSpec) (RemoteJobID, error) {
	credential, err := saturnParseCred(cred.Raw)
	if err != nil {
		return "", err
	}
	_ = credential
	name := fmt.Sprintf("gpuhub-job-%d", time.Now().Unix())
	image := "public.ecr.aws/saturncloud/saturn-python-pytorch:2026.05.01"
	instanceSize := "g4dnxlarge" // free-tier-recognized T4
	if job.GPUType != "" && job.GPUType != "any" {
		switch strings.ToUpper(job.GPUType) {
		case "A10", "A10G":
			instanceSize = "g5xlarge"
		case "L40S":
			instanceSize = "k0rdent/massedcompute-l40s-desmoines-usa-1"
		case "A100":
			instanceSize = "k0rdent/crusoe-a100-fairview-usa-1"
		case "V100":
			instanceSize = "k0rdent/verda-v100-helsinki-finland-1"
		case "RTX4090", "RTX6000Ada", "RTX6000Pro":
			instanceSize = "k0rdent/digitalocean-rtx6000ada-toronto-canada-1"
		}
	}
	script := scriptForSaturn(job)
	// Saturn JobCreate: `command` is the primary command (REQUIRED, non-empty);
	// `start_script` is a shell script run before it. Saturn requires `command` to be
	// a short shell command string; embedding a multi-line script there can lead to a
	// job that is created but never schedules a pod. Put the payload in start_script
	// and use a simple `bash <script-file>` style command instead.
	command := "bash /tmp/gpuhub-job.sh"
	body := map[string]any{
		"name":          name,
		"instance_size": instanceSize,
		"image_uri":     image,
		"command":       command,
		"start_script":  "#!/bin/bash\ncat > /tmp/gpuhub-job.sh <<'GPUHUB_EOF'\n" + script + "\nGPUHUB_EOF\nchmod +x /tmp/gpuhub-job.sh\n",
		"environment_variables": map[string]string{
			"GPUHUB_GPU_TYPE": job.GPUType,
			"GPUHUB_MIN_VRAM_GB": fmt.Sprint(job.MinVRAMGB),
			"GPUHUB_GPU_COUNT": fmt.Sprint(job.GPUCount),
			"GPUHUB_ATTEMPT": fmt.Sprint(ctxValueAttempt(ctx)),
			"GPUHUB_RESUME": fmt.Sprint(map[bool]string{true: "1", false: "0"}[ctxValueAttempt(ctx) > 0]),
		},
	}
	b, err := s.do(ctx, cred, http.MethodPost, "/api/jobs", body)
	if err != nil {
		return "", err
	}
	var probe struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &probe); err == nil && probe.ID != "" {
		return RemoteJobID(probe.ID), nil
	}
	// Some firmware returns {id: ...} nested
	var nested struct {
		Job struct{ ID string `json:"id"` } `json:"job"`
	}
	if err := json.Unmarshal(b, &nested); err == nil && nested.Job.ID != "" {
		return RemoteJobID(nested.Job.ID), nil
	}
	return "", fmt.Errorf("saturn job create returned unmappable body: %s", truncate(string(b), 200))
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

func scriptForSaturn(job JobSpec) string {
	switch job.Type {
	case JobScript:
		return job.Payload
	case JobCommand:
		b, _ := json.Marshal(job.Payload)
		return fmt.Sprintf("#!/bin/bash\nsh -c %s\n", b)
	case JobNotebook, JobDocker:
		b, _ := json.Marshal(job.Payload)
		return fmt.Sprintf("echo 'notebook/docker payload %s cannot run directly on Saturn without extra setup'\n", b)
	default:
		return job.Payload
	}
}

func (s *saturnAdapter) Status(ctx context.Context, cred Credential, _ string, id RemoteJobID) (JobStatus, error) {
	b, err := s.do(ctx, cred, http.MethodGet, "/api/jobs/"+string(id), nil)
	if err != nil {
		return JobStatus{}, err
	}
	var probe struct {
		Name      string `json:"name"`
		Status    string `json:"status"`
		Spec      struct {
			Status string `json:"status"`
		} `json:"spec"`
		Runtime struct {
			Status string `json:"status"`
			Message string `json:"message"`
		} `json:"runtime"`
	}
	_ = json.Unmarshal(b, &probe)
	state := strings.ToLower(firstNonEmpty(probe.Runtime.Status, probe.Spec.Status, probe.Status, "unknown"))
	switch state {
	case "created", "pending", "starting":
		state = "queued"
	case "running", "active":
		state = "running"
	case "failed", "error":
		state = "failed"
	case "stopped", "terminated":
		// Saturn uses "stopped" for BOTH a finished job and a job that never got
		// a pod (e.g. quota 0 / UPGRADE_TO_PRO). Check history: no pods => failed,
		// otherwise succeeded. This prevents fake "succeeded" jobs with no output.
		hb, herr := s.do(ctx, cred, http.MethodGet, "/api/jobs/"+string(id)+"/history", nil)
		if herr == nil {
			var hist struct {
				Pods []any `json:"pods"`
			}
			if json.Unmarshal(hb, &hist) == nil && len(hist.Pods) == 0 {
				return JobStatus{State: "failed", Message: "Saturn job durdu ama hiç pod calismadi (kota/limit?)", Raw: string(b)}, nil
			}
		}
		state = "succeeded"
	case "complete", "completed":
		state = "succeeded"
	}
	return JobStatus{State: state, Message: probe.Runtime.Message, Raw: string(b)}, nil
}

func (s *saturnAdapter) Logs(ctx context.Context, cred Credential, _ string, id RemoteJobID) (io.Reader, error) {
	b, err := s.do(ctx, cred, http.MethodGet, "/api/jobs/"+string(id)+"/logs", nil)
	if err != nil {
		return nil, err
	}
	// Saturn returns {"logs":[{...}],"prev_key":...,"next_key":...} - decode the
	// log array into plain text instead of leaking raw JSON into the job logs
	// (which broke AI Test output).
	var probe struct {
		Logs []struct {
			Data    string `json:"data"`
			Message string `json:"message"`
			Line    string `json:"line"`
			Text    string `json:"text"`
			Stream  string `json:"stream_name"`
		} `json:"logs"`
	}
	if err := json.Unmarshal(b, &probe); err == nil {
		var sb strings.Builder
		for _, l := range probe.Logs {
			v := firstNonEmpty(l.Data, l.Message, l.Line, l.Text)
			if v != "" {
				sb.WriteString(v)
			}
		}
		if sb.Len() > 0 {
			return strings.NewReader(sb.String()), nil
		}
		return strings.NewReader("[Saturn job henüz log üretmedi - pod kuyrukta/çalışıyor]"), nil
	}
	return strings.NewReader(string(b)), nil
}

func (s *saturnAdapter) Cancel(ctx context.Context, cred Credential, _ string, id RemoteJobID) error {
	_, err := s.do(ctx, cred, http.MethodPost, "/api/jobs/"+string(id)+"/stop", json.RawMessage(`{}`))
	return err
}

func (s *saturnAdapter) Cost(ctx context.Context, cred Credential, _ string, id RemoteJobID) (Money, error) {
	return 0, nil // Saturn free tier usage = time-based; cannot be priced $-wise
}
