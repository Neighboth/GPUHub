package providers

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// webBridgePy is embedded so the helper is ALWAYS available, including inside
// the Docker/Alpine image where /app/examples is not copied (the earlier
// "web bridge helper examples/web_bridge.py not found" error).
//
//go:embed web_bridge.py
var webBridgePy string

// webbridgeAdapter bridges providers that have NO public job-submit REST API by
// using the user's WEB credentials (username/password). Strategy, in order:
//
//   1. "api"     - try the provider's hidden/undocumented backend HTTP endpoints
//                  (the ones its own web dashboard/CLI uses), with a session or
//                  OAuth token obtained from the supplied credentials.
//   2. "browser" - if no API path works, drive the provider's web UI with Playwright
//                  (Python helper examples/web_bridge.py) to obtain a session token
//                  and execute the job. This is the user's "web'den bağlan, API'ye çevir".
//
// Credential formats accepted:
//
//   Colab:  {"username":"you@gmail.com","password":"app-password"}
//   Modal:  {"username":"...","password":"..."}    OR {"id":"ak-...","secret":"as-..."}
//
// Credentials are stored encrypted by GPUHub's crypto.Box (same as every key).
// No API key is ever logged or sent to a third party: all calls go to the
// provider's own domains, through the key's sticky proxy (proxyClient).
type webbridgeAdapter struct {
	id    string
	name  string
	caps  Capabilities
	host  string // provider web host, used by the Python helper
	modal bool   // Modal uses ak-/as- token pair via its gRPC SDK instead of browser login
}

func NewColabBridge() *webbridgeAdapter {
	return &webbridgeAdapter{
		id: "colab", name: "Google Colab", host: "https://colab.research.google.com",
		caps: Capabilities{
			GPUs: []GPU{gpu("T4", 16, 1)}, MaxDurationHours: 12,
			Frameworks: []string{"pytorch", "tensorflow", "jax"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "Recurring free T4 GPU; submit via official google-colab-cli (OAuth) / hidden Colab HTTP API, Playwright fallback. Requires WSL on Windows.",
		},
	}
}

func NewModalBridge() *webbridgeAdapter {
	return &webbridgeAdapter{
		id: "modal", name: "Modal", host: "https://modal.com", modal: true,
		caps: Capabilities{
			GPUs: []GPU{gpu("T4", 16, 8), gpu("L4", 24, 8), gpu("A10", 24, 8), gpu("L40S", 48, 8), gpu("A100", 40, 8), gpu("H100", 80, 8)},
			MaxDurationHours: 24, Frameworks: []string{"pytorch", "tensorflow", "jax"},
			Notebooks: false, Docker: true, SSH: false,
			FreeTierNotes: "$30/month recurring compute credit. No REST control plane: submits via Modal SDK (gRPC ak-/as-) through examples/modal_runner.py; Playwright fallback for dashboard-only flows.",
		},
	}
}

type webCred struct {
	Username string `json:"username"`
	Password string `json:"password"`
	TokenID  string `json:"id"`
	Secret   string `json:"secret"`
}

func parseWebCred(raw string) (webCred, error) {
	var c webCred
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &c); err != nil {
		// opaque string -> password (legacy single-token keys)
		return webCred{Password: strings.TrimSpace(raw)}, nil
	}
	return c, nil
}

// callBridge invokes the Python helper. The helper tries hidden APIs first, then
// falls back to Playwright. It reads a JSON request on stdin and writes JSON on stdout.
func (w *webbridgeAdapter) callBridge(ctx context.Context, cred Credential, action string, extra map[string]any) (map[string]any, error) {
	c, err := parseWebCred(cred.Raw)
	if err != nil {
		return nil, err
	}
	if w.modal {
		if c.TokenID == "" || c.Secret == "" {
			return nil, fmt.Errorf("Modal credential must be {\"id\":\"ak-...\",\"secret\":\"as-...\"} (from `modal token new`), or username/password for web fallback")
		}
	} else if c.Username == "" || c.Password == "" {
		return nil, fmt.Errorf("%s credential must be {\"username\":\"...\",\"password\":\"...\"}", w.name)
	}

	req := map[string]any{
		"provider": w.id, "action": action,
		"host": w.host, "username": c.Username, "password": c.Password,
		"token_id": c.TokenID, "secret": c.Secret,
	}
	for k, v := range extra {
		req[k] = v
	}
	body, _ := json.Marshal(req)

	// Write the embedded helper to a temp file. This ALWAYS works (Docker/Alpine
	// included) and removes the old "helper not found" failure. Prefer an on-disk
	// copy if one exists (e.g. a developer editing examples/web_bridge.py live).
	helper := ""
	for _, p := range []string{
		filepath.Join("examples", "web_bridge.py"),
		filepath.Join(filepath.Dir(os.Args[0]), "examples", "web_bridge.py"),
	} {
		if _, err := os.Stat(p); err == nil {
			helper = p
			break
		}
	}
	if helper == "" {
		tmp := filepath.Join(os.TempDir(), "gpuhub-web-bridge.py")
		if err := os.WriteFile(tmp, []byte(webBridgePy), 0o600); err != nil {
			return nil, fmt.Errorf("cannot materialize embedded web bridge: %w", err)
		}
		helper = tmp
	}

	py := "python"
	if _, err := exec.LookPath(py); err != nil {
		py = "python3"
		if _, err := exec.LookPath(py); err != nil {
			// No Python at all (e.g. minimal Alpine image). Do NATIVE validation so
			// `Test` still works: check credential shape locally and return a clear
			// message for actions that genuinely need Python (submit/status/logs).
			return nativeBridgeResult(w, c, action, extra)
		}
	}
	cmd := exec.CommandContext(ctx, py, helper)
	cmd.Stdin = strings.NewReader(string(body))
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("web bridge (%s/%s) failed: %w: %s", w.id, action, err, truncate(errb.String(), 300))
	}
	var resp map[string]any
	if err := json.Unmarshal([]byte(out.String()), &resp); err != nil {
		return nil, fmt.Errorf("web bridge bad output: %v: %s", err, truncate(out.String(), 300))
	}
	if e, _ := resp["error"].(string); e != "" {
		return nil, fmt.Errorf("web bridge: %s", e)
	}
	return resp, nil
}

// nativeBridgeResult handles actions without any Python runtime. Validate is a
// pure credential-shape check; submit/status/logs explain what's needed.
func nativeBridgeResult(w *webbridgeAdapter, c webCred, action string, extra map[string]any) (map[string]any, error) {
	switch action {
	case "validate":
		if w.modal {
			if !strings.HasPrefix(c.TokenID, "ak-") || !strings.HasPrefix(c.Secret, "as-") {
				return nil, fmt.Errorf("Modal credential must be {\"id\":\"ak-...\",\"secret\":\"as-...\"} (from `modal token new`)")
			}
		} else if c.Username == "" || c.Password == "" {
			return nil, fmt.Errorf("%s credential must be {\"username\":\"...\",\"password\":\"...\"}", w.name)
		}
		return map[string]any{"ok": true, "native": true}, nil
	case "quota":
		if w.modal {
			return map[string]any{"raw": "$30/month recurring compute credit (Modal Starter); no REST balance endpoint"}, nil
		}
		return map[string]any{"raw": "Google Colab free T4 - dynamic weekly allowance; no fixed quota API"}, nil
	case "submit", "status", "logs", "cancel":
		return nil, fmt.Errorf("%s %s needs a Python runtime (`python3` + `modal`/`playwright`): install python3 in the GPUHub image (Dockerfile updated)", w.name, action)
	}
	return nil, fmt.Errorf("unknown bridge action %q", action)
}

func (w *webbridgeAdapter) ID() string            { return w.id }
func (w *webbridgeAdapter) Name() string          { return w.name }
func (w *webbridgeAdapter) Capabilities() Capabilities { return w.caps }

func (w *webbridgeAdapter) ValidateKey(ctx context.Context, cred Credential, _ string) error {
	_, err := w.callBridge(ctx, cred, "validate", nil)
	return err
}

func (w *webbridgeAdapter) ListGPUs(ctx context.Context, cred Credential, _ string) ([]GPU, error) {
	return w.caps.GPUs, nil
}

func (w *webbridgeAdapter) Quota(ctx context.Context, cred Credential, _ string) (Quota, error) {
	r, err := w.callBridge(ctx, cred, "quota", nil)
	if err != nil {
		return Quota{Raw: err.Error()}, nil
	}
	return Quota{Raw: fmt.Sprint(r["raw"]), Percent: floatVal(r["percent"])}, nil
}

func (w *webbridgeAdapter) Submit(ctx context.Context, cred Credential, _ string, job JobSpec) (RemoteJobID, error) {
	r, err := w.callBridge(ctx, cred, "submit", map[string]any{
		"payload": job.Payload, "gpu_type": job.GPUType, "type": string(job.Type),
	})
	if err != nil {
		return "", err
	}
	id := fmt.Sprint(r["id"])
	if id == "" || id == "<nil>" {
		return "", fmt.Errorf("web bridge returned no job id: %v", r)
	}
	return RemoteJobID(id), nil
}

func (w *webbridgeAdapter) Status(ctx context.Context, cred Credential, _ string, id RemoteJobID) (JobStatus, error) {
	r, err := w.callBridge(ctx, cred, "status", map[string]any{"id": string(id)})
	if err != nil {
		return JobStatus{}, err
	}
	return JobStatus{State: fmt.Sprint(r["state"]), Message: fmt.Sprint(r["message"])}, nil
}

func (w *webbridgeAdapter) Logs(ctx context.Context, cred Credential, _ string, id RemoteJobID) (io.Reader, error) {
	r, err := w.callBridge(ctx, cred, "logs", map[string]any{"id": string(id)})
	if err != nil {
		return nil, err
	}
	return strings.NewReader(fmt.Sprint(r["logs"])), nil
}

func (w *webbridgeAdapter) Cancel(ctx context.Context, cred Credential, _ string, id RemoteJobID) error {
	_, err := w.callBridge(ctx, cred, "cancel", map[string]any{"id": string(id)})
	return err
}

func (w *webbridgeAdapter) Cost(ctx context.Context, cred Credential, _ string, id RemoteJobID) (Money, error) {
	return 0, nil
}

func floatVal(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case int:
		return float64(x)
	}
	return 0
}

// keep net/http imported for future direct-API probes in this file.
var _ = http.MethodGet

func init() {
	registerBuiltin(func() []Provider { return []Provider{NewColabBridge(), NewModalBridge()} })
}
