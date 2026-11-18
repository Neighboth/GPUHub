import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react';
import {
  Server,
  KeyRound,
  FlaskConical,
  Gauge,
  Plus,
  RefreshCw,
  Layers,
} from 'lucide-react';
import {
  api,
  errorMessage,
  resultSummary,
  providerKeyCount,
  providerCapabilities,
  type Provider,
  type ProxyItem,
} from '../api';
import {
  Button,
  Card,
  CardContent,
  Badge,
  Modal,
  Input,
  Label,
  Select,
  Textarea,
  Toggle,
  LoadingBlock,
  ErrorBlock,
  EmptyState,
  FieldError,
} from '../components/ui';
import { useToast } from '../components/Toast';

interface QuotaRow {
  [k: string]: unknown;
}

export default function Providers() {
  const toast = useToast();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [proxies, setProxies] = useState<ProxyItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  // Add-key modal state
  const [keyModalFor, setKeyModalFor] = useState<Provider | null>(null);
  const [label, setLabel] = useState('');
  const [credential, setCredential] = useState('');
  const [boundProxyId, setBoundProxyId] = useState('');
  const [dailyQuota, setDailyQuota] = useState('');
  const [weeklyQuota, setWeeklyQuota] = useState('');
  const [priority, setPriority] = useState('100');
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');

  // Quota modal state
  const [quotaFor, setQuotaFor] = useState<Provider | null>(null);
  const [quotaData, setQuotaData] = useState<unknown>(null);
  const [quotaLoading, setQuotaLoading] = useState(false);
  const [quotaError, setQuotaError] = useState('');

  // Per-provider test spinner
  const [testingId, setTestingId] = useState<string | null>(null);
  const [toggleBusy, setToggleBusy] = useState<string | null>(null);

  const load = useCallback((spin = true) => {
    if (spin) setLoading(true);
    else setRefreshing(true);
    setError('');
    Promise.all([api.providers(), api.proxies().catch(() => [] as ProxyItem[])])
      .then(([p, px]) => {
        setProviders(p);
        setProxies(px);
      })
      .catch((e) => setError(errorMessage(e, 'Failed to load providers')))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  function openKeyModal(p: Provider) {
    setKeyModalFor(p);
    setLabel('');
    setCredential('');
    setBoundProxyId('');
    setDailyQuota('');
    setWeeklyQuota('');
    setPriority('100');
    setFormError('');
  }

  async function submitAddKey(e: FormEvent) {
    e.preventDefault();
    if (!keyModalFor) return;
    if (!label.trim()) return setFormError('Label is required.');
    if (!credential.trim()) return setFormError('Credential is required.');
    setSubmitting(true);
    setFormError('');
    try {
      await api.addProviderKey(keyModalFor.id, {
        label: label.trim(),
        credential: credential.trim(),
        bound_proxy_id: boundProxyId || null,
        daily_quota: dailyQuota.trim() === '' ? null : Number(dailyQuota),
        weekly_quota: weeklyQuota.trim() === '' ? null : Number(weeklyQuota),
        priority: priority.trim() === '' ? 100 : Number(priority),
      });
      toast(`Key “${label.trim()}” added to ${keyModalFor.name}`, 'success');
      setKeyModalFor(null);
      load(false);
    } catch (err) {
      setFormError(errorMessage(err, 'Failed to add key'));
    } finally {
      setSubmitting(false);
    }
  }

  async function onToggle(p: Provider, next: boolean) {
    setToggleBusy(p.id);
    // Optimistic update
    setProviders((prev) => prev.map((x) => (x.id === p.id ? { ...x, enabled: next } : x)));
    try {
      await api.updateProvider(p.id, next);
      toast(`${p.name} ${next ? 'enabled' : 'disabled'}`, 'success');
    } catch (e) {
      setProviders((prev) => prev.map((x) => (x.id === p.id ? { ...x, enabled: !next } : x)));
      toast(errorMessage(e, 'Failed to update provider'), 'error');
    } finally {
      setToggleBusy(null);
    }
  }

  async function onTest(p: Provider) {
    setTestingId(p.id);
    try {
      const res = await api.testProvider(p.id);
      toast(`${p.name}: ${resultSummary(res)}`, 'success');
    } catch (e) {
      toast(`${p.name} test failed: ${errorMessage(e)}`, 'error');
    } finally {
      setTestingId(null);
    }
  }

  async function openQuota(p: Provider) {
    setQuotaFor(p);
    setQuotaData(null);
    setQuotaError('');
    setQuotaLoading(true);
    try {
      setQuotaData(await api.providerQuota(p.id));
    } catch (e) {
      setQuotaError(errorMessage(e, 'Failed to load quota'));
    } finally {
      setQuotaLoading(false);
    }
  }

  const quotaRows = useMemo(() => normalizeQuota(quotaData), [quotaData]);

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">Providers</h1>
          <p className="text-xs text-zinc-500">Free-tier GPU compute providers and their credential health.</p>
        </div>
        <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
          <RefreshCw size={14} />
          Refresh
        </Button>
      </div>

      {loading ? (
        <LoadingBlock label="Loading providers…" />
      ) : error ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : providers.length === 0 ? (
        <EmptyState
          icon={Server}
          title="No providers configured"
          subtitle="Providers are registered by the GPUHub server. Check your server configuration."
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 md:grid-cols-2 2xl:grid-cols-3">
          {providers.map((p) => {
            const caps = providerCapabilities(p);
            const enabled = p.enabled ?? true;
            const keyCount = providerKeyCount(p);
            return (
              <Card key={p.id} className="flex flex-col">
                <div className="flex items-start justify-between gap-3 border-b border-zinc-800/80 px-5 py-4">
                  <div className="flex items-center gap-3">
                    <div className="flex h-10 w-10 items-center justify-center rounded-lg border border-zinc-800 bg-zinc-900">
                      <Layers size={18} className="text-emerald-400" strokeWidth={1.8} />
                    </div>
                    <div className="min-w-0">
                      <h3 className="truncate text-sm font-semibold text-zinc-100">{p.name}</h3>
                      <code className="text-[11px] text-zinc-500">{p.id}</code>
                    </div>
                  </div>
                  <Toggle
                    checked={enabled}
                    onChange={(next) => onToggle(p, next)}
                    disabled={toggleBusy === p.id}
                    label={`Toggle ${p.name}`}
                  />
                </div>

                <CardContent className="flex flex-1 flex-col gap-4">
                  {/* Capabilities */}
                  <div>
                    <div className="label-base">GPU capabilities</div>
                    <div className="flex flex-wrap gap-1.5">
                      {caps.length > 0 ? (
                        caps.map((c) => (
                          <Badge key={c} variant="info">
                            {c}
                          </Badge>
                        ))
                      ) : (
                        <span className="text-xs text-zinc-600">No capability info reported</span>
                      )}
                    </div>
                  </div>

                  {/* Key count */}
                  <div className="flex items-center justify-between rounded-lg border border-zinc-800 bg-zinc-900/50 px-3 py-2">
                    <span className="flex items-center gap-2 text-xs text-zinc-400">
                      <KeyRound size={13} className="text-zinc-500" /> API keys
                    </span>
                    <span className="text-sm font-bold tabular-nums text-zinc-100">{keyCount}</span>
                  </div>

                  {/* Actions */}
                  <div className="mt-auto flex flex-wrap items-center gap-2 pt-1">
                    <Button size="sm" variant="primary" onClick={() => openKeyModal(p)}>
                      <Plus size={13} /> Add key
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => onTest(p)} loading={testingId === p.id}>
                      <FlaskConical size={13} /> Test
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => openQuota(p)}>
                      <Gauge size={13} /> Quota
                    </Button>
                  </div>
                </CardContent>
              </Card>
            );
          })}
        </div>
      )}

      {/* Add key modal */}
      <Modal
        open={!!keyModalFor}
        onClose={() => setKeyModalFor(null)}
        title={keyModalFor ? `Add key — ${keyModalFor.name}` : 'Add key'}
        wide
        footer={
          <>
            <Button variant="ghost" onClick={() => setKeyModalFor(null)}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" form="add-key-form" loading={submitting}>
              Add key
            </Button>
          </>
        }
      >
        <form id="add-key-form" onSubmit={submitAddKey} className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <Label htmlFor="key-label">Label *</Label>
              <Input
                id="key-label"
                placeholder="e.g. kaggle-prod-1"
                value={label}
                onChange={(e) => setLabel(e.target.value)}
                disabled={submitting}
              />
            </div>
            <div>
              <Label htmlFor="bound-proxy">Bound proxy</Label>
              <Select id="bound-proxy" value={boundProxyId} onChange={(e) => setBoundProxyId(e.target.value)} disabled={submitting}>
                <option value="">No proxy (direct)</option>
                {proxies.map((px) => (
                  <option key={px.id} value={px.id}>
                    {px.label} {px.country ? `· ${px.country}` : ''}
                  </option>
                ))}
              </Select>
              <p className="mt-1.5 text-[11px] text-zinc-500">Keys without a proxy stay disabled until one is bound.</p>
            </div>
          </div>

          <div>
            <Label htmlFor="credential">Credential *</Label>
            <Textarea
              id="credential"
              placeholder="Paste the API token / service account JSON / key material…"
              value={credential}
              onChange={(e) => setCredential(e.target.value)}
              disabled={submitting}
              className="min-h-[110px]"
            />
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
            <div>
              <Label htmlFor="daily-quota">Daily quota</Label>
              <Input
                id="daily-quota"
                type="number"
                min={0}
                placeholder="e.g. 30"
                value={dailyQuota}
                onChange={(e) => setDailyQuota(e.target.value)}
                disabled={submitting}
              />
            </div>
            <div>
              <Label htmlFor="weekly-quota">Weekly quota</Label>
              <Input
                id="weekly-quota"
                type="number"
                min={0}
                placeholder="e.g. 200"
                value={weeklyQuota}
                onChange={(e) => setWeeklyQuota(e.target.value)}
                disabled={submitting}
              />
            </div>
            <div>
              <Label htmlFor="priority">Priority</Label>
              <Input
                id="priority"
                type="number"
                min={0}
                placeholder="100"
                value={priority}
                onChange={(e) => setPriority(e.target.value)}
                disabled={submitting}
              />
              <p className="mt-1.5 text-[11px] text-zinc-500">Lower = preferred</p>
            </div>
          </div>

          <FieldError>{formError}</FieldError>
        </form>
      </Modal>

      {/* Quota modal */}
      <Modal
        open={!!quotaFor}
        onClose={() => setQuotaFor(null)}
        title={quotaFor ? `Quota — ${quotaFor.name}` : 'Quota'}
        wide
        footer={
          <Button variant="ghost" onClick={() => setQuotaFor(null)}>
            Close
          </Button>
        }
      >
        {quotaLoading ? (
          <LoadingBlock label="Fetching quota…" />
        ) : quotaError ? (
          <div className="rounded-lg border border-red-500/25 bg-red-500/5 px-3 py-2 text-sm text-red-300">
            {quotaError}
          </div>
        ) : quotaRows.columns.length === 0 ? (
          <pre className="max-h-80 overflow-auto rounded-lg border border-zinc-800 bg-zinc-950 p-3 text-[11px] leading-relaxed text-zinc-300">
            {JSON.stringify(quotaData, null, 2) ?? 'No quota data.'}
          </pre>
        ) : (
          <div className="overflow-x-auto rounded-lg border border-zinc-800">
            <table className="min-w-full divide-y divide-zinc-800 text-xs">
              <thead className="bg-zinc-900/80">
                <tr>
                  {quotaRows.columns.map((c) => (
                    <th key={c} className="th-base">{c}</th>
                  ))}
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-800/70">
                {quotaRows.rows.map((row, i) => (
                  <tr key={i} className="hover:bg-zinc-800/30">
                    {quotaRows.columns.map((c) => (
                      <td key={c} className="td-base font-mono text-[11px]">
                        {renderCell(row[c])}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Modal>
    </div>
  );
}

/** Normalize arbitrary quota payloads into a table. */
function normalizeQuota(data: unknown): { columns: string[]; rows: QuotaRow[] } {
  if (!data) return { columns: [], rows: [] };

  // Find the first array payload (handles {keys:[...]}, {quotas:[...]}, etc.)
  let arr: unknown[] | null = null;
  if (Array.isArray(data)) {
    arr = data;
  } else if (typeof data === 'object') {
    const obj = data as Record<string, unknown>;
    for (const k of ['keys', 'quotas', 'quota', 'items', 'usage', 'data', 'rows']) {
      if (Array.isArray(obj[k])) {
        arr = obj[k] as unknown[];
        break;
      }
    }
    // Single-key object: { "keyId": {...}, ... }
    if (!arr) {
      const entries = Object.entries(obj);
      if (entries.length > 0 && entries.every(([, v]) => v && typeof v === 'object' && !Array.isArray(v))) {
        return {
          columns: ['key', ...new Set(entries.flatMap(([, v]) => Object.keys(v as Record<string, unknown>)))],
          rows: entries.map(([k, v]) => ({ key: k, ...(v as QuotaRow) })),
        };
      }
      return { columns: [], rows: [] };
    }
  } else {
    return { columns: [], rows: [] };
  }

  if (arr.length === 0) return { columns: ['info'], rows: [{ info: 'No per-key quota data' }] };

  const columns: string[] = [];
  for (const item of arr) {
    if (item && typeof item === 'object' && !Array.isArray(item)) {
      for (const k of Object.keys(item as Record<string, unknown>)) {
        if (!columns.includes(k)) columns.push(k);
      }
    } else {
      if (!columns.includes('value')) columns.push('value');
    }
  }
  const rows = arr.map((item) => {
    if (item && typeof item === 'object' && !Array.isArray(item)) return item as QuotaRow;
    return { value: item } as QuotaRow;
  });
  return { columns, rows };
}

function renderCell(v: unknown): string {
  if (v === null || v === undefined) return '—';
  if (typeof v === 'object') {
    try {
      return JSON.stringify(v);
    } catch {
      return String(v);
    }
  }
  return String(v);
}
