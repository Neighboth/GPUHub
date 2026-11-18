-- GPUHub initial schema
CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT NOT NULL UNIQUE,
  password_hash TEXT NOT NULL,
  role TEXT NOT NULL DEFAULT 'admin',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS proxies (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  label TEXT NOT NULL,
  url TEXT NOT NULL,
  country TEXT NOT NULL DEFAULT '',
  health_status TEXT NOT NULL DEFAULT 'unknown', -- unknown|healthy|unhealthy
  last_check_at TIMESTAMP,
  check_error TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- Provider catalog rows are seeded from the compiled-in registry at startup.
CREATE TABLE IF NOT EXISTS providers (
  id TEXT PRIMARY KEY,               -- stable adapter id, e.g. "kaggle"
  name TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  config TEXT NOT NULL DEFAULT '{}', -- JSON, provider-specific (e.g. extra scopes)
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- STICKY BINDING: bound_proxy_id is set at creation time and can only change via
-- explicit admin reassign or when the proxy is deleted (ON DELETE SET NULL).
-- UNIQUE partial index enforces 1 proxy -> at most 1 key.
CREATE TABLE IF NOT EXISTS provider_keys (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  provider_id TEXT NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
  label TEXT NOT NULL,
  credential_enc TEXT NOT NULL,
  bound_proxy_id INTEGER REFERENCES proxies(id) ON DELETE SET NULL, -- NULL => "no proxy" state, key is disabled
  active INTEGER NOT NULL DEFAULT 1,
  daily_quota INTEGER NOT NULL DEFAULT 0,   -- max jobs/day, 0 = unlimited
  weekly_quota INTEGER NOT NULL DEFAULT 0,  -- max jobs/week, 0 = unlimited
  usage_today INTEGER NOT NULL DEFAULT 0,
  usage_week INTEGER NOT NULL DEFAULT 0,
  usage_day TEXT NOT NULL DEFAULT '',       -- YYYY-MM-DD for daily reset
  usage_week_start TEXT NOT NULL DEFAULT '',-- YYYY-MM-DD of ISO week start
  priority INTEGER NOT NULL DEFAULT 100,    -- lower = preferred
  weight INTEGER NOT NULL DEFAULT 1,
  last_used_at TIMESTAMP,
  error_count INTEGER NOT NULL DEFAULT 0,
  consecutive_errors INTEGER NOT NULL DEFAULT 0,
  cooldown_until TIMESTAMP,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_keys_proxy_unique ON provider_keys(bound_proxy_id) WHERE bound_proxy_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS idx_keys_provider ON provider_keys(provider_id);

CREATE TABLE IF NOT EXISTS jobs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  type TEXT NOT NULL,                -- script|notebook|docker|command
  payload TEXT NOT NULL,
  gpu_type TEXT NOT NULL DEFAULT 'any',
  min_vram_gb INTEGER NOT NULL DEFAULT 0,
  gpu_count INTEGER NOT NULL DEFAULT 1,
  max_duration_seconds INTEGER NOT NULL DEFAULT 3600,
  provider_preference TEXT NOT NULL DEFAULT '[]', -- JSON array of provider ids
  max_cost_usd_micros INTEGER NOT NULL DEFAULT 0, -- 0 = unlimited
  status TEXT NOT NULL DEFAULT 'queued',    -- queued|scheduling|running|succeeded|failed|cancelled|timeout|unschedulable
  provider_id TEXT,
  key_id INTEGER REFERENCES provider_keys(id) ON DELETE SET NULL,
  remote_id TEXT,
  attempts INTEGER NOT NULL DEFAULT 0,
  cost_usd_micros INTEGER NOT NULL DEFAULT 0,
  fail_reason TEXT NOT NULL DEFAULT '',
  rejections TEXT NOT NULL DEFAULT '{}',    -- JSON map provider_id -> reason (set when unschedulable)
  logs TEXT NOT NULL DEFAULT '',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  started_at TIMESTAMP,
  finished_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_jobs_status ON jobs(status);
CREATE INDEX IF NOT EXISTS idx_jobs_created ON jobs(created_at);

CREATE TABLE IF NOT EXISTS job_attempts (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  provider_id TEXT NOT NULL,
  key_id INTEGER,
  proxy_id INTEGER,
  remote_id TEXT NOT NULL DEFAULT '',
  status TEXT NOT NULL DEFAULT 'running',   -- running|succeeded|failed|timeout|quota|cancelled
  error TEXT NOT NULL DEFAULT '',
  started_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  finished_at TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_attempts_job ON job_attempts(job_id);

CREATE TABLE IF NOT EXISTS key_usage (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  key_id INTEGER NOT NULL REFERENCES provider_keys(id) ON DELETE CASCADE,
  job_id INTEGER REFERENCES jobs(id) ON DELETE SET NULL,
  day TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_key_usage_key ON key_usage(key_id, day);

CREATE TABLE IF NOT EXISTS job_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  job_id INTEGER NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
  line TEXT NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_job_logs_job ON job_logs(job_id);

CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  actor TEXT NOT NULL,
  action TEXT NOT NULL,
  detail TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_ts ON audit_log(ts);

CREATE TABLE IF NOT EXISTS notifications (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  level TEXT NOT NULL DEFAULT 'info', -- info|warn|critical
  message TEXT NOT NULL,
  read INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS refresh_tokens (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash TEXT NOT NULL UNIQUE,
  expires_at TIMESTAMP NOT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
