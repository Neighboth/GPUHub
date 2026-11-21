/**
 * GPUHub API client.
 * Base URL: /api/v1
 * Tokens in localStorage: gpuhub_access / gpuhub_refresh
 * On 401: attempts a single POST /auth/refresh, then retries once.
 * If refresh fails, tokens are cleared and the app is redirected to /login.
 */

const BASE = '/api/v1';
const ACCESS_KEY = 'gpuhub_access';
const REFRESH_KEY = 'gpuhub_refresh';
const USER_KEY = 'gpuhub_user';

export function getAccessToken(): string | null {
  return localStorage.getItem(ACCESS_KEY);
}
export function getRefreshToken(): string | null {
  return localStorage.getItem(REFRESH_KEY);
}
export function getStoredUser(): StoredUser | null {
  const raw = localStorage.getItem(USER_KEY);
  if (!raw) return null;
  try {
    return JSON.parse(raw) as StoredUser;
  } catch {
    return null;
  }
}
export function setTokens(access: string, refresh?: string | null): void {
  localStorage.setItem(ACCESS_KEY, access);
  if (refresh) localStorage.setItem(REFRESH_KEY, refresh);
}
export function setStoredUser(user: StoredUser | null): void {
  if (user) localStorage.setItem(USER_KEY, JSON.stringify(user));
  else localStorage.removeItem(USER_KEY);
}
export function clearTokens(): void {
  localStorage.removeItem(ACCESS_KEY);
  localStorage.removeItem(REFRESH_KEY);
  localStorage.removeItem(USER_KEY);
}

export interface StoredUser {
  username?: string;
  role?: string;
  [k: string]: unknown;
}

export class ApiError extends Error {
  status: number;
  body: unknown;
  constructor(status: number, body: unknown, message?: string) {
    super(message || `Request failed with status ${status}`);
    this.status = status;
    this.body = body;
    this.name = 'ApiError';
  }
}

/** Pull a useful human message out of an unknown error/response body. */
export function errorMessage(e: unknown, fallback = 'Something went wrong'): string {
  if (!e) return fallback;
  if (e instanceof ApiError) {
    const b = e.body as Record<string, unknown> | string | null;
    if (typeof b === 'string' && b.trim()) return b;
    if (b && typeof b === 'object') {
      const m = b.error ?? b.message ?? b.detail ?? b.reason;
      if (typeof m === 'string' && m.trim()) return m;
      try {
        return JSON.stringify(b);
      } catch {
        /* ignore */
      }
    }
    return e.message || fallback;
  }
  if (e instanceof Error && e.message) return e.message;
  return fallback;
}

type Json = Record<string, unknown>;

async function doRefresh(): Promise<string | null> {
  const refresh = getRefreshToken();
  if (!refresh) return null;
  try {
    const res = await fetch(`${BASE}/auth/refresh`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Requested-With': 'XMLHttpRequest' },
      body: JSON.stringify({ refresh_token: refresh }),
    });
    if (!res.ok) return null;
    const data = (await res.json()) as Json;
    const access =
      (data.access_token as string) ?? (data.accessToken as string) ?? (data.token as string) ?? null;
    if (!access) return null;
    const newRefresh = (data.refresh_token as string) ?? (data.refreshToken as string) ?? refresh;
    setTokens(access, newRefresh);
    return access;
  } catch {
    return null;
  }
}

let refreshInflight: Promise<string | null> | null = null;

interface RequestOptions {
  method?: string;
  body?: unknown;
  /** Skip the 401 -> refresh -> retry flow (used by auth endpoints themselves). */
  noRefreshRetry?: boolean;
}

async function request<T = unknown>(path: string, opts: RequestOptions = {}): Promise<T> {
  const { method = 'GET', body, noRefreshRetry = false } = opts;
  const headers: Record<string, string> = { 'X-Requested-With': 'XMLHttpRequest' };
  if (body !== undefined) headers['Content-Type'] = 'application/json';
  const token = getAccessToken();
  if (token) headers['Authorization'] = `Bearer ${token}`;

  const res = await fetch(`${BASE}${path}`, {
    method,
    headers,
    body: body !== undefined ? JSON.stringify(body) : undefined,
  });

  if (res.status === 401 && !noRefreshRetry) {
    if (!refreshInflight) refreshInflight = doRefresh();
    const newToken = await refreshInflight;
    refreshInflight = null;
    if (newToken) {
      return request<T>(path, { ...opts, noRefreshRetry: true });
    }
    clearTokens();
    if (typeof window !== 'undefined' && window.location.pathname !== '/login') {
      window.location.href = '/login';
    }
    throw new ApiError(401, null, 'Session expired - please sign in again');
  }

  const text = await res.text();
  let data: unknown = null;
  if (text) {
    try {
      data = JSON.parse(text);
    } catch {
      data = text;
    }
  }
  if (!res.ok) {
    throw new ApiError(res.status, data);
  }
  return data as T;
}

/** Extract a list from a response that may be an array or a wrapped object. */
function unwrapList<T = Json>(data: unknown, keys: string[]): T[] {
  if (Array.isArray(data)) return data as T[];
  if (data && typeof data === 'object') {
    const obj = data as Json;
    for (const k of keys) {
      if (Array.isArray(obj[k])) return obj[k] as T[];
    }
  }
  return [];
}

/* -------------------------------------------------------------------------- */
/* Types                                                                       */
/* -------------------------------------------------------------------------- */

export interface AuthStatus {
  needs_setup: boolean;
}

export interface DashboardSummary {
  running: number;
  queued: number;
  succeeded_24h: number;
  failed_24h: number;
  cost_today_usd_micros: number;
  cost_series: { date: string; usd_micros: number }[];
  providers: { id: string; name: string; quota_percent: number }[];
  recent_errors: { ts: string; message: string }[];
}

export interface Provider {
  id: string;
  name: string;
  description?: string;
  gpu_types?: string[];
  capabilities?: string[];
  key_count?: number;
  keys_count?: number;
  enabled?: boolean;
  default_enabled?: boolean;
}

export interface AddingKeyBody {
  label?: string;
  credential: string;
  bound_proxy_id?: number | null;
  daily_quota?: number | null;
  weekly_quota?: number | null;
  priority?: number;
}

export interface ProxyItem {
  id: string;
  label: string;
  url?: string;
  url_masked?: string;
  country?: string;
  health?: string;
  health_status?: string;
  status?: string;
  last_check_at?: string | null;
  check_error?: string;
  bound_key?: { id?: string; label?: string; provider?: string; provider_name?: string } | null;
  bound_keys?: { id?: string; label?: string; provider?: string; provider_name?: string }[];
  bound_key_id?: string | null;
  bound_key_label?: string | null;
  bound_provider_id?: string | null;
}

export type ProxyHealth = 'healthy' | 'unhealthy' | 'unknown';

export interface JobAttempt {
  provider?: string;
  provider_name?: string;
  key_label?: string;
  key?: string;
  proxy_label?: string;
  proxy?: string;
  status?: string;
  error?: string | null;
  started_at?: string | null;
  started?: string | null;
  created_at?: string | null;
}

export interface Job {
  id: string;
  type: string;
  gpu_type?: string;
  min_vram_gb?: number;
  gpu_count?: number;
  max_duration_seconds?: number | null;
  provider_preference?: string[];
  max_cost_usd?: number | null;
  max_cost_usd_micros?: number | null;
  status: string;
  provider?: string | { id?: string; name?: string } | null;
  provider_name?: string | null;
  created_at?: string;
  createdAt?: string;
  cost_usd_micros?: number;
  cost_usd?: number;
  attempts?: JobAttempt[];
  rejections?: Record<string, string | string[]> | null;
  logs?: string | null;
}

export interface CreateJobBody {
  type: string;
  payload: string;
  gpu_type: string;
  gpu_type_fallback?: string[];
  min_vram_gb: number;
  gpu_count: number;
  max_duration_seconds?: number | null;
  provider_preference?: string[];
  max_cost_usd?: number | null;
}

export interface ApiKey {
  id: string;
  label: string;
  provider?: string;
  provider_name?: string;
  provider_id?: string;
  masked_credential?: string;
  credential_masked?: string;
  credential?: string;
  bound_proxy?: { id?: string; label?: string } | null;
  bound_proxy_label?: string | null;
  proxy_label?: string | null;
  proxy_id?: string | null;
  priority?: number;
  daily_quota?: number | null;
  weekly_quota?: number | null;
  usage_today?: number | null;
  usage_week?: number | null;
  last_used_at?: string | null;
  error_count?: number | null;
  errors?: number | null;
  cooldown_until?: string | null;
  active?: boolean;
}

export interface Settings {
  rotation_strategy?: string;
  scheduler_strategy?: string;
  quota_high_watermark_percent?: number;
  max_retries?: number;
  default_max_cost_usd?: number | null;
  max_cost_usd?: number | null;
  default_max_cost_usd_micros?: number | null;
  notifications_webhook_url?: string | null;
  webhook_url?: string | null;
  [k: string]: unknown;
}

export interface AuditEntry {
  id?: string;
  ts?: string;
  time?: string;
  created_at?: string;
  actor?: string;
  user?: string;
  action?: string;
  detail?: string;
  message?: string;
}

/* -------------------------------------------------------------------------- */
/* Auth                                                                        */
/* -------------------------------------------------------------------------- */

async function authStatus(): Promise<AuthStatus> {
  return request<AuthStatus>('/auth/status', { noRefreshRetry: true });
}

async function setup(body: { username: string; password: string; confirm: string }): Promise<unknown> {
  return request('/auth/setup', { method: 'POST', body, noRefreshRetry: true });
}

async function login(username: string, password: string): Promise<{ user: StoredUser | null }> {
  const data = await request<Record<string, unknown>>('/auth/login', {
    method: 'POST',
    body: { username, password },
    noRefreshRetry: true,
  });
  const access =
    (data.access_token as string) ?? (data.accessToken as string) ?? (data.token as string) ?? null;
  if (!access) {
    throw new ApiError(200, data, 'Login succeeded but no access token was returned');
  }
  const refresh = (data.refresh_token as string) ?? (data.refreshToken as string) ?? null;
  setTokens(access, refresh);
  const user =
    (data.user as StoredUser) ??
    (data.username ? { username: data.username as string, role: data.role as string | undefined } : null) ??
    { username };
  setStoredUser(user ?? { username });
  return { user };
}

function logout(): void {
  clearTokens();
}

/* -------------------------------------------------------------------------- */
/* Dashboard                                                                   */
/* -------------------------------------------------------------------------- */

async function dashboardSummary(): Promise<DashboardSummary> {
  const d = await request<Record<string, unknown>>('/dashboard/summary');
  return {
    running: Number(d.running ?? 0),
    queued: Number(d.queued ?? 0),
    succeeded_24h: Number(d.succeeded_24h ?? 0),
    failed_24h: Number(d.failed_24h ?? 0),
    cost_today_usd_micros: Number(d.cost_today_usd_micros ?? 0),
    cost_series: unwrapList<{ date: string; usd_micros: number }>(d.cost_series, []),
    providers: unwrapList<{ id: string; name: string; quota_percent: number }>(d.providers, []),
    recent_errors: unwrapList<{ ts: string; message: string }>(d.recent_errors, []),
  };
}

/* -------------------------------------------------------------------------- */
/* Providers                                                                   */
/* -------------------------------------------------------------------------- */

async function providers(): Promise<Provider[]> {
  return unwrapList<Provider>(await request('/providers'), ['providers', 'items', 'data']);
}

async function updateProvider(id: string, enabled: boolean): Promise<unknown> {
  return request(`/providers/${encodeURIComponent(id)}`, { method: 'PATCH', body: { enabled } });
}

async function addProviderKey(providerId: string, body: AddingKeyBody): Promise<unknown> {
  return request(`/providers/${encodeURIComponent(providerId)}/keys`, { method: 'POST', body });
}

async function revealKey(keyId: string | number): Promise<{ credential: string }> {
  return request(`/keys/${encodeURIComponent(String(keyId))}/reveal`, { method: 'POST', body: {} }) as Promise<{ credential: string }>;
}

async function testProvider(id: string, keyId?: string): Promise<unknown> {
  return request(`/providers/${encodeURIComponent(id)}/test`, {
    method: 'POST',
    body: keyId ? { key_id: keyId } : {},
  });
}

async function providerQuota(id: string): Promise<unknown> {
  return request(`/providers/${encodeURIComponent(id)}/quota`);
}

/* -------------------------------------------------------------------------- */
/* Proxies                                                                     */
/* -------------------------------------------------------------------------- */

export function proxyLabelHealth(p: ProxyItem): ProxyHealth {
  const h = (p.health ?? p.health_status ?? p.status ?? 'unknown').toLowerCase();
  if (h.includes('unhealthy') || h.includes('fail') || h.includes('down') || h.includes('error')) return 'unhealthy';
  if (h.includes('healthy') || h.includes('ok') || h.includes('up')) return 'healthy';
  return 'unknown';
}

async function proxies(): Promise<ProxyItem[]> {
  return unwrapList<ProxyItem>(await request('/proxies'), ['proxies', 'items', 'data']);
}

async function createProxy(body: { label: string; url: string; country?: string }): Promise<unknown> {
  return request('/proxies', { method: 'POST', body });
}

async function testProxy(id: string): Promise<unknown> {
  return request(`/proxies/${encodeURIComponent(id)}/test`, { method: 'POST', body: {} });
}

async function deleteProxy(id: string): Promise<unknown> {
  return request(`/proxies/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/** Detect "N bound keys were disabled" signals in a delete-proxy response. */
export function disabledKeysSignal(res: unknown): number | null {
  if (!res || typeof res !== 'object') return null;
  const o = res as Json;
  const candidates = [
    o.disabled_keys,
    o.disabledKeys,
    o.keys_disabled,
    o.disabled_keys_count,
    o.affected_keys,
    o.disabled,
  ];
  for (const c of candidates) {
    if (Array.isArray(c) && c.length > 0) return c.length;
    if (typeof c === 'number' && c > 0) return c;
    if (typeof c === 'boolean' && c) return -1; // unknown count, but happened
  }
  if (typeof o.warning === 'string' && /disab/i.test(o.warning)) return -1;
  if (typeof o.message === 'string' && /disab/i.test(o.message)) return -1;
  return null;
}

/* -------------------------------------------------------------------------- */
/* Jobs                                                                        */
/* -------------------------------------------------------------------------- */

async function jobs(status?: string, limit?: number): Promise<Job[]> {
  const params = new URLSearchParams();
  if (status && status !== 'all') params.set('status', status);
  if (limit && limit > 0) params.set('limit', String(limit));
  const q = params.toString();
  return unwrapList<Job>(await request(`/jobs${q ? `?${q}` : ''}`), ['jobs', 'items', 'data']);
}

async function createJob(body: CreateJobBody): Promise<Job> {
  return request<Job>('/jobs', { method: 'POST', body });
}

async function job(id: string): Promise<Job> {
  const d = await request<Record<string, unknown>>(`/jobs/${encodeURIComponent(id)}`);
  // Backend wraps the job as { job: {...}, attempts: [...] }
  if (d.job && typeof d.job === 'object' && !Array.isArray(d.job)) {
    const jobObj = d.job as Job;
    // job.attempts inside the wrapper is an integer count; the array lives at d.attempts.
    if (Array.isArray(d.attempts)) {
      jobObj.attempts = d.attempts as Job['attempts'];
    } else if (!Array.isArray(jobObj.attempts)) {
      jobObj.attempts = [];
    }
    return jobObj;
  }
  return d as unknown as Job;
}

async function cancelJob(id: string): Promise<unknown> {
  return request(`/jobs/${encodeURIComponent(id)}/cancel`, { method: 'POST', body: {} });
}

/* -------------------------------------------------------------------------- */
/* Keys                                                                        */
/* -------------------------------------------------------------------------- */

async function keys(): Promise<ApiKey[]> {
  return unwrapList<ApiKey>(await request('/keys'), ['keys', 'items', 'data']);
}

async function updateKey(id: string, active: boolean): Promise<unknown> {
  return request(`/keys/${encodeURIComponent(id)}`, { method: 'PATCH', body: { active } });
}

async function testKey(id: string): Promise<unknown> {
  return request(`/keys/${encodeURIComponent(id)}/test`, { method: 'POST', body: {} });
}

async function reassignKeyProxy(id: string, proxyId: string): Promise<unknown> {
  const pid = Number(proxyId);
  return request(`/keys/${encodeURIComponent(id)}/proxy`, {
    method: 'PATCH',
    body: { proxy_id: Number.isFinite(pid) ? pid : 0 },
  });
}

async function deleteKey(id: string): Promise<unknown> {
  return request(`/keys/${encodeURIComponent(id)}`, { method: 'DELETE' });
}

/* -------------------------------------------------------------------------- */
/* Settings                                                                    */
/* -------------------------------------------------------------------------- */

async function settings(): Promise<Settings> {
  const d = await request<Record<string, unknown>>('/settings');
  if (d.settings && typeof d.settings === 'object' && !Array.isArray(d.settings)) {
    return d.settings as Settings;
  }
  return d as Settings;
}

async function updateSettings(body: Partial<Settings>): Promise<unknown> {
  return request('/settings', { method: 'PATCH', body });
}

async function rotateMasterKey(currentMasterKey: string, newMasterKey: string): Promise<unknown> {
  return request('/settings/rotate-master-key', {
    method: 'POST',
    body: { current_master_key: currentMasterKey, new_master_key: newMasterKey },
  });
}

async function testWebhook(url?: string): Promise<unknown> {
  return request('/settings/test-webhook', {
    method: 'POST',
    body: url ? { url } : {},
  });
}

/* -------------------------------------------------------------------------- */
/* Audit                                                                       */
/* -------------------------------------------------------------------------- */

async function audit(limit = 200): Promise<AuditEntry[]> {
  return unwrapList<AuditEntry>(await request(`/audit?limit=${limit}`), [
    'entries',
    'audit',
    'items',
    'logs',
    'events',
    'data',
  ]);
}

/* -------------------------------------------------------------------------- */
/* Misc helpers                                                                */
/* -------------------------------------------------------------------------- */

/** Mask userinfo in a proxy URL: socks5://user:pass@host -> socks5://user:****@host */
export function maskProxyUrl(url?: string | null): string {
  if (!url) return '-';
  try {
    return url.replace(/(\/\/[^/\s:@]+:)[^@\s/]+(@)/, '$1****$2');
  } catch {
    return url;
  }
}

export function jobProviderName(j: Job): string {
  if (typeof j.provider === 'string') return j.provider;
  if (j.provider && typeof j.provider === 'object') return j.provider.name ?? j.provider.id ?? '-';
  return j.provider_name ?? '-';
}

export function jobCreatedAt(j: Job): string | undefined {
  return j.created_at ?? j.createdAt;
}

export function jobCostMicros(j: Job): number | null {
  if (typeof j.cost_usd_micros === 'number') return j.cost_usd_micros;
  if (typeof j.cost_usd === 'number') return Math.round(j.cost_usd * 1e6);
  return null;
}

export function attemptStartedAt(a: JobAttempt): string | undefined {
  return a.started_at ?? a.started ?? a.created_at ?? undefined;
}

export function attemptProxyLabel(a: JobAttempt): string {
  return a.proxy_label ?? a.proxy ?? '-';
}

export function attemptKeyLabel(a: JobAttempt): string {
  return a.key_label ?? a.key ?? '-';
}

export function auditTime(a: AuditEntry): string | undefined {
  return a.ts ?? a.time ?? a.created_at;
}

export function keyProxyLabel(k: ApiKey): string | null {
  if (k.bound_proxy && typeof k.bound_proxy === 'object') return k.bound_proxy.label ?? null;
  return k.bound_proxy_label ?? k.proxy_label ?? null;
}

export function keyMaskedCredential(k: ApiKey): string {
  return k.masked_credential ?? k.credential_masked ?? k.credential ?? '••••••••';
}

export function keyErrors(k: ApiKey): number {
  return k.error_count ?? k.errors ?? 0;
}

export function providerKeyCount(p: Provider): number {
  return p.key_count ?? p.keys_count ?? 0;
}

export function providerCapabilities(p: Provider): string[] {
  const caps = (p as unknown as Record<string, unknown>).gpu_types ?? p.capabilities ?? [];
  if (Array.isArray(caps)) {
    // Either string[] or GPU objects [{type, vram_gb...}]
    return caps.map((c) => (typeof c === 'string' ? c : ((c as { type?: string })?.type ?? JSON.stringify(c))));
  }
  if (caps && typeof caps === 'object') {
    const gpus = (caps as { gpus?: Array<{ type?: string }> }).gpus;
    if (Array.isArray(gpus)) return gpus.map((g) => g?.type ?? 'unknown').filter(Boolean);
  }
  return [];
}

/** Render an unknown test-result payload as a short human string. */
export function resultSummary(res: unknown): string {
  if (res == null) return 'OK';
  if (typeof res === 'string') return res.slice(0, 200);
  if (typeof res === 'object') {
    const o = res as Record<string, unknown>;
    const msg = o.message ?? o.detail ?? o.status ?? o.result ?? o.error;
    if (typeof msg === 'string' && msg.trim()) return msg.slice(0, 300);
    if (typeof o.ok === 'boolean' || typeof o.success === 'boolean') {
      return (o.ok ?? o.success) ? 'OK' : 'Failed';
    }
    try {
      const s = JSON.stringify(o);
      return s.length > 300 ? s.slice(0, 300) + '…' : s;
    } catch {
      return 'OK';
    }
  }
  return String(res);
}

export const api = {
  authStatus,
  setup,
  login,
  logout,
  dashboardSummary,
  providers,
  updateProvider,
  addProviderKey,
  revealKey,
  testProvider,
  providerQuota,
  proxies,
  createProxy,
  testProxy,
  deleteProxy,
  jobs,
  createJob,
  job,
  cancelJob,
  keys,
  updateKey,
  testKey,
  reassignKeyProxy,
  deleteKey,
  settings,
  updateSettings,
  rotateMasterKey,
  testWebhook,
  audit,
};
