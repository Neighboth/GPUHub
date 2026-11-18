package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Port          int    `yaml:"port"`
	DataDir       string `yaml:"data_dir"`
	DatabasePath  string `yaml:"database_path"`
	MasterKey     string `yaml:"-"`
	JWTSecret     string `yaml:"-"`
	AdminUsername string `yaml:"-"`
	AdminPassword string `yaml:"-"`
	Dev           bool   `yaml:"dev"`

	// Scheduler
	SchedulerStrategy  string        `yaml:"scheduler_strategy"` // round_robin | least_used | most_quota | weighted
	RotationStrategy   string        `yaml:"rotation_strategy"`  // key rotation among keys of one provider (never proxies)
	PollInterval       time.Duration `yaml:"poll_interval"`
	SchedulerInterval  time.Duration `yaml:"scheduler_interval"`
	MaxRetries         int           `yaml:"max_retries"`
	QuotaHighWatermark float64       `yaml:"quota_high_watermark"` // 0.90 => steer away at 90%
	SchedulerWorkers   int           `yaml:"scheduler_workers"`

	// Proxy
	ProxyHealthInterval time.Duration `yaml:"proxy_health_interval"`
	ProxyHealthURL      string        `yaml:"proxy_health_url"`
	ProxyHealthTimeout  time.Duration `yaml:"proxy_health_timeout"`

	// Notifications
	NotifyWebhookURL string `yaml:"notify_webhook_url"`

	BaseURL string `yaml:"base_url"`
}

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// Load reads ./config.yaml (optional) and then applies environment overrides.
// Environment wins over YAML, per the spec (.env + config.yaml).
func Load() (*Config, error) {
	cfg := &Config{
		Port:                8080,
		DataDir:             "./data",
		SchedulerStrategy:   "most_quota",
		RotationStrategy:    "most_quota",
		PollInterval:        5 * time.Second,
		SchedulerInterval:   2 * time.Second,
		MaxRetries:          3,
		QuotaHighWatermark:  0.90,
		SchedulerWorkers:    4,
		ProxyHealthInterval: 60 * time.Second,
		ProxyHealthURL:      "https://api.ipify.org?format=json",
		ProxyHealthTimeout:  15 * time.Second,
	}

	if b, err := os.ReadFile("config.yaml"); err == nil {
		if err := yaml.Unmarshal(b, cfg); err != nil {
			return nil, fmt.Errorf("config.yaml: %w", err)
		}
	}

	if v := os.Getenv("GPUHUB_PORT"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.Port)
	}
	if v := os.Getenv("GPUHUB_DATA_DIR"); v != "" {
		cfg.DataDir = v
	}
	if v := os.Getenv("GPUHUB_DB_PATH"); v != "" {
		cfg.DatabasePath = v
	}
	cfg.MasterKey = os.Getenv("GPUHUB_MASTER_KEY")
	cfg.JWTSecret = os.Getenv("GPUHUB_JWT_SECRET")
	cfg.AdminUsername = getenv("ADMIN_USERNAME", "admin")
	cfg.AdminPassword = os.Getenv("ADMIN_PASSWORD")
	if v := os.Getenv("GPUHUB_DEV"); v == "1" || strings.EqualFold(v, "true") {
		cfg.Dev = true
	}
	if v := os.Getenv("GPUHUB_SCHEDULER_STRATEGY"); v != "" {
		cfg.SchedulerStrategy = v
	}
	if v := os.Getenv("GPUHUB_ROTATION_STRATEGY"); v != "" {
		cfg.RotationStrategy = v
	}
	if v := os.Getenv("GPUHUB_PROXY_HEALTH_URL"); v != "" {
		cfg.ProxyHealthURL = v
	}
	if v := os.Getenv("GPUHUB_NOTIFY_WEBHOOK_URL"); v != "" {
		cfg.NotifyWebhookURL = v
	}
	if v := os.Getenv("GPUHUB_BASE_URL"); v != "" {
		cfg.BaseURL = v
	}
	if v := os.Getenv("GPUHUB_MAX_RETRIES"); v != "" {
		fmt.Sscanf(v, "%d", &cfg.MaxRetries)
	}
	if v := os.Getenv("GPUHUB_QUOTA_HIGH_WATERMARK"); v != "" {
		fmt.Sscanf(v, "%f", &cfg.QuotaHighWatermark)
	}

	if cfg.DatabasePath == "" {
		cfg.DatabasePath = cfg.DataDir + string(os.PathSeparator) + "gpuhub.db"
	}

	if cfg.MasterKey == "" {
		if cfg.Dev {
			// Ephemeral dev key: generated per boot, data encrypted this run becomes unreadable after restart.
			cfg.MasterKey = "dev-only-insecure-key-do-not-use-0000"
		} else {
			return nil, fmt.Errorf("GPUHUB_MASTER_KEY is required (set it in .env; 32+ random bytes, e.g. openssl rand -base64 32)")
		}
	}
	if cfg.JWTSecret == "" {
		cfg.JWTSecret = cfg.MasterKey // acceptable; separate secrets recommended
	}
	return cfg, nil
}
