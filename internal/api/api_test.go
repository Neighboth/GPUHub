package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gpuhub/internal/auth"
	"gpuhub/internal/config"
	"gpuhub/internal/crypto"
	"gpuhub/internal/db"
	"gpuhub/internal/proxy"
)

const testMasterKey = "test-master-key-0123456789abcdef"

func setupServer(t *testing.T) (*httptest.Server, *proxy.Manager) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	box, err := crypto.NewBox(testMasterKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		SchedulerStrategy: "most_quota", RotationStrategy: "most_quota",
		AdminUsername: "admin", AdminPassword: "super-secret-password",
		QuotaHighWatermark: 0.9, MaxRetries: 3,
	}
	proxyMgr := proxy.NewManager(conn)
	proxyMgr.Decrypt = box.Decrypt
	srv := NewServer(cfg, conn, box, auth.NewService("jwt-test-secret"), proxyMgr, nil)
	ctx := context.Background()
	srv.SeedProviders(ctx)
	if _, err := srv.BootstrapAdmin(ctx); err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, proxyMgr
}

func apiReq(t *testing.T, method, url, token string, body any) (*http.Response, []byte) {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp, b
}

func login(t *testing.T, baseURL string) string {
	t.Helper()
	resp, b := apiReq(t, http.MethodPost, baseURL+"/api/v1/auth/login", "", map[string]string{
		"username": "admin", "password": "super-secret-password",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: %d %s", resp.StatusCode, b)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out.AccessToken
}

func TestCSRFHeaderRequired(t *testing.T) {
	ts, _ := setupServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/v1/auth/login", bytes.NewReader([]byte(`{"username":"admin","password":"super-secret-password"}`)))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 without X-Requested-With, got %d", resp.StatusCode)
	}
}

// Full sticky-binding flow over HTTP: create two proxies, one key bound to proxy A,
// delete proxy A, verify the key is disabled with NO binding to proxy B.
func TestProxyDelete_DisablesKeyNoRebind(t *testing.T) {
	ts, _ := setupServer(t)
	token := login(t, ts.URL)

	// Create two proxies.
	_, b := apiReq(t, http.MethodPost, ts.URL+"/api/v1/proxies", token, map[string]string{"label": "proxy-a", "url": "http://10.1.1.1:3128", "country": "US"})
	var proxyA struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(b, &proxyA); err != nil {
		t.Fatal(err)
	}
	_, b = apiReq(t, http.MethodPost, ts.URL+"/api/v1/proxies", token, map[string]string{"label": "proxy-b", "url": "http://10.2.2.2:3128", "country": "DE"})
	var proxyB struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(b, &proxyB); err != nil {
		t.Fatal(err)
	}

	// Create a key bound to proxy A.
	resp, b := apiReq(t, http.MethodPost, ts.URL+"/api/v1/providers/kaggle/keys", token, map[string]any{
		"label": "kaggle-main", "credential": `{"username":"u","key":"k"}`, "bound_proxy_id": proxyA.ID,
		"daily_quota": 5, "priority": 10,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create key: %d %s", resp.StatusCode, b)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	_ = json.Unmarshal(b, &created)

	// A second key cannot steal proxy A.
	resp, b = apiReq(t, http.MethodPost, ts.URL+"/api/v1/providers/kaggle/keys", token, map[string]any{
		"label": "kaggle-second", "credential": `{"username":"u2","key":"k2"}`, "bound_proxy_id": proxyA.ID,
	})
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("expected 409 for proxy reuse, got %d %s", resp.StatusCode, b)
	}

	// Delete proxy A.
	resp, b = apiReq(t, http.MethodDelete, ts.URL+"/api/v1/proxies/"+itoa(proxyA.ID), token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("delete proxy: %d %s", resp.StatusCode, b)
	}
	var delOut struct {
		DisabledKeys [][]string `json:"disabled_keys"`
	}
	_ = json.Unmarshal(b, &delOut)
	if len(delOut.DisabledKeys) != 1 {
		t.Fatalf("expected 1 disabled key, got %v", delOut)
	}

	// Verify key state: disabled, no binding (NOT re-bound to proxy B).
	_, b = apiReq(t, http.MethodGet, ts.URL+"/api/v1/keys", token, nil)
	var keysOut struct {
		Keys []struct {
			ID           int64  `json:"id"`
			Active       bool   `json:"active"`
			BoundProxyID *int64 `json:"bound_proxy_id"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(b, &keysOut); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, k := range keysOut.Keys {
		if k.ID == created.ID {
			found = true
			if k.Active {
				t.Fatal("key should be disabled after proxy deletion")
			}
			if k.BoundProxyID != nil {
				t.Fatal("key must NOT be rebound to another proxy automatically")
			}
		}
	}
	if !found {
		t.Fatal("key not found in list")
	}
}

// GPU fields are mandatory on job submission.
func TestCreateJob_RequiresGPUFields(t *testing.T) {
	ts, _ := setupServer(t)
	token := login(t, ts.URL)
	cases := []map[string]any{
		{"type": "script", "payload": "print(1)", "min_vram_gb": 16, "gpu_count": 1}, // missing gpu_type
		{"type": "script", "payload": "print(1)", "gpu_type": "T4", "gpu_count": 1},  // missing min_vram_gb
		{"type": "script", "payload": "print(1)", "gpu_type": "T4", "min_vram_gb": 0, "gpu_count": 1},
		{"type": "script", "payload": "print(1)", "gpu_type": "NOPE", "min_vram_gb": 16, "gpu_count": 1},
		{"type": "script", "payload": "print(1)", "gpu_type": "T4", "min_vram_gb": 16, "gpu_count": 0},
	}
	for i, body := range cases {
		resp, b := apiReq(t, http.MethodPost, ts.URL+"/api/v1/jobs", token, body)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("case %d: expected 400, got %d %s", i, resp.StatusCode, b)
		}
	}
	resp, b := apiReq(t, http.MethodPost, ts.URL+"/api/v1/jobs", token, map[string]any{
		"type": "script", "payload": "print(1)", "gpu_type": "T4", "min_vram_gb": 16, "gpu_count": 1, "max_duration_seconds": 600,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("valid job rejected: %d %s", resp.StatusCode, b)
	}
}

func itoa(v int64) string {
	b, _ := json.Marshal(v)
	s := string(b)
	return s
}
