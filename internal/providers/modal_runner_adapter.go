package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Modal has NO public job-submit REST API. The only ways to run compute on Modal are:
//   a) Modal Python SDK (modal.App + modal.Function + .spawn() / .map())
//   b) A web endpoint deployed in your Modal account (e.g. examples/modal_runner.py) — then
//      GPUHub's Self-hosted Runner provider drives it over HTTP like any runner.
// This adapter therefore acts as a "deploy / runner pointer" — it validates the token
// shape and returns a clear, actionable error for job submission attempts.
//
// Credential:
//   {"id":"ak-...","secret":"as-..."}    # both from `modal token new`
//   {"token_id":"ak-...","token_secret":"as-..."}
//
// The user's earlier "ak-0Sauk7Pw6HVqHSesakRWbn" alone is NOT enough to drive Modal —
// it's only the token ID; the token SECRET is missing. UI shows both fields.

type modalAdapter struct{}

func NewModalAdapter() *modalAdapter { return &modalAdapter{} }

func (m *modalAdapter) ID() string   { return "modal" }
func (m *modalAdapter) Name() string { return "Modal" }

func (m *modalAdapter) Capabilities() Capabilities {
	return Capabilities{
		GPUs: []GPU{
			gpu("T4", 16, 8), gpu("L4", 24, 8), gpu("A10", 24, 8), gpu("L40S", 48, 8),
			gpu("A100", 40, 8), gpu("A100", 80, 8), gpu("RTX6000Pro", 96, 8),
			gpu("H100", 80, 8), gpu("H200", 141, 8), gpu("B200", 180, 8), gpu("B300", 288, 8),
		},
		MaxDurationHours: 24,
		Frameworks:       []string{"pytorch", "tensorflow", "jax"},
		Notebooks:        false, Docker: true, SSH: false,
		FreeTierNotes:    "$30/mo free compute credits (Starter). 11 GPU SKUs T4→B300. NO job REST API — deploy examples/modal_runner.py and use Self-hosted Runner.",
	}
}

type modalCred struct {
	TokenID     string
	TokenSecret string
}

// modalParseCred reads the raw credential JSON directly (ParseCredential's CredentialPayload
// silently drops unknown top-level fields, so we parse here). Accepts:
//   {"id":"ak-...","secret":"as-..."}
//   {"token_id":"ak-...","token_secret":"as-..."}
//   {"extra":{"id":"ak-...","secret":"as-..."}}
//   {"token":"ak-...","token_secret":"as-..."}  (id in token, secret in token_secret)
func modalParseCred(raw string) (modalCred, error) {
	var m modalCred
	s := strings.TrimSpace(raw)
	if !strings.HasPrefix(s, "{") {
		// Opaque string: treat as token id only (secret unknown).
		return modalCred{TokenID: s}, nil
	}
	var top map[string]any
	if err := json.Unmarshal([]byte(s), &top); err != nil {
		return modalCred{}, fmt.Errorf("credential JSON invalid: %w", err)
	}
	str := func(v any) string {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
		return ""
	}
	extra, _ := top["extra"].(map[string]any)
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v := str(top[k]); v != "" {
				return v
			}
			if extra != nil {
				if v := str(extra[k]); v != "" {
					return v
				}
			}
		}
		return ""
	}
	m.TokenID = pick("id", "token_id", "token")
	m.TokenSecret = pick("secret", "token_secret", "key")
	return m, nil
}

func (m *modalAdapter) ValidateKey(ctx context.Context, cred Credential, _ string) error {
	c, err := modalParseCred(cred.Raw)
	if err != nil {
		return err
	}
	if c.TokenID == "" || !strings.HasPrefix(c.TokenID, "ak-") {
		return errors.New("Modal token_id missing/invalid — must look like 'ak-…'. Run `modal token new` to get the pair")
	}
	if c.TokenSecret == "" {
		return errors.New("Modal token_secret missing — paste BOTH tokens: {\"id\":\"ak-…\",\"secret\":\"as-…\"}. Run `modal token new` to see both")
	}
	if !strings.HasPrefix(c.TokenSecret, "as-") {
		return errors.New("Modal token_secret must look like 'as-…'. Run `modal token new` to get the pair")
	}
	// No HTTP path exists to validate against modal.com without the SDK. We accept credentials that pass shape validation + note the live capability test happens at first job.
	_ = ctx
	return nil
}

func (m *modalAdapter) ListGPUs(ctx context.Context, cred Credential, _ string) ([]GPU, error) {
	return m.Capabilities().GPUs, nil
}

func (m *modalAdapter) Quota(ctx context.Context, cred Credential, _ string) (Quota, error) {
	return Quota{Raw: "Modal $30/mo free compute credits; no REST endpoint to introspect balance. Watch https://modal.com/usage"}, nil
}

func (m *modalAdapter) Submit(ctx context.Context, cred Credential, _ string, job JobSpec) (RemoteJobID, error) {
	return "", fmt.Errorf(`Modal has no public job-submit API in 2026-11. Deploy examples/modal_runner.py to your Modal account and connect GPUHub's Self-hosted Runner to the printed URL instead — your $30/mo free credits will fund actual runner jobs. Recommended GPU: T4 (~$0.59/hr; ~50 hours of free compute).`)
}

func (m *modalAdapter) Status(ctx context.Context, cred Credential, _ string, id RemoteJobID) (JobStatus, error) {
	return JobStatus{State: "unknown"}, nil
}

func (m *modalAdapter) Logs(ctx context.Context, cred Credential, _ string, id RemoteJobID) (io.Reader, error) {
	return strings.NewReader("Modal has no public log API — deploy examples/modal_runner.py and use Self-hosted Runner to get live logs.\n"), nil
}

func (m *modalAdapter) Cancel(ctx context.Context, cred Credential, _ string, id RemoteJobID) error {
	return fmt.Errorf("Modal has no public cancel API — use Self-hosted Runner against your deployed modal_runner.py instance")
}

func (m *modalAdapter) Cost(ctx context.Context, cred Credential, _ string, id RemoteJobID) (Money, error) {
	return 0, nil
}


func init() {
	registerBuiltin(func() []Provider { return []Provider{NewModalAdapter()} })
}
