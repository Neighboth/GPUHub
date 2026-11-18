// Package providers defines the Provider adapter interface and all built-in adapters.
package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// Money is an integer count of micro-dollars (1e-6 USD). 1 USD = 1_000_000.
type Money int64

func (m Money) USD() float64 { return float64(m) / 1_000_000 }

func (m Money) String() string { return fmt.Sprintf("$%.4f", m.USD()) }

func ParseMoneyUSD(s string) (Money, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return Money(f * 1_000_000), err
}

// GPU is one GPU offering from a provider.
type GPU struct {
	Type     string `json:"type"` // canonical GPUType, e.g. "T4"
	VRAMGB   int    `json:"vram_gb"`
	Count    int    `json:"count"`     // available count right now
	MaxCount int    `json:"max_count"` // max per job
	Spot     bool   `json:"spot"`
}

// Capabilities describes what a provider can do, without network calls.
type Capabilities struct {
	GPUs             []GPU    `json:"gpus"`
	MaxDurationHours int      `json:"max_duration_hours"` // 0 = unlimited
	Frameworks       []string `json:"frameworks"`         // e.g. ["pytorch","tensorflow","jax"]
	Notebooks        bool     `json:"notebooks"`
	Docker           bool     `json:"docker"`
	SSH              bool     `json:"ssh"`
	FreeTierNotes    string   `json:"free_tier_notes"` // summary shown in UI/README
}

// Credential is the decrypted secret material for a key.
// Enc is the opaque JSON blob stored encrypted; fields are adapter-specific.
type Credential struct {
	KeyID int64
	Raw   string // decrypted; adapters parse this (JSON or token)
}

type Quota struct {
	DailyUsed   int     `json:"daily_used"`
	DailyLimit  int     `json:"daily_limit"` // 0 = unknown/unlimited
	WeeklyUsed  int     `json:"weekly_used"`
	WeeklyLimit int     `json:"weekly_limit"` // 0 = unknown/unlimited
	Percent     float64 `json:"percent"`      // 0..1 of the binding constraint; 1 = exhausted
	ResetsAt    string  `json:"resets_at,omitempty"`
	Raw         string  `json:"raw,omitempty"`
}

type JobType string

const (
	JobScript   JobType = "script"
	JobNotebook JobType = "notebook"
	JobDocker   JobType = "docker"
	JobCommand  JobType = "command"
)

// JobSpec is the provider-agnostic job description. GPUType/MinVRAMGB/GPUCount are enforced upstream.
type JobSpec struct {
	Type               JobType       `json:"type"`
	Payload            string        `json:"payload"`
	GPUType            string        `json:"gpu_type"`                    // canonical name or "any"
	GPUTypeFallback    []string      `json:"gpu_type_fallback,omitempty"` // ordered fallbacks, e.g. ["A100","H100","L40S"]
	MinVRAMGB          int           `json:"min_vram_gb"`
	GPUCount           int           `json:"gpu_count"`
	MaxDuration        time.Duration `json:"max_duration"`
	ProviderPreference []string      `json:"provider_preference,omitempty"`
	MaxCost            Money         `json:"max_cost_usd_micros"`
	SessionRenew       bool          `json:"session_renew,omitempty"`
}

type RemoteJobID string

type JobStatus struct {
	State    string `json:"state"` // queued|running|succeeded|failed|cancelled|timeout|unknown
	Progress int    `json:"progress,omitempty"`
	Message  string `json:"message,omitempty"`
	Cost     Money  `json:"cost_usd_micros"`
	Raw      string `json:"raw,omitempty"`
}

// Rejection collects why a provider failed the scheduling filter.
type Rejection struct {
	ProviderID string `json:"provider_id"`
	Reason     string `json:"reason"`
}

// Provider is the adapter interface every GPU provider implements.
type Provider interface {
	ID() string
	Name() string
	Capabilities() Capabilities
	ValidateKey(ctx context.Context, cred Credential, proxyURL string) error
	ListGPUs(ctx context.Context, cred Credential, proxyURL string) ([]GPU, error)
	Quota(ctx context.Context, cred Credential, proxyURL string) (Quota, error)
	Submit(ctx context.Context, cred Credential, proxyURL string, job JobSpec) (RemoteJobID, error)
	Status(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) (JobStatus, error)
	Logs(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) (io.Reader, error)
	Cancel(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) error
	Cost(ctx context.Context, cred Credential, proxyURL string, id RemoteJobID) (Money, error)
}

// CredentialPayload is the JSON shape stored (encrypted) for a key.
// Adapters that need multiple fields (e.g. Kaggle username+key) use this; simple token adapters use {"token":"..."}.
type CredentialPayload struct {
	Token    string            `json:"token,omitempty"`
	Username string            `json:"username,omitempty"`
	Key      string            `json:"key,omitempty"`
	BaseURL  string            `json:"base_url,omitempty"`
	Extra    map[string]string `json:"extra,omitempty"`
}

func ParseCredential(raw string) (CredentialPayload, error) {
	var p CredentialPayload
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "{") {
		if err := json.Unmarshal([]byte(s), &p); err != nil {
			return p, fmt.Errorf("credential JSON invalid: %w", err)
		}
		// Accept "url" as an alias for base_url.
		if p.BaseURL == "" {
			if u, ok := p.Extra["url"]; ok {
				p.BaseURL = u
			}
		}
		// Accept top-level "url" (unknown field — parse manually).
		if p.BaseURL == "" {
			var withURL struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal([]byte(s), &withURL)
			p.BaseURL = withURL.URL
		}
		return p, nil
	}
	// Treat opaque strings as token.
	p.Token = s
	return p, nil
}
