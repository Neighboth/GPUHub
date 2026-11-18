package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"sort"
	"strings"
	"time"

	"gpuhub/internal/config"
	"gpuhub/internal/crypto"
	"gpuhub/internal/providers"
)

type Scheduler struct {
	DB     *sql.DB
	Cfg    *config.Config
	Box    *crypto.Box
	Notify func(level, msg string) // may be nil
}

// Job mirrors the jobs table for scheduling purposes.
type Job struct {
	ID                 int64
	Type               providers.JobType
	Payload            string
	GPUType            string
	GPUFallback        []string
	MinVRAMGB          int
	GPUCount           int
	MaxDurationSeconds int
	ProviderPreference []string
	MaxCostMicros      int64
	Status             string
	ProviderID         sql.NullString
	KeyID              sql.NullInt64
	RemoteID           sql.NullString
	Attempts           int
	CostMicros         int64
	StartedAt          sql.NullTime
	SessionRenew       bool
}

type candidate struct {
	provider      providers.Provider
	keyID         int64
	keyLabel      string
	priority      int
	weight        int
	usage         int
	remaining     int64 // -1 = unknown/unlimited
	strategyScore int64
}

func New(db *sql.DB, cfg *config.Config, box *crypto.Box, notify func(level, msg string)) *Scheduler {
	return &Scheduler{DB: db, Cfg: cfg, Box: box, Notify: notify}
}

func (s *Scheduler) log(level, msg string) {
	if s.Notify != nil {
		s.Notify(level, msg)
	} else {
		log.Printf("[%s] %s", level, msg)
	}
}

func (s *Scheduler) Start(ctx context.Context) {
	go s.loop(ctx, s.Cfg.SchedulerInterval, func() { s.schedulePending(ctx) })
	go s.loop(ctx, s.Cfg.PollInterval, func() { s.pollRunning(ctx) })
}

func (s *Scheduler) loop(ctx context.Context, interval time.Duration, fn func()) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			fn()
		}
	}
}

// tickResetCounters resets per-day/per-week usage counters whose windows rolled over.
func (s *Scheduler) tickResetCounters(ctx context.Context) {
	today := time.Now().Format("2006-01-02")
	_, _ = s.DB.ExecContext(ctx, `UPDATE provider_keys SET usage_today=0, usage_day=? WHERE usage_day<>?`, today, today)
	// ISO-ish week start (Monday).
	now := time.Now()
	weekday := (int(now.Weekday()) + 6) % 7
	ws := now.AddDate(0, 0, -weekday).Format("2006-01-02")
	_, _ = s.DB.ExecContext(ctx, `UPDATE provider_keys SET usage_week=0, usage_week_start=? WHERE usage_week_start<>?`, ws, ws)
}

func (s *Scheduler) schedulePending(ctx context.Context) {
	s.tickResetCounters(ctx)
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM jobs WHERE status='queued' ORDER BY id LIMIT 5`)
	if err != nil {
		log.Printf("scheduler: %v", err)
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		_ = s.scheduleOne(ctx, id)
	}
}

func (s *Scheduler) loadJob(ctx context.Context, id int64) (*Job, error) {
	j := &Job{}
	var pref, fallback string
	var renew int
	err := s.DB.QueryRowContext(ctx, `SELECT id,type,payload,gpu_type,min_vram_gb,gpu_count,max_duration_seconds,
		provider_preference,max_cost_usd_micros,status,provider_id,key_id,remote_id,attempts,cost_usd_micros,started_at,gpu_fallback,session_renew
		FROM jobs WHERE id=?`, id).Scan(&j.ID, &j.Type, &j.Payload, &j.GPUType, &j.MinVRAMGB, &j.GPUCount,
		&j.MaxDurationSeconds, &pref, &j.MaxCostMicros, &j.Status, &j.ProviderID, &j.KeyID, &j.RemoteID,
		&j.Attempts, &j.CostMicros, &j.StartedAt, &fallback, &renew)
	if err != nil {
		return nil, err
	}
	j.SessionRenew = renew == 1
	_ = json.Unmarshal([]byte(pref), &j.ProviderPreference)
	_ = json.Unmarshal([]byte(fallback), &j.GPUFallback)
	return j, nil
}

func (s *Scheduler) specOf(j *Job) providers.JobSpec {
	return providers.JobSpec{
		Type: j.Type, Payload: j.Payload, GPUType: j.GPUType, GPUTypeFallback: j.GPUFallback,
		MinVRAMGB: j.MinVRAMGB,
		GPUCount:  j.GPUCount, MaxDuration: time.Duration(j.MaxDurationSeconds) * time.Second,
		ProviderPreference: j.ProviderPreference, MaxCost: providers.Money(j.MaxCostMicros),
		SessionRenew: j.SessionRenew,
	}
}

func (s *Scheduler) scheduleOne(ctx context.Context, id int64) error {
	res, err := s.DB.ExecContext(ctx, `UPDATE jobs SET status='scheduling', updated_at=CURRENT_TIMESTAMP WHERE id=? AND status='queued'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // someone else took it
	}
	j, err := s.loadJob(ctx, id)
	if err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='failed', fail_reason=? WHERE id=?`, "load job: "+err.Error(), id)
		return err
	}

	spec := s.specOf(j)
	rejections := map[string]string{}
	var candidates []candidate
	soft := false

	for _, p := range providers.All() {
		pid := p.ID()
		if len(spec.ProviderPreference) > 0 {
			found := false
			for _, q := range spec.ProviderPreference {
				if q == pid {
					found = true
					break
				}
			}
			if !found {
				rejections[pid] = "excluded by provider_preference"
				continue
			}
		}
		var enabled int
		if err := s.DB.QueryRowContext(ctx, `SELECT enabled FROM providers WHERE id=?`, pid).Scan(&enabled); err != nil || enabled != 1 {
			rejections[pid] = "provider disabled"
			continue
		}
		caps := p.Capabilities()
		if _, ok := matchProviderGPUs(caps, spec); !ok {
			rejections[pid] = rejectionReason(caps, spec)
			continue
		}
		keys, err := s.loadEligibleKeys(ctx, pid, false)
		if err != nil {
			rejections[pid] = "key scan error: " + err.Error()
			continue
		}
		if len(keys) == 0 {
			// Distinguish structural unavailability (no keys at all / wrong GPU)
			// from temporary cooldown — only the former may produce "unschedulable".
			softKeys, err := s.loadEligibleKeys(ctx, pid, true)
			if err == nil && len(softKeys) > 0 {
				soft = true
				rejections[pid] = "all keys in cooldown — waiting to retry"
			} else {
				rejections[pid] = "no eligible key (disabled, over local quota, or proxy unbound)"
			}
			continue
		}
		for _, k := range keys {
			k.provider = p
			candidates = append(candidates, k)
		}
	}

	if len(candidates) == 0 {
		rj, _ := json.Marshal(rejections)
		if soft && j.Attempts < s.Cfg.MaxRetries {
			// Temporary unavailability (cooldown): keep the job queued and retry next cycle.
			_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='queued', rejections=?, fail_reason='waiting on provider cooldown', updated_at=CURRENT_TIMESTAMP WHERE id=?`, string(rj), id)
			return nil
		}
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='unschedulable', rejections=?, fail_reason='no provider/key could run this job', updated_at=CURRENT_TIMESTAMP WHERE id=?`, string(rj), id)
		s.audit(ctx, "scheduler", "job_unschedulable", fmt.Sprintf("job_id=%d rejections=%s", id, string(rj)))
		return nil
	}

	// Sort: priority asc, then strategy.
	strategy := s.Cfg.RotationStrategy
	sort.SliceStable(candidates, func(a, b int) bool {
		if candidates[a].priority != candidates[b].priority {
			return candidates[a].priority < candidates[b].priority
		}
		switch strategy {
		case "least_used":
			return candidates[a].usage < candidates[b].usage
		case "most_quota":
			return candidates[a].remaining > candidates[b].remaining
		case "weighted":
			return rand.Float64()*float64(candidates[a].weight) > rand.Float64()*float64(candidates[b].weight)
		default: // round_robin
			return candidates[a].keyID < candidates[b].keyID
		}
	})

	budget := s.Cfg.MaxRetries - j.Attempts
	if budget <= 0 {
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='failed', fail_reason='max retries exceeded', updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
		return nil
	}

	lastErr := ""
	tried := 0
	for _, c := range candidates {
		if tried >= budget {
			break
		}
		tried++
		cred, err := s.decryptCred(ctx, c.keyID)
		if err != nil {
			lastErr = err.Error()
			continue
		}
		// Respect the provider-side quota watermark (spec section 7): skip keys at >=90%.
		qctx, cancel := context.WithTimeout(ctx, 20*time.Second)
		q, qerr := c.provider.Quota(qctx, cred, "")
		cancel()
		if qerr == nil && q.Percent >= s.Cfg.QuotaHighWatermark {
			rejections[c.provider.ID()] = fmt.Sprintf("provider quota at %.0f%% (watermark %.0f%%)", q.Percent*100, s.Cfg.QuotaHighWatermark*100)
			tried-- // this skip does not consume retry budget
			continue
		}
		remoteID, err := c.provider.Submit(providers.WithJobContext(ctx, j.ID, j.Attempts+tried), cred, "", spec)
		if err != nil {
			s.onAttemptFailure(ctx, j, c, err)
			lastErr = err.Error()
			continue
		}
		s.onAttemptSuccess(ctx, j, c, remoteID)
		return nil
	}

	if tried == 0 {
		// Everything was rejected by quota watermark — nothing was attempted; stay queued for next cycle.
		rj, _ := json.Marshal(rejections)
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='queued', rejections=?, fail_reason='all candidates at quota watermark', updated_at=CURRENT_TIMESTAMP WHERE id=?`, string(rj), id)
		return nil
	}
	newAttempts := j.Attempts + tried
	if newAttempts >= s.Cfg.MaxRetries {
		rj, _ := json.Marshal(rejections)
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='failed', attempts=?, fail_reason=?, rejections=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
			newAttempts, "all candidates failed: "+truncate(lastErr, 500), string(rj), id)
	} else {
		rj, _ := json.Marshal(rejections)
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='queued', attempts=?, rejections=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
			newAttempts, string(rj), id)
	}
	return nil
}

// loadEligibleKeys returns active, proxy-bound, quota-available keys for a provider.
// With ignoreCooldown=true, keys temporarily in cooldown are included (used only to
// distinguish "soft" unavailability from structural rejection).
func (s *Scheduler) loadEligibleKeys(ctx context.Context, providerID string, ignoreCooldown bool) ([]candidate, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id,label,priority,weight,usage_today,daily_quota,usage_week,weekly_quota,cooldown_until FROM provider_keys
		WHERE provider_id=? AND active=1
		ORDER BY priority ASC, id ASC`, providerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		var dailyQuota, weeklyQuota int
		var usageToday, usageWeek int
		var cooldown sql.NullTime
		if err := rows.Scan(&c.keyID, &c.keyLabel, &c.priority, &c.weight, &usageToday, &dailyQuota, &usageWeek, &weeklyQuota, &cooldown); err != nil {
			return nil, err
		}
		if !ignoreCooldown && cooldown.Valid && cooldown.Time.After(time.Now()) {
			continue // in cooldown
		}
		if dailyQuota > 0 && usageToday >= dailyQuota {
			continue
		}
		if weeklyQuota > 0 && usageWeek >= weeklyQuota {
			continue
		}
		c.usage = usageToday
		rem := int64(math.MaxInt64)
		if dailyQuota > 0 {
			rem = int64(dailyQuota - usageToday)
		}
		if weeklyQuota > 0 {
			r := int64(weeklyQuota - usageWeek)
			if r < rem {
				rem = r
			}
		}
		if rem == math.MaxInt64 {
			rem = -1 // unlimited
		}
		c.remaining = rem
		out = append(out, c)
	}
	return out, nil
}

func (s *Scheduler) decryptCred(ctx context.Context, keyID int64) (providers.Credential, error) {
	var enc string
	if err := s.DB.QueryRowContext(ctx, `SELECT credential_enc FROM provider_keys WHERE id=?`, keyID).Scan(&enc); err != nil {
		return providers.Credential{}, err
	}
	raw, err := s.Box.Decrypt(enc)
	if err != nil {
		return providers.Credential{}, err
	}
	return providers.Credential{KeyID: keyID, Raw: raw}, nil
}

func (s *Scheduler) onAttemptSuccess(ctx context.Context, j *Job, c candidate, remoteID providers.RemoteJobID) {
	now := time.Now()
	_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='running', provider_id=?, key_id=?, remote_id=?, attempts=attempts+1, started_at=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		c.provider.ID(), c.keyID, string(remoteID), now, j.ID)
	var proxyID sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT bound_proxy_id FROM provider_keys WHERE id=?`, c.keyID).Scan(&proxyID)
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO job_attempts(job_id,provider_id,key_id,proxy_id,remote_id,status,started_at) VALUES(?,?,?,?,?,'running',?)`,
		j.ID, c.provider.ID(), c.keyID, proxyID, string(remoteID), now)
	_, _ = s.DB.ExecContext(ctx, `UPDATE provider_keys SET usage_today=usage_today+1, usage_week=usage_week+1, last_used_at=?, consecutive_errors=0 WHERE id=?`, now, c.keyID)
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO key_usage(key_id, job_id, day) VALUES(?,?,?)`, c.keyID, j.ID, time.Now().Format("2006-01-02"))
	s.audit(ctx, "scheduler", "job_submit", fmt.Sprintf("job_id=%d provider=%s key_id=%d proxy_id=%v remote_id=%s", j.ID, c.provider.ID(), c.keyID, proxyID, remoteID))
}

func (s *Scheduler) onAttemptFailure(ctx context.Context, j *Job, c candidate, cause error) {
	now := time.Now()
	var proxyID sql.NullInt64
	_ = s.DB.QueryRowContext(ctx, `SELECT bound_proxy_id FROM provider_keys WHERE id=?`, c.keyID).Scan(&proxyID)
	status := "failed"
	if e, ok := cause.(*providers.HTTPError); ok {
		if e.Status == 429 {
			status = "quota"
		}
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO job_attempts(job_id,provider_id,key_id,proxy_id,status,error,started_at,finished_at) VALUES(?,?,?,?,?,?,?,?)`,
		j.ID, c.provider.ID(), c.keyID, proxyID, status, truncate(cause.Error(), 1000), now, now)
	// Exponential backoff + auto-disable after repeated failures.
	var consec int
	_ = s.DB.QueryRowContext(ctx, `SELECT consecutive_errors FROM provider_keys WHERE id=?`, c.keyID).Scan(&consec)
	consec++
	cooldown := time.Duration(1<<min(consec, 6)) * time.Minute // 2,4,8,16,32,64 min
	_, _ = s.DB.ExecContext(ctx, `UPDATE provider_keys SET consecutive_errors=?, error_count=error_count+1, cooldown_until=? WHERE id=?`,
		consec, now.Add(cooldown), c.keyID)
	if consec >= 5 {
		_, _ = s.DB.ExecContext(ctx, `UPDATE provider_keys SET active=0 WHERE id=?`, c.keyID)
		s.log("critical", fmt.Sprintf("Key %q (id=%d, provider=%s) disabled after %d consecutive errors. Admin action required.", c.keyLabel, c.keyID, c.provider.ID(), consec))
		s.audit(ctx, "scheduler", "key_auto_disabled", fmt.Sprintf("key_id=%d provider=%s consecutive_errors=%d", c.keyID, c.provider.ID(), consec))
	}
	s.audit(ctx, "scheduler", "job_attempt_failed", fmt.Sprintf("job_id=%d provider=%s key_id=%d error=%s", j.ID, c.provider.ID(), c.keyID, truncate(cause.Error(), 300)))
}

func (s *Scheduler) pollRunning(ctx context.Context) {
	rows, err := s.DB.QueryContext(ctx, `SELECT id FROM jobs WHERE status='running' LIMIT 10`)
	if err != nil {
		return
	}
	var ids []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		ids = append(ids, id)
	}
	rows.Close()
	for _, id := range ids {
		_ = s.pollOne(ctx, id)
	}
}

func (s *Scheduler) pollOne(ctx context.Context, id int64) error {
	j, err := s.loadJob(ctx, id)
	if err != nil {
		return err
	}
	if !j.ProviderID.Valid {
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='queued', updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
		return nil
	}
	p := providers.Lookup(j.ProviderID.String)
	if p == nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='failed', fail_reason='provider missing' WHERE id=?`, id)
		return nil
	}
	// Timeout enforcement.
	if j.StartedAt.Valid && j.MaxDurationSeconds > 0 && time.Since(j.StartedAt.Time) > time.Duration(j.MaxDurationSeconds)*time.Second {
		cred, err := s.decryptCred(ctx, j.KeyID.Int64)
		if err == nil {
			_ = p.Cancel(ctx, cred, "", providers.RemoteJobID(j.RemoteID.String))
		}
		return s.finishOrFailover(ctx, j, p, "timeout", "job exceeded max_duration")
	}
	cred, err := s.decryptCred(ctx, j.KeyID.Int64)
	if err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='failed', fail_reason=? WHERE id=?`, "decrypt: "+err.Error(), id)
		return err
	}
	ctx2, cancel := context.WithTimeout(ctx, 30*time.Second)
	st, err := p.Status(ctx2, cred, "", providers.RemoteJobID(j.RemoteID.String))
	cancel()
	if err != nil {
		log.Printf("poll job %d: %v", id, err)
		return err
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET cost_usd_micros=? WHERE id=?`, st.Cost, id)
	// MaxCost enforcement.
	if j.MaxCostMicros > 0 && int64(st.Cost) > j.MaxCostMicros {
		_ = p.Cancel(ctx, cred, "", providers.RemoteJobID(j.RemoteID.String))
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='failed', fail_reason='max_cost_usd exceeded', finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=?`, id)
		s.audit(ctx, "scheduler", "job_max_cost_stop", fmt.Sprintf("job_id=%d cost=%d max=%d", id, st.Cost, j.MaxCostMicros))
		return nil
	}
	// Pull logs (best effort).
	if rc, err := p.Logs(ctx, cred, "", providers.RemoteJobID(j.RemoteID.String)); err == nil {
		if b, err := io.ReadAll(io.LimitReader(rc, 8<<20)); err == nil {
			_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET logs=? WHERE id=?`, string(b), id)
		}
		if cl, ok := rc.(io.Closer); ok {
			_ = cl.Close()
		}
	}
	switch normalizePoll(st.State) {
	case "succeeded":
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='succeeded', finished_at=CURRENT_TIMESTAMP, cost_usd_micros=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`, st.Cost, id)
		s.finishAttempt(ctx, id, "succeeded", "")
		s.audit(ctx, "scheduler", "job_succeeded", fmt.Sprintf("job_id=%d provider=%s cost=%s", id, p.ID(), st.Cost))
	case "failed", "timeout", "cancelled":
		return s.finishOrFailover(ctx, j, p, st.State, st.Message)
	default:
		// still running/queued
	}
	return nil
}

func normalizePoll(state string) string {
	switch strings.ToLower(state) {
	case "success", "completed", "done", "succeeded":
		return "succeeded"
	case "fail", "failed", "error", "crashed":
		return "failed"
	case "timeout", "timed_out":
		return "timeout"
	case "cancel", "cancelled", "canceled", "stopped":
		return "cancelled"
	default:
		return state
	}
}

// finishOrFailover: if retry budget remains, requeue for another provider; else mark terminal.
func (s *Scheduler) finishOrFailover(ctx context.Context, j *Job, p providers.Provider, failureState, msg string) error {
	s.finishAttempt(ctx, j.ID, failureState, msg)
	if j.Attempts < s.Cfg.MaxRetries && failureState != "cancelled" {
		_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status='queued', provider_id=NULL, key_id=NULL, remote_id=NULL, started_at=NULL, fail_reason=?, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
			fmt.Sprintf("failover after %s on %s: %s", failureState, p.ID(), truncate(msg, 300)), j.ID)
		s.audit(ctx, "scheduler", "job_failover", fmt.Sprintf("job_id=%d from_provider=%s reason=%s", j.ID, p.ID(), truncate(msg, 300)))
		return nil
	}
	terminal := failureState
	if terminal == "succeeded" {
		terminal = "failed"
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE jobs SET status=?, fail_reason=?, finished_at=CURRENT_TIMESTAMP, updated_at=CURRENT_TIMESTAMP WHERE id=?`,
		terminal, truncate(msg, 1000), j.ID)
	s.audit(ctx, "scheduler", "job_failed_terminal", fmt.Sprintf("job_id=%d provider=%s state=%s msg=%s", j.ID, p.ID(), failureState, truncate(msg, 300)))
	return nil
}

func (s *Scheduler) finishAttempt(ctx context.Context, jobID int64, status, errMsg string) {
	_, _ = s.DB.ExecContext(ctx, `UPDATE job_attempts SET status=?, error=?, finished_at=CURRENT_TIMESTAMP WHERE job_id=? AND status='running'`, status, truncate(errMsg, 1000), jobID)
}

func (s *Scheduler) audit(ctx context.Context, actor, action, detail string) {
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO audit_log(actor, action, detail) VALUES(?,?,?)`, actor, action, detail)
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
