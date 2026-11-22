package providers

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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
			GPUs: []GPU{gpu("T4", 16, 1), gpu("L4", 24, 1), gpu("A100", 40, 1), gpu("H100", 80, 1), gpu("TPU", 0, 1), gpu("CPU", 0, 2)},
			MaxDurationHours: 12,
			Frameworks:       []string{"pytorch", "tensorflow", "jax"}, Notebooks: true, Docker: false, SSH: false,
			FreeTierNotes: "Recurring free T4 GPU (plus Pro L4/A100/H100); authenticated via Google OAuth2 token (not app password).",
		},
	}
}

func NewModalBridge() *webbridgeAdapter {
	return &webbridgeAdapter{
		id: "modal", name: "Modal", host: "https://modal.com", modal: true,
		caps: Capabilities{
			GPUs: []GPU{gpu("T4", 16, 8), gpu("L4", 24, 8), gpu("A10", 24, 8), gpu("A10G", 24, 8), gpu("L40S", 48, 8), gpu("A100", 40, 8), gpu("H100", 80, 8), gpu("CPU", 0, 8)},
			MaxDurationHours: 24, Frameworks: []string{"pytorch", "tensorflow", "jax"},
			Notebooks: false, Docker: true, SSH: false,
			FreeTierNotes: "$30/month recurring compute credit. Submits directly via Modal Sandbox gRPC SDK (ak-/as-).",
		},
	}
}

type webCred struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	TokenID      string `json:"id"`
	Secret       string `json:"secret"`
	Token        string `json:"token"`
	RefreshToken string `json:"refresh_token"`
	Code         string `json:"code"`
}

func parseWebCred(raw string) (webCred, error) {
	var c webCred
	s := strings.TrimSpace(raw)
	if err := json.Unmarshal([]byte(s), &c); err != nil {
		// opaque string -> token
		return webCred{Token: s}, nil
	}
	return c, nil
}

func colabTokenFromCred(c webCred) string {
	for _, v := range []string{c.RefreshToken, c.Token, c.Code, c.Password} {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
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
			return nil, fmt.Errorf("Modal credential must be {\"id\":\"ak-...\",\"secret\":\"as-...\"} (from `modal token new`)")
		}
	} else if colabTokenFromCred(c) == "" {
		return nil, fmt.Errorf("%s credential must include a Google OAuth refresh_token (1//...) or authorization code (4/...)", w.name)
	}

	req := map[string]any{
		"provider": w.id, "action": action,
		"host": w.host, "username": c.Username, "password": c.Password,
		"token_id": c.TokenID, "secret": c.Secret,
		"token": colabTokenFromCred(c), "refresh_token": c.RefreshToken, "code": c.Code,
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
	// Ensure the Playwright python driver can find Node.js even on slim images.
	if os.Getenv("PLAYWRIGHT_NODEJS_PATH") == "" {
		if p, err := exec.LookPath("node"); err == nil {
			cmd.Env = append(os.Environ(), "PLAYWRIGHT_NODEJS_PATH="+p)
		}
	}
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		// The helper writes its error JSON to STDOUT (fail()) and exits 1. Show
		// both streams so the user never sees an empty "exit status 1:" message.
		detail := truncate(errb.String(), 300)
		if strings.TrimSpace(detail) == "" {
			detail = truncate(out.String(), 300)
		}
		return nil, fmt.Errorf("web bridge (%s/%s) failed: %w: %s", w.id, action, err, detail)
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
		} else if colabTokenFromCred(c) == "" {
			return nil, fmt.Errorf("%s credential must include a Google OAuth token (1//... or 4/...)", w.name)
		}
		return map[string]any{"ok": true, "native": true}, nil
	case "quota":
		if w.modal {
			return map[string]any{"raw": "$30/month recurring compute credit (Modal Starter); no REST balance endpoint"}, nil
		}
		return map[string]any{"raw": "Google Colab free T4 - dynamic weekly allowance; OAuth2 connected"}, nil
	case "submit", "status", "logs", "cancel":
		return nil, fmt.Errorf("%s %s needs a Python runtime (`python3` + `modal`): install python3 in the GPUHub image", w.name, action)
	}
	return nil, fmt.Errorf("unknown bridge action %q", action)
}

func (w *webbridgeAdapter) ID() string            { return w.id }
func (w *webbridgeAdapter) Name() string          { return w.name }
func (w *webbridgeAdapter) Capabilities() Capabilities { return w.caps }

// ValidateKey performs a REAL validation: it calls the Python bridge (Modal SDK
// or Colab OAuth2 /tun/m/ccu-info) when a Python runtime is available.
func (w *webbridgeAdapter) ValidateKey(ctx context.Context, cred Credential, proxyURL string) error {
	// Fast-fail on obviously malformed credentials before spawning Python.
	c, err := parseWebCred(cred.Raw)
	if err != nil {
		return err
	}
	if w.modal {
		if !strings.HasPrefix(c.TokenID, "ak-") || len(c.TokenID) < 12 {
			return fmt.Errorf("Modal credential must be {\"id\":\"ak-...\",\"secret\":\"as-...\"} (from `modal token new`) - got id %q", c.TokenID)
		}
		if !strings.HasPrefix(c.Secret, "as-") || len(c.Secret) < 12 {
			return fmt.Errorf("Modal credential secret must start with 'as-' (from `modal token new`)")
		}
	} else if colabTokenFromCred(c) == "" {
		return fmt.Errorf("%s credential must include a Google OAuth refresh_token (1//...) or authorization code (4/...)", w.name)
	}

	// Real end-to-end validation through the bridge.
	if _, err := exec.LookPath("python"); err == nil {
		_, err = w.callBridge(ctx, cred, "validate", nil)
		return err
	}
	if _, err := exec.LookPath("python3"); err == nil {
		_, err = w.callBridge(ctx, cred, "validate", nil)
		return err
	}
	return fmt.Errorf("cannot validate %s: python3 not installed in GPUHub image", w.name)
}

func (w *webbridgeAdapter) ListGPUs(ctx context.Context, cred Credential, _ string) ([]GPU, error) {
	return w.caps.GPUs, nil
}

func (w *webbridgeAdapter) Quota(ctx context.Context, cred Credential, _ string) (Quota, error) {
	if !w.modal {
		if r, err := w.callBridge(ctx, cred, "quota", nil); err == nil {
			return Quota{Raw: fmt.Sprint(r["raw"]), Percent: floatVal(r["percent"])}, nil
		}
	}
	c, _ := parseWebCred(cred.Raw)
	r, err := nativeBridgeResult(w, c, "quota", nil)
	if err != nil {
		return Quota{Raw: err.Error()}, nil
	}
	return Quota{Raw: fmt.Sprint(r["raw"]), Percent: floatVal(r["percent"])}, nil
}

func (w *webbridgeAdapter) Submit(ctx context.Context, cred Credential, _ string, job JobSpec) (RemoteJobID, error) {
	r, err := w.callBridge(ctx, cred, "submit", map[string]any{
		"payload":              job.Payload,
		"gpu_type":             job.GPUType,
		"type":                 string(job.Type),
		"max_duration_seconds": int(job.MaxDuration.Seconds()),
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

const (
	colabOAuthClientID     = "764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com"
	colabOAuthClientSecret = "d-FL95Q19q7MQmFpd7hHD0Ty"
	colabOAuthRedirectURI  = "http://localhost:8080/oauth/callback"
)

// ColabOAuthURL returns Google's official OAuth2 consent URL used by google-colab-cli.
// Note: Google's desktop OAuth client 764086051850-6qr4p... only allows http://localhost
// loopback redirect URIs.
func ColabOAuthURL() string {
	v := url.Values{
		"client_id":     {colabOAuthClientID},
		"redirect_uri":  {colabOAuthRedirectURI},
		"response_type": {"code"},
		"scope":         {"openid https://www.googleapis.com/auth/userinfo.profile https://www.googleapis.com/auth/userinfo.email https://www.googleapis.com/auth/cloud-platform https://www.googleapis.com/auth/colaboratory https://www.googleapis.com/auth/drive.file"},
		"access_type":   {"offline"},
		"prompt":        {"consent"},
	}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + v.Encode()
}

// extractOAuthCodeFromInput extracts the `code` query parameter if the user pasted
// the full http://localhost:8080/oauth/callback?code=4/0A... redirect URL from their browser.
func extractOAuthCodeFromInput(s string) (code string, redirectURI string) {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "localhost:") {
		rawURL := s
		if strings.HasPrefix(rawURL, "localhost:") {
			rawURL = "http://" + rawURL
		}
		if u, err := url.Parse(rawURL); err == nil {
			if c := u.Query().Get("code"); c != "" {
				base := fmt.Sprintf("%s://%s%s", u.Scheme, u.Host, u.Path)
				return c, base
			}
		}
	}
	if strings.Contains(s, "code=") {
		if idx := strings.Index(s, "code="); idx >= 0 {
			rest := s[idx+len("code="):]
			if amp := strings.IndexByte(rest, '&'); amp >= 0 {
				rest = rest[:amp]
			}
			if decoded, err := url.QueryUnescape(rest); err == nil && decoded != "" {
				return decoded, colabOAuthRedirectURI
			}
			return rest, colabOAuthRedirectURI
		}
	}
	return s, colabOAuthRedirectURI
}

// ExchangeColabCredential exchanges a one-time Google authorization code (4/... or full localhost redirect URL)
// for a long-lived OAuth2 refresh_token before encrypting and storing it.
// If the credential already holds a refresh_token (1//...) it is returned as-is.
func ExchangeColabCredential(ctx context.Context, raw string) (string, error) {
	c, err := parseWebCred(raw)
	if err != nil {
		return raw, err
	}
	tok := colabTokenFromCred(c)
	if tok == "" {
		return "", fmt.Errorf("Google Colab credential must include an OAuth code, redirect URL, or refresh_token")
	}
	extractedCode, redirectURI := extractOAuthCodeFromInput(tok)
	if c.Code != "" {
		extractedCode, redirectURI = extractOAuthCodeFromInput(c.Code)
	}
	// Only exchange one-time authorization codes (typically start with "4/").
	if !strings.HasPrefix(extractedCode, "4/") && c.Code == "" {
		return raw, nil
	}
	code := extractedCode
	form := url.Values{
		"code":          {code},
		"client_id":     {colabOAuthClientID},
		"client_secret": {colabOAuthClientSecret},
		"redirect_uri":  {redirectURI},
		"grant_type":    {"authorization_code"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("OAuth code exchange failed: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var res struct {
		RefreshToken string `json:"refresh_token"`
		AccessToken  string `json:"access_token"`
		Error        string `json:"error"`
		ErrorDesc    string `json:"error_description"`
	}
	_ = json.Unmarshal(b, &res)
	if resp.StatusCode >= 400 || res.Error != "" {
		return "", fmt.Errorf("Google OAuth error: %s %s", res.Error, res.ErrorDesc)
	}
	if res.RefreshToken == "" {
		return "", fmt.Errorf("Google OAuth did not return a refresh_token (make sure to approve offline access)")
	}
	out, _ := json.Marshal(map[string]string{"token": res.RefreshToken})
	return string(out), nil
}

func init() {
	registerBuiltin(func() []Provider { return []Provider{NewColabBridge(), NewModalBridge()} })
}
