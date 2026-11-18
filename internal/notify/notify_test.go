package notify

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDiscordWebhook_GetsEmbedPayload(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	// URL must look like a discord webhook to trigger the embed path.
	payload := SendWebhook // just to reference; we call buildPayload directly below
	_ = payload

	// Use the package-internal builder through an injected URL containing "discord.com/api/webhooks".
	// The httptest URL can't contain that host substring; so verify buildPayload directly instead.
	body := buildPayload("https://discord.com/api/webhooks/123/abc", "critical", "Proxy #1 down — key disabled")
	var embedMsg map[string]any
	if err := json.Unmarshal(body, &embedMsg); err != nil {
		t.Fatal(err)
	}
	username, _ := embedMsg["username"].(string)
	embeds, ok := embedMsg["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("expected exactly 1 embed, got %v", embedMsg)
	}
	embed := embeds[0].(map[string]any)
	if username != "GPUHub" {
		t.Fatalf("username = %q, want GPUHub", username)
	}
	if embed["title"] == nil || embed["description"] != "Proxy #1 down — key disabled" {
		t.Fatalf("embed malformed: %v", embed)
	}
	if color, _ := embed["color"].(float64); color != 0xe74c3c {
		t.Fatalf("color = %v, want critical red (15158332)", embed["color"])
	}
}

func TestGenericWebhook_GetsPlainJSON(t *testing.T) {
	body := buildPayload("https://hooks.slack.com/services/T/B/X", "warn", "something")
	var msg map[string]string
	if err := json.Unmarshal(body, &msg); err != nil {
		t.Fatal(err)
	}
	if msg["level"] != "warn" || msg["message"] != "something" || msg["source"] != "gpuhub" {
		t.Fatalf("plain payload malformed: %v", msg)
	}
}

func TestSendWebhook_Posts(t *testing.T) {
	var got map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if err := SendWebhook(context.Background(), srv.URL, "info", "hello"); err != nil {
		t.Fatal(err)
	}
	if got["message"] != "hello" || got["level"] != "info" {
		t.Fatalf("posted payload = %v", got)
	}
}

func TestSendWebhook_DiscordFailureIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusInternalServerError)
	}))
	defer srv.Close()
	// A non-discord URL keeps plain format — we just check error propagation.
	if err := SendWebhook(context.Background(), srv.URL, "critical", "x"); err == nil {
		t.Fatal("expected error on 5xx webhook")
	}
}
