package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gpuhub/internal/config"
	"gpuhub/internal/crypto"
	"gpuhub/internal/db"
	"gpuhub/internal/providers"
)

const testMasterKey = "test-master-key-0123456789abcdef"

func setupTestDB(t *testing.T) (*sql.DB, *crypto.Box, *config.Config) {
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
	cfg := &config.Config{MaxRetries: 3, QuotaHighWatermark: 0.9, RotationStrategy: "round_robin", PollInterval: time.Second, SchedulerInterval: time.Second}
	return conn, box, cfg
}

func insertProxy(t *testing.T, conn *sql.DB, label, rawURL string, box *crypto.Box) int64 {
	t.Helper()
	enc, err := box.Encrypt(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := conn.ExecContext(context.Background(), `INSERT INTO proxies(label, url, country) VALUES(?,?,'US')`, label, "enc:"+enc)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertKey(t *testing.T, conn *sql.DB, providerID, label, credential string, proxyID int64, box *crypto.Box, priority int) int64 {
	t.Helper()
	enc, err := box.Encrypt(credential)
	if err != nil {
		t.Fatal(err)
	}
	res, err := conn.ExecContext(context.Background(), `INSERT INTO provider_keys(provider_id,label,credential_enc,bound_proxy_id,active,priority,weight) VALUES(?,?,?,?,1,?,1)`,
		providerID, label, enc, proxyID, priority)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertProvider(t *testing.T, conn *sql.DB, id string, enabled bool) {
	t.Helper()
	e := 0
	if enabled {
		e = 1
	}
	_, err := conn.ExecContext(context.Background(), `INSERT INTO providers(id,name,enabled) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled`, id, id, e)
	if err != nil {
		t.Fatal(err)
	}
}

func insertJob(t *testing.T, conn *sql.DB, gpuType string, minVRAM, gpuCount int, preference []string) int64 {
	t.Helper()
	pref, _ := json.Marshal(preference)
	res, err := conn.ExecContext(context.Background(), `INSERT INTO jobs(type,payload,gpu_type,min_vram_gb,gpu_count,max_duration_seconds,provider_preference,status) VALUES('script','print(1)',?,?,?,3600,?,'queued')`,
		gpuType, minVRAM, gpuCount, string(pref))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

func keyStatus(t *testing.T, conn *sql.DB, keyID int64) (active bool, boundProxyID *int64) {
	t.Helper()
	var a int
	var bp sql.NullInt64
	if err := conn.QueryRow(`SELECT active, bound_proxy_id FROM provider_keys WHERE id=?`, keyID).Scan(&a, &bp); err != nil {
		t.Fatal(err)
	}
	if bp.Valid {
		return a == 1, &bp.Int64
	}
	return a == 1, nil
}

func TestGPUMatch_SpecificType(t *testing.T) {
	t4Caps := providers.Capabilities{GPUs: []providers.GPU{{Type: "T4", VRAMGB: 16, Count: 1, MaxCount: 1}}}
	spec := providers.JobSpec{GPUType: "A100", MinVRAMGB: 40, GPUCount: 1}
	if _, ok := matchProviderGPUs(t4Caps, spec); ok {
		t.Fatal("T4 provider must not match an A100 request")
	}
	a100Caps := providers.Capabilities{GPUs: []providers.GPU{{Type: "A100", VRAMGB: 80, Count: 1, MaxCount: 1}}}
	if _, ok := matchProviderGPUs(a100Caps, providers.JobSpec{GPUType: "A100", MinVRAMGB: 80, GPUCount: 1}); !ok {
		t.Fatal("A100 provider must match")
	}
	// VRAM insufficient
	if _, ok := matchProviderGPUs(t4Caps, providers.JobSpec{GPUType: "any", MinVRAMGB: 24, GPUCount: 1}); ok {
		t.Fatal("T4(16GB) must not match min_vram_gb=24")
	}
	// Count insufficient
	if _, ok := matchProviderGPUs(t4Caps, providers.JobSpec{GPUType: "any", MinVRAMGB: 16, GPUCount: 2}); ok {
		t.Fatal("max_count=1 must not match gpu_count=2")
	}
	// any matches
	g, ok := matchProviderGPUs(a100Caps, providers.JobSpec{GPUType: "any", MinVRAMGB: 40, GPUCount: 1})
	if !ok || g.Type != "A100" {
		t.Fatalf("any-match got %+v ok=%v", g, ok)
	}
}

func TestFailover_ModalUnsupportedToPaperspace(t *testing.T) {
	conn, box, cfg := setupTestDB(t)

	paperspaceMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/jobs" || r.Method != http.MethodPost {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"ps-remote-1","status":"queued"}`))
	}))
	defer paperspaceMock.Close()
	providers.OverrideBaseURL("paperspace", paperspaceMock.URL)

	insertProvider(t, conn, "modal", true)
	insertProvider(t, conn, "paperspace", true)

	px1 := insertProxy(t, conn, "proxy-us-1", "http://127.0.0.1:1", box)
	px2 := insertProxy(t, conn, "proxy-eu-1", "http://127.0.0.1:1", box)
	modalKey := insertKey(t, conn, "modal", "modal-primary", `{"token":"m"}`, px1, box, 10)
	paperKey := insertKey(t, conn, "paperspace", "paperspace-primary", `{"token":"p"}`, px2, box, 10)

	jobID := insertJob(t, conn, "any", 8, 1, []string{"modal", "paperspace"})

	s := New(conn, cfg, box, nil)
	if err := s.scheduleOne(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}

	var status, providerID, remoteID string
	var attempts int
	if err := conn.QueryRow(`SELECT status, COALESCE(provider_id,''), COALESCE(remote_id,''), attempts FROM jobs WHERE id=?`, jobID).
		Scan(&status, &providerID, &remoteID, &attempts); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Fatalf("status=%s, want running", status)
	}
	if providerID != "paperspace" {
		t.Fatalf("provider=%s, want paperspace (failover)", providerID)
	}
	if remoteID != "ps-remote-1" {
		t.Fatalf("remote_id=%s", remoteID)
	}

	// Attempt for modal must be recorded as failed, paperspace as running.
	var modalAttempts, psAttempts int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM job_attempts WHERE job_id=? AND provider_id='modal' AND status='failed'`, jobID).Scan(&modalAttempts)
	_ = conn.QueryRow(`SELECT COUNT(*) FROM job_attempts WHERE job_id=? AND provider_id='paperspace' AND status='running'`, jobID).Scan(&psAttempts)
	if modalAttempts != 1 || psAttempts != 1 {
		t.Fatalf("attempts modal=%d paperspace=%d (want 1/1)", modalAttempts, psAttempts)
	}

	// Key usage incremented only for paperspace.
	var modalUsage int
	_ = conn.QueryRow(`SELECT usage_today FROM provider_keys WHERE id=?`, modalKey).Scan(&modalUsage)
	if modalUsage != 0 {
		t.Fatalf("modal key usage=%d, want 0", modalUsage)
	}
	var psUsage int
	_ = conn.QueryRow(`SELECT usage_today FROM provider_keys WHERE id=?`, paperKey).Scan(&psUsage)
	if psUsage != 1 {
		t.Fatalf("paperspace key usage=%d, want 1", psUsage)
	}
}

func TestFailover_429QuotaToNextProvider(t *testing.T) {
	conn, box, cfg := setupTestDB(t)

	paperspaceMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"rate limited"}`, http.StatusTooManyRequests)
	}))
	defer paperspaceMock.Close()
	saturnMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"saturn-remote-9"}`))
	}))
	defer saturnMock.Close()
	providers.OverrideBaseURL("paperspace", paperspaceMock.URL)
	providers.OverrideBaseURL("saturn", saturnMock.URL)

	insertProvider(t, conn, "paperspace", true)
	insertProvider(t, conn, "saturn", true)

	px1 := insertProxy(t, conn, "proxy-us-1", "http://127.0.0.1:1", box)
	px2 := insertProxy(t, conn, "proxy-eu-1", "http://127.0.0.1:1", box)
	psKey := insertKey(t, conn, "paperspace", "ps", `{"token":"p"}`, px1, box, 10)
	saturnKey := insertKey(t, conn, "saturn", "sat", `{"token":"s"}`, px2, box, 20)

	jobID := insertJob(t, conn, "any", 8, 1, []string{"paperspace", "saturn"})
	s := New(conn, cfg, box, nil)
	if err := s.scheduleOne(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}

	var status, providerID string
	_ = conn.QueryRow(`SELECT status, COALESCE(provider_id,'') FROM jobs WHERE id=?`, jobID).Scan(&status, &providerID)
	if status != "running" || providerID != "saturn" {
		t.Fatalf("status=%s provider=%s, want running/saturn", status, providerID)
	}
	var quotaAttempts int
	_ = conn.QueryRow(`SELECT COUNT(*) FROM job_attempts WHERE job_id=? AND provider_id='paperspace' AND status='quota'`, jobID).Scan(&quotaAttempts)
	if quotaAttempts != 1 {
		t.Fatalf("expected 1 quota attempt for paperspace, got %d", quotaAttempts)
	}
	// Paperspace key must be in cooldown.
	var hasCooldown bool
	_ = conn.QueryRow(`SELECT cooldown_until IS NOT NULL FROM provider_keys WHERE id=?`, psKey).Scan(&hasCooldown)
	if !hasCooldown {
		t.Fatal("paperspace key should have cooldown_until set after 429")
	}
	_ = saturnKey
}

func TestUnschedulable_NoMatchingProvider(t *testing.T) {
	conn, box, cfg := setupTestDB(t)

	insertProvider(t, conn, "paperspace", true)
	px1 := insertProxy(t, conn, "proxy-us-1", "http://127.0.0.1:1", box)
	_ = insertKey(t, conn, "paperspace", "ps", `{"token":"p"}`, px1, box, 10)

	// Paperspace has no H100.
	jobID := insertJob(t, conn, "H100", 80, 1, []string{"paperspace"})
	s := New(conn, cfg, box, nil)
	if err := s.scheduleOne(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	var status string
	var rejections string
	_ = conn.QueryRow(`SELECT status, rejections FROM jobs WHERE id=?`, jobID).Scan(&status, &rejections)
	if status != "unschedulable" {
		t.Fatalf("status=%s, want unschedulable", status)
	}
	if !strings.Contains(rejections, "paperspace") {
		t.Fatalf("rejections should mention paperspace, got %s", rejections)
	}
}

func TestDisabledProviderIsRejected(t *testing.T) {
	conn, box, cfg := setupTestDB(t)
	insertProvider(t, conn, "paperspace", false) // disabled
	px1 := insertProxy(t, conn, "proxy-us-1", "http://127.0.0.1:1", box)
	_ = insertKey(t, conn, "paperspace", "ps", `{"token":"p"}`, px1, box, 10)

	jobID := insertJob(t, conn, "any", 8, 1, []string{"paperspace"})
	s := New(conn, cfg, box, nil)
	if err := s.scheduleOne(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	var status string
	_ = conn.QueryRow(`SELECT status FROM jobs WHERE id=?`, jobID).Scan(&status)
	if status != "unschedulable" {
		t.Fatalf("status=%s, want unschedulable (disabled provider)", status)
	}
}

func TestPollOne_FailureTriggersFailover(t *testing.T) {
	conn, box, cfg := setupTestDB(t)

	failMock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Status endpoint reports failure.
		_, _ = w.Write([]byte(`{"status":"failed","message":"cuda oom"}`))
	}))
	defer failMock.Close()
	providers.OverrideBaseURL("paperspace", failMock.URL)

	insertProvider(t, conn, "paperspace", true)
	px1 := insertProxy(t, conn, "proxy-us-1", "http://127.0.0.1:1", box)
	_ = insertKey(t, conn, "paperspace", "ps", `{"token":"p"}`, px1, box, 10)
	jobID := insertJob(t, conn, "any", 8, 1, []string{"paperspace"})
	_, _ = conn.ExecContext(context.Background(), `UPDATE jobs SET status='running', provider_id='paperspace', key_id=(SELECT id FROM provider_keys WHERE label='ps'), remote_id='ps-remote-x', started_at=CURRENT_TIMESTAMP`)

	s := New(conn, cfg, box, nil)
	if err := s.pollOne(context.Background(), jobID); err != nil {
		t.Fatal(err)
	}
	var status string
	var attempts int
	_ = conn.QueryRow(`SELECT status, attempts FROM jobs WHERE id=?`, jobID).Scan(&status, &attempts)
	if status != "queued" {
		t.Fatalf("status=%s, want queued (failover requeue)", status)
	}
	if providerID := func() string {
		var v sql.NullString
		_ = conn.QueryRow(`SELECT provider_id FROM jobs WHERE id=?`, jobID).Scan(&v)
		return v.String
	}(); providerID != "" {
		t.Fatalf("provider_id=%q, want cleared for failover", providerID)
	}
}

func TestStickyBindingHelper(t *testing.T) {
	conn, box, _ := setupTestDB(t)
	insertProvider(t, conn, "kaggle", true)
	px1 := insertProxy(t, conn, "proxy-us-1", "socks5://127.0.0.1:1080", box)
	px2 := insertProxy(t, conn, "proxy-eu-1", "socks5://127.0.0.2:1080", box)
	key := insertKey(t, conn, "kaggle", "k1", `{"token":"t"}`, px1, box, 10)

	// 100 lookups: always px1.
	for i := 0; i < 100; i++ {
		var got sql.NullInt64
		if err := conn.QueryRow(`SELECT bound_proxy_id FROM provider_keys WHERE id=?`, key).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if !got.Valid || got.Int64 != px1 {
			t.Fatalf("iteration %d: proxy changed unexpectedly to %v", i, got)
		}
	}
	// Manual reassign only.
	_, _ = conn.Exec(`UPDATE provider_keys SET bound_proxy_id=? WHERE id=?`, px2, key)
	var got sql.NullInt64
	_ = conn.QueryRow(`SELECT bound_proxy_id FROM provider_keys WHERE id=?`, key).Scan(&got)
	if got.Int64 != px2 {
		t.Fatal("manual reassign did not persist")
	}
}
