package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testCred() Credential {
	return Credential{KeyID: 999, Raw: `{"token":"test-token-123"}`}
}

func TestParseCredential_JSON(t *testing.T) {
	p, err := ParseCredential(`{"token":"abc"}`)
	if err != nil {
		t.Fatal(err)
	}
	if p.Token != "abc" {
		t.Fatalf("got %q", p.Token)
	}
}

func TestParseCredential_OpaqueToken(t *testing.T) {
	p, err := ParseCredential("  opaque-key  ")
	if err != nil {
		t.Fatal(err)
	}
	if p.Token != "opaque-key" {
		t.Fatalf("got %q", p.Token)
	}
}

func TestValidateKey_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/users/me" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
			t.Errorf("missing bearer auth, got %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"id":"u1"}`))
	}))
	defer srv.Close()
	OverrideBaseURL("lightning", srv.URL)
	p := NewLightning().WithBaseURL(srv.URL)
	if p.baseURL != srv.URL {
		t.Fatalf("override not applied: %s", p.baseURL)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.ValidateKey(ctx, testCred(), ""); err != nil {
		t.Fatal(err)
	}
}

func TestValidateKey_401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	OverrideBaseURL("beam", srv.URL)
	p := NewBeam().WithBaseURL(srv.URL)
	ctx := context.Background()
	err := p.ValidateKey(ctx, testCred(), "")
	if err == nil {
		t.Fatal("expected error")
	}
	if e, ok := err.(*HTTPError); !ok || e.Status != 401 {
		t.Fatalf("expected HTTPError 401, got %#v", err)
	}
}

func TestSubmit_ParsesRemoteID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/jobs" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["gpu_type"] != "A100" {
			t.Errorf("gpu_type not propagated: %v", body["gpu_type"])
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"job-abc-123","status":"queued"}`))
	}))
	defer srv.Close()
	OverrideBaseURL("paperspace", srv.URL)
	p := NewPaperspace().WithBaseURL(srv.URL)
	ctx := context.Background()
	id, err := p.Submit(ctx, testCred(), "", JobSpec{Type: JobScript, Payload: "print(1)", GPUType: "A100", MinVRAMGB: 40, GPUCount: 1, MaxDuration: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if id != "job-abc-123" {
		t.Fatalf("remote id = %q", id)
	}
}

func TestSubmit_Quota429(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer srv.Close()
	OverrideBaseURL("paperspace", srv.URL)
	p := NewPaperspace().WithBaseURL(srv.URL)
	ctx := context.Background()
	_, err := p.Submit(ctx, testCred(), "", JobSpec{Type: JobScript, Payload: "x", GPUType: "T4", MinVRAMGB: 16, GPUCount: 1})
	if err == nil {
		t.Fatal("expected error")
	}
	he, ok := err.(*HTTPError)
	if !ok {
		t.Fatalf("expected *HTTPError, got %T", err)
	}
	if he.Status != 429 || he.Err != ErrQuota {
		t.Fatalf("expected 429 quota error, got %+v", he)
	}
}

func TestStatus_NormalizesState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"COMPLETED","cost_usd_micros":1500000}`))
	}))
	defer srv.Close()
	OverrideBaseURL("paperspace", srv.URL)
	p := NewPaperspace().WithBaseURL(srv.URL)
	st, err := p.Status(context.Background(), testCred(), "", "job-abc-123")
	if err != nil {
		t.Fatal(err)
	}
	if st.State != "succeeded" {
		t.Fatalf("state = %q", st.State)
	}
	if st.Cost != 1_500_000 {
		t.Fatalf("cost = %d", st.Cost)
	}
}

func TestLogs_ReturnsBody(t *testing.T) {
	content := "epoch 1 loss=0.5\nepoch 2 loss=0.4\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(content))
	}))
	defer srv.Close()
	OverrideBaseURL("paperspace", srv.URL)
	p := NewPaperspace().WithBaseURL(srv.URL)
	rc, err := p.Logs(context.Background(), testCred(), "", "job-abc-123")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.(io.ReadCloser).Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != content {
		t.Fatalf("logs = %q", b)
	}
}

func TestQuota_NotExposed_ReturnsEmpty(t *testing.T) {
	p := Lookup("kaggle") // kaggle's Quota() does hit the introspect endpoint; not exposed statically, returns zeroed Quota or falls back
	_ = p
	// A provider with no quota endpoint configured must return a well-formed zeroed Quota, no error.
	p2 := &apiAdapter{id: "noquota", name: "NoQuota", caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 1)}},
		baseURL: "http://127.0.0.1:0", auth: bearer}
	q, err := p2.Quota(context.Background(), testCred(), "")
	if err != nil {
		t.Fatal(err)
	}
	if q.Percent != 0 {
		t.Fatalf("percent = %f", q.Percent)
	}
}

func TestQuota_FromEndpoint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"daily_used":45,"daily_limit":50,"percent":0.9}`))
	}))
	defer srv.Close()
	a := &apiAdapter{id: "mockq", name: "MockQ", caps: Capabilities{GPUs: []GPU{gpu("T4", 16, 1)}},
		baseURL: srv.URL, auth: bearer, quota: "/usage"}
	q, err := a.Quota(context.Background(), testCred(), "")
	if err != nil {
		t.Fatal(err)
	}
	if q.Percent < 0.89 || q.Percent > 0.91 {
		t.Fatalf("percent = %f", q.Percent)
	}
}

func TestAll_ProvidersRegistered(t *testing.T) {
	all := All()
	if len(all) < 32 {
		t.Fatalf("expected >=32 adapters, got %d", len(all))
	}
	seen := map[string]bool{}
	for _, p := range all {
		if seen[p.ID()] {
			t.Fatalf("duplicate adapter id %s", p.ID())
		}
		seen[p.ID()] = true
		caps := p.Capabilities()
		if len(caps.GPUs) == 0 {
			t.Errorf("%s: no GPUs declared", p.ID())
		}
		for _, g := range caps.GPUs {
			if _, ok := GPUCatalog[g.Type]; !ok {
				t.Errorf("%s: unknown GPU type %q", p.ID(), g.Type)
			}
		}
	}
}

func TestNormalizeState(t *testing.T) {
	for in, want := range map[string]string{
		"COMPLETED": "succeeded", "done": "succeeded", "RUNNING": "running",
		"PENDING": "queued", "ERROR": "failed", "killed": "cancelled", "timed_out": "timeout", "mystery": "unknown",
	} {
		if got := normalizeState(strings.ToLower(in)); got != want {
			t.Errorf("normalizeState(%q) = %q, want %q", in, got, want)
		}
	}
}
