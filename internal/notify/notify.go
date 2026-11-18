// Package notify delivers admin notifications: DB row + optional webhook
// (plain JSON or Discord embed format when the URL is a discord.com webhook).
package notify

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func isDiscordWebhook(url string) bool {
	u := strings.ToLower(url)
	return strings.Contains(u, "discord.com/api/webhooks/") || strings.Contains(u, "discordapp.com/api/webhooks/")
}

type discordEmbed struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Color       int    `json:"color"`
	Footer      struct {
		Text string `json:"text"`
	} `json:"footer"`
}

type discordPayload struct {
	Username string         `json:"username"`
	Embeds   []discordEmbed `json:"embeds"`
}

func levelColor(level string) int {
	switch level {
	case "critical":
		return 0xe74c3c // red
	case "warn":
		return 0xf1c40f // yellow
	default:
		return 0x2ecc71 // green
	}
}

func levelEmoji(level string) string {
	switch level {
	case "critical":
		return "🚨"
	case "warn":
		return "⚠️"
	default:
		return "ℹ️"
	}
}

func buildPayload(webhookURL, level, msg string) []byte {
	if isDiscordWebhook(webhookURL) {
		embed := discordEmbed{
			Title:       levelEmoji(level) + " GPUHub - " + strings.ToUpper(level),
			Description: msg,
			Color:       levelColor(level),
		}
		embed.Footer.Text = "gpuhub"
		b, _ := json.Marshal(discordPayload{Username: "GPUHub", Embeds: []discordEmbed{embed}})
		return b
	}
	b, _ := json.Marshal(map[string]string{"level": level, "message": msg, "source": "gpuhub"})
	return b
}

// SendWebhook posts the notification to the configured webhook. Discord webhooks get an embed.
func SendWebhook(ctx context.Context, webhookURL, level, msg string) error {
	if webhookURL == "" {
		return nil
	}
	body := buildPayload(webhookURL, level, msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	resp, err := http.DefaultClient.Do(req.WithContext(c))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}

// Notify writes a DB notification row (if db set, never fails the caller) and fires the webhook.
func Notify(db *sql.DB, webhookURL, level, msg string) {
	if db != nil {
		_, _ = db.ExecContext(context.Background(), `INSERT INTO notifications(level, message) VALUES(?,?)`, level, msg)
	}
	if webhookURL == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		_ = SendWebhook(ctx, webhookURL, level, msg)
	}()
}
