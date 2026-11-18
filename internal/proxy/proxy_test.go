package proxy

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"gpuhub/internal/crypto"
	"gpuhub/internal/db"
)

const testMasterKey = "test-master-key-0123456789abcdef"

func setup(t *testing.T) (*sql.DB, *crypto.Box) {
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
	return conn, box
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

func insertKey(t *testing.T, conn *sql.DB, providerID, label string, proxyID int64, box *crypto.Box) int64 {
	t.Helper()
	enc, err := box.Encrypt(`{"token":"secret"}`)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = conn.ExecContext(context.Background(), `INSERT INTO providers(id,name,enabled) VALUES(?,?,1) ON CONFLICT(id) DO NOTHING`, providerID, providerID)
	res, err := conn.ExecContext(context.Background(), `INSERT INTO provider_keys(provider_id,label,credential_enc,bound_proxy_id,active) VALUES(?,?,?,?,1)`,
		providerID, label, enc, proxyID)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	return id
}

// STICKY PROXY acceptance test: the same key must always egress through the same proxy,
// across 100 consecutive lookups, until an explicit manual reassign happens.
func TestStickyTransport_100CallsSameProxy(t *testing.T) {
	conn, box := setup(t)
	px1 := insertProxy(t, conn, "proxy-us-1", "http://10.0.0.1:8080", box)
	px2 := insertProxy(t, conn, "proxy-eu-1", "http://10.0.0.2:8080", box)
	key := insertKey(t, conn, "kaggle", "k1", px1, box)

	m := NewManager(conn)
	m.Decrypt = box.Decrypt
	defer func() {
		for id := range m.cache {
			m.InvalidateTransport(id)
		}
	}()

	for i := 0; i < 100; i++ {
		tr, url, err := m.TransportForKey(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		if url != "http://10.0.0.1:8080" {
			t.Fatalf("iteration %d: proxy url = %s, want px1", i, url)
		}
		if tr == nil {
			t.Fatal("nil transport")
		}
	}

	// Proxy list exists but is NOT used unless manual reassign happens.
	tr1, _, err := m.TransportForKey(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	tr2, _, err := m.TransportForKey(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if tr1 != tr2 {
		t.Fatal("transport cache not reused - connection pool would be rebuilt per call")
	}

	// Manual reassign (simulates admin PATCH /keys/{id}/proxy + InvalidateTransport).
	if _, err := conn.Exec(`UPDATE provider_keys SET bound_proxy_id=? WHERE id=?`, px2, key); err != nil {
		t.Fatal(err)
	}
	m.InvalidateTransport(key)
	_, url, err := m.TransportForKey(context.Background(), key)
	if err != nil {
		t.Fatal(err)
	}
	if url != "http://10.0.0.2:8080" {
		t.Fatalf("after manual reassign proxy url = %s, want px2", url)
	}
}

// When a proxy fails health checks, bound keys are deactivated - never auto-rebound.
func TestHealthCheckFailure_DisablesKeyNoRebind(t *testing.T) {
	conn, box := setup(t)

	// "Proxy" is an HTTP proxy endpoint that returns 502 for everything.
	failingProxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer failingProxy.Close()
	healthyProxy := insertProxy(t, conn, "proxy-healthy", "http://10.0.0.9:8080", box)

	badProxy := insertProxy(t, conn, "proxy-bad", failingProxy.URL, box)
	key := insertKey(t, conn, "kaggle", "k-bad", badProxy, box)

	m := NewManager(conn)
	m.Decrypt = box.Decrypt
	notifications := []string{}
	notify := func(level, msg string) { notifications = append(notifications, level+": "+msg) }

	checkURL := "http://gpuhub.internal/ping"
	if err := m.HealthCheckAll(context.Background(), checkURL, 3*time.Second, notify); err != nil {
		t.Fatal(err)
	}

	var active int
	var proxyID sql.NullInt64
	if err := conn.QueryRow(`SELECT active, bound_proxy_id FROM provider_keys WHERE id=?`, key).Scan(&active, &proxyID); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("key bound to unhealthy proxy should be deactivated")
	}
	if !proxyID.Valid || proxyID.Int64 != badProxy {
		t.Fatal("key must stay bound to the unhealthy proxy - no automatic rebinding allowed")
	}
	if proxyID.Int64 == healthyProxy {
		t.Fatal("key was rebound to another proxy - sticky binding violated")
	}
	if len(notifications) == 0 {
		t.Fatal("admin must be notified about the deactivation")
	}
	// Proxy marked unhealthy in DB.
	var health string
	_ = conn.QueryRow(`SELECT health_status FROM proxies WHERE id=?`, badProxy).Scan(&health)
	if health != "unhealthy" {
		t.Fatalf("health_status=%s, want unhealthy", health)
	}
}

// Deleting a proxy removes the binding and disables the key; nothing is rebound automatically.
func TestProxyDeleteLogicSimulation(t *testing.T) {
	conn, box := setup(t)
	px1 := insertProxy(t, conn, "proxy-us-1", "http://10.0.0.1:8080", box)
	_ = insertProxy(t, conn, "proxy-eu-1", "http://10.0.0.2:8080", box)
	key := insertKey(t, conn, "kaggle", "k1", px1, box)

	// Mirrors handleDeleteProxy: detach + disable inside one transaction.
	tx, err := conn.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`UPDATE provider_keys SET bound_proxy_id=NULL, active=0 WHERE bound_proxy_id=?`, px1); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(`DELETE FROM proxies WHERE id=?`, px1); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	var active int
	var proxyID sql.NullInt64
	if err := conn.QueryRow(`SELECT active, bound_proxy_id FROM provider_keys WHERE id=?`, key).Scan(&active, &proxyID); err != nil {
		t.Fatal(err)
	}
	if active != 0 {
		t.Fatal("key must be disabled after its proxy is deleted")
	}
	if proxyID.Valid {
		t.Fatal("proxy binding must be NULL after deletion - never auto-rebound")
	}
}

func TestParseProxyURL(t *testing.T) {
	good := []string{"http://u:p@host:3128", "https://host:3128", "socks5://user:pass@1.2.3.4:1080", "socks5h://1.2.3.4:1080"}
	for _, g := range good {
		if _, err := ParseProxyURL(g); err != nil {
			t.Errorf("%s: %v", g, err)
		}
	}
	bad := []string{"ftp://host", "host:3128", "http://", ""}
	for _, b := range bad {
		if _, err := ParseProxyURL(b); err == nil {
			t.Errorf("%s: expected error", b)
		}
	}
}

func TestRedact(t *testing.T) {
	got := Redact("http://admin:s3cret@proxy.example.com:8080")
	want := "http://admin:****@proxy.example.com:8080"
	if got != want {
		t.Fatalf("Redact = %q, want %q", got, want)
	}
}
