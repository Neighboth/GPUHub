// Package proxy implements GPUHub's STICKY PROXY BINDING model.
//
// Rules (enforced here and in the API layer):
//   - A proxy, once bound to a key, stays bound until an admin manually reassigns
//     it or the proxy is deleted. There is NO rotation of proxies, ever.
//   - Every request made on behalf of a key goes through the exact same proxy,
//     using a cached *http.Transport per key (key_id -> *http.Transport) so the
//     same connection pool is reused.
//   - Health checks run every 60s. Unhealthy proxies are marked, and keys bound
//     to them are deactivated. They are NEVER auto-rebound to another proxy.
package proxy

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/proxy"
)

type Proxy struct {
	ID              int64      `json:"id"`
	Label           string     `json:"label"`
	URL             string     `json:"url"`
	Country         string     `json:"country"`
	HealthStatus    string     `json:"health_status"`
	LastCheckAt     *time.Time `json:"last_check_at"`
	CheckError      string     `json:"check_error,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	BoundKeyLabel   string     `json:"bound_key_label,omitempty"`
	BoundProviderID string     `json:"bound_provider_id,omitempty"`
}

type Manager struct {
	db      *sql.DB
	cache   map[int64]*http.Transport
	cacheMu sync.Mutex
	Timeout time.Duration

	// Decrypt, if set, is used to resolve stored proxy URLs (which may be "enc:"-prefixed
	// and encrypted at rest). Set by cmd/gpuhub; nil in tests unless needed.
	Decrypt func(string) (string, error)
}

func NewManager(db *sql.DB) *Manager {
	return &Manager{db: db, cache: map[int64]*http.Transport{}, Timeout: 60 * time.Second}
}

// ParseProxyURL validates a proxy URL. Supports http, https and socks5, with optional user:pass@.
func ParseProxyURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid proxy url: %w", err)
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("unsupported proxy scheme %q (allowed: http, https, socks5)", u.Scheme)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("proxy url missing host")
	}
	return u, nil
}

// Redact returns a display-safe URL with the password masked.
func Redact(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "***"
	}
	if u.User == nil {
		return raw
	}
	user := u.User.Username()
	if _, hasPass := u.User.Password(); hasPass {
		prefix := u.Scheme + "://" + user + ":****@"
		host := u.Host
		suffix := ""
		if u.Path != "/" {
			suffix = u.Path
		}
		if u.RawQuery != "" {
			suffix += "?" + u.RawQuery
		}
		return prefix + host + suffix
	}
	return u.Scheme + "://" + user + "@" + u.Host + u.Path
}

// buildTransport creates (or returns cached) transport for a key pinned to one proxy.
// The binding key for the cache is keyID; if the key's proxy changed, the admin reassign
// path calls InvalidateTransport.
func (m *Manager) buildTransport(keyID int64, proxyURL string) (*http.Transport, error) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if t, ok := m.cache[keyID]; ok {
		return t, nil
	}
	u, err := ParseProxyURL(proxyURL)
	if err != nil {
		return nil, err
	}
	t := &http.Transport{
		MaxIdleConns:          10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 45 * time.Second,
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "socks5" || scheme == "socks5h" {
		// http.ProxyURL only speaks HTTP CONNECT; SOCKS5 needs a custom dialer.
		var auth *proxy.Auth
		if u.User != nil {
			pass, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pass}
		}
		d, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return nil, err
		}
		t.DialContext = func(ctx context.Context, network, addr string) (conn net.Conn, err error) {
			type ctxDialer interface {
				DialContext(context.Context, string, string) (net.Conn, error)
			}
			if cd, ok := d.(ctxDialer); ok {
				return cd.DialContext(ctx, network, addr)
			}
			type dialer interface {
				Dial(string, string) (net.Conn, error)
			}
			c, err := d.(dialer).Dial(network, addr)
			if err != nil {
				return nil, err
			}
			return c, nil
		}
	} else {
		// http/https proxy via CONNECT. Proxy func is constant for this transport.
		proxyURLCopy := *u
		t.Proxy = func(*http.Request) (*url.URL, error) { return &proxyURLCopy, nil }
	}
	m.cache[keyID] = t
	return t, nil
}

// TransportForKey returns the cached, proxy-pinned transport for a key.
// This is the ONLY way provider adapters get an *http.Client — guaranteeing sticky binding.
func (m *Manager) TransportForKey(ctx context.Context, keyID int64) (*http.Transport, string, error) {
	var proxyID sql.NullInt64
	var proxyURL string
	err := m.db.QueryRowContext(ctx, `
		SELECT k.bound_proxy_id, COALESCE(p.url,'')
		FROM provider_keys k LEFT JOIN proxies p ON p.id = k.bound_proxy_id
		WHERE k.id = ?`, keyID).Scan(&proxyID, &proxyURL)
	if err != nil {
		return nil, "", err
	}
	if !proxyID.Valid {
		// Direct egress is allowed — sticky binding applies only to BOUND proxies.
		m.cacheMu.Lock()
		if t, ok := m.cache[keyID]; ok {
			m.cacheMu.Unlock()
			return t, "", nil
		}
		t := &http.Transport{MaxIdleConns: 10, IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 45 * time.Second}
		m.cache[keyID] = t
		m.cacheMu.Unlock()
		return t, "", nil
	}
	rawURL, err := m.resolveURL(proxyURL)
	if err != nil {
		return nil, "", err
	}
	t, err := m.buildTransport(keyID, rawURL)
	if err != nil {
		return nil, "", err
	}
	return t, rawURL, nil
}

// ClientForKey returns an *http.Client that ALWAYS egresses via the key's bound proxy.
func (m *Manager) ClientForKey(ctx context.Context, keyID int64) (*http.Client, error) {
	t, _, err := m.TransportForKey(ctx, keyID)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: t, Timeout: m.Timeout}, nil
}

// InvalidateTransport drops the cached transport (called on manual reassign / proxy delete).
func (m *Manager) InvalidateTransport(keyID int64) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	if t, ok := m.cache[keyID]; ok {
		t.CloseIdleConnections()
		delete(m.cache, keyID)
	}
}

// resolveURL decrypts enc:-prefixed URLs when a Decrypt function is configured; otherwise returns stored value.
func (m *Manager) resolveURL(stored string) (string, error) {
	if strings.HasPrefix(stored, "enc:") {
		if m.Decrypt == nil {
			return "", fmt.Errorf("proxy url is encrypted but Manager.Decrypt is not set")
		}
		return m.Decrypt(strings.TrimPrefix(stored, "enc:"))
	}
	return stored, nil
}

// HealthCheckAll tests every proxy that is bound to at least one key.
// Unhealthy proxies are marked; bound keys are deactivated; admins are notified.
// Proxies are NEVER auto-rebound — spec section 6.
func (m *Manager) HealthCheckAll(ctx context.Context, checkURL string, timeout time.Duration, notify func(level, msg string)) error {
	rows, err := m.db.QueryContext(ctx, `
		SELECT DISTINCT p.id, p.url FROM proxies p
		JOIN provider_keys k ON k.bound_proxy_id = p.id`)
	if err != nil {
		return err
	}
	type target struct {
		id  int64
		url string
	}
	var targets []target
	for rows.Next() {
		var t target
		if err := rows.Scan(&t.id, &t.url); err == nil {
			targets = append(targets, t)
		}
	}
	rows.Close()

	for _, t := range targets {
		rawURL, err := m.resolveURL(t.url)
		var status, checkErr string
		if err != nil {
			status, checkErr = "unhealthy", err.Error()
		} else {
			status, checkErr = m.checkOne(ctx, rawURL, checkURL, timeout)
		}
		now := time.Now()
		_, _ = m.db.ExecContext(ctx, `UPDATE proxies SET health_status=?, last_check_at=?, check_error=? WHERE id=?`,
			status, now, checkErr, t.id)
		if status == "unhealthy" {
			res, _ := m.db.ExecContext(ctx, `UPDATE provider_keys SET active=0 WHERE bound_proxy_id=? AND active=1`, t.id)
			n, _ := res.RowsAffected()
			if notify != nil {
				notify("critical", fmt.Sprintf("Proxy #%d (%s) failed health check: %s. %d bound key(s) deactivated. NO automatic rebinding performed — manual reassign required.", t.id, Redact(rawURL), checkErr, n))
			}
			_, _ = m.db.ExecContext(ctx, `INSERT INTO audit_log(actor, action, detail) VALUES('system','proxy_health_fail', ?)`,
				fmt.Sprintf("proxy_id=%d error=%s keys_disabled=%d", t.id, checkErr, n))
		}
	}
	return nil
}

func (m *Manager) checkOne(ctx context.Context, proxyURL, checkURL string, timeout time.Duration) (string, string) {
	u, err := ParseProxyURL(proxyURL)
	if err != nil {
		return "unhealthy", err.Error()
	}
	t := &http.Transport{}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "socks5" || scheme == "socks5h" {
		var auth *proxy.Auth
		if u.User != nil {
			pass, _ := u.User.Password()
			auth = &proxy.Auth{User: u.User.Username(), Password: pass}
		}
		d, err := proxy.SOCKS5("tcp", u.Host, auth, proxy.Direct)
		if err != nil {
			return "unhealthy", err.Error()
		}
		t.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			type dialer interface {
				Dial(string, string) (net.Conn, error)
			}
			return d.(dialer).Dial(network, addr)
		}
	} else {
		p := *u
		t.Proxy = func(*http.Request) (*url.URL, error) { return &p, nil }
	}
	client := &http.Client{Transport: t, Timeout: timeout}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, checkURL, nil)
	if err != nil {
		return "unhealthy", err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return "unhealthy", err.Error()
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
	if resp.StatusCode >= 500 {
		return "unhealthy", fmt.Sprintf("check url returned %d", resp.StatusCode)
	}
	return "healthy", ""
}

// TestProxy performs an on-demand check (admin "Test" button). Does not change bindings.
func (m *Manager) TestProxy(ctx context.Context, proxyURL, checkURL string, timeout time.Duration) (string, string) {
	return m.checkOne(ctx, proxyURL, checkURL, timeout)
}

// HealthLoop runs HealthCheckAll on an interval until ctx is cancelled.
func (m *Manager) HealthLoop(ctx context.Context, interval time.Duration, checkURL string, timeout time.Duration, notify func(level, msg string)) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := m.HealthCheckAll(ctx, checkURL, timeout, notify); err != nil {
				log.Printf("proxy health check error: %v", err)
			}
		}
	}
}

// --- helpers for JSON passthrough used by adapters ---

func DoJSON(ctx context.Context, c *http.Client, method, url string, headers map[string]string, body io.Reader, out any) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	if out != nil && resp.StatusCode < 300 {
		defer resp.Body.Close()
		b, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		if err != nil {
			return resp, err
		}
		if len(strings.TrimSpace(string(b))) > 0 {
			if err := json.Unmarshal(b, out); err != nil {
				return resp, fmt.Errorf("decode response from %s: %w", url, err)
			}
		}
	}
	return resp, nil
}
