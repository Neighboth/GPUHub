import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  KeyRound,
  RefreshCw,
  FlaskConical,
  Trash2,
  ArrowRightLeft,
  RotateCw,
  ChevronRight,
  AlertTriangle,
} from 'lucide-react';
import {
  api,
  errorMessage,
  resultSummary,
  getStoredUser,
  keyProxyLabel,
  keyMaskedCredential,
  keyErrors,
  type ApiKey,
  type ProxyItem,
  type Settings,
} from '../api';
import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  Badge,
  Modal,
  Select,
  Label,
  Toggle,
  Meter,
  Table,
  THead,
  TBody,
  Tr,
  Th,
  Td,
  LoadingBlock,
  ErrorBlock,
  EmptyState,
} from '../components/ui';
import { useToast } from '../components/Toast';
import { fmtDateTime, isFuture } from '../lib/format';

export default function Keys() {
  const toast = useToast();

  const [items, setItems] = useState<ApiKey[]>([]);
  const [proxies, setProxies] = useState<ProxyItem[]>([]);
  const [settings, setSettings] = useState<Settings | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  const [testingId, setTestingId] = useState<string | null>(null);
  const [toggleBusy, setToggleBusy] = useState<string | null>(null);

  // Reassign proxy modal
  const [reassignTarget, setReassignTarget] = useState<ApiKey | null>(null);
  const [reassignProxyId, setReassignProxyId] = useState('');
  const [reassigning, setReassigning] = useState(false);

  // Delete modal
  const [deleteTarget, setDeleteTarget] = useState<ApiKey | null>(null);
  const [deleting, setDeleting] = useState(false);

  // Admin gating for proxy reassignment: hide for explicitly non-admin roles.
  const role = getStoredUser()?.role;
  const isAdmin = !role || role === 'admin';

  const load = useCallback((spin = true) => {
    if (spin) setLoading(true);
    else setRefreshing(true);
    setError('');
    Promise.all([
      api.keys(),
      api.proxies().catch(() => [] as ProxyItem[]),
      api.settings().catch(() => null as Settings | null),
    ])
      .then(([k, px, s]) => {
        setItems(k);
        setProxies(px);
        setSettings(s);
      })
      .catch((e) => setError(errorMessage(e, 'Failed to load keys')))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function onToggle(k: ApiKey, next: boolean) {
    setToggleBusy(k.id);
    setItems((prev) => prev.map((x) => (x.id === k.id ? { ...x, active: next } : x)));
    try {
      await api.updateKey(k.id, next);
      toast(`Key “${k.label}” ${next ? 'activated' : 'deactivated'}`, 'success');
    } catch (e) {
      setItems((prev) => prev.map((x) => (x.id === k.id ? { ...x, active: !next } : x)));
      toast(errorMessage(e, 'Failed to update key'), 'error');
    } finally {
      setToggleBusy(null);
    }
  }

  async function onTest(k: ApiKey) {
    setTestingId(k.id);
    try {
      const res = await api.testKey(k.id);
      toast(`${k.label}: ${resultSummary(res)}`, 'success');
      load(false);
    } catch (e) {
      toast(`${k.label} test failed: ${errorMessage(e)}`, 'error');
    } finally {
      setTestingId(null);
    }
  }

  function openReassign(k: ApiKey) {
    setReassignTarget(k);
    setReassignProxyId('');
  }

  async function submitReassign() {
    if (!reassignTarget || !reassignProxyId) return;
    setReassigning(true);
    try {
      await api.reassignKeyProxy(reassignTarget.id, reassignProxyId);
      toast(`Proxy binding updated for “${reassignTarget.label}”`, 'success');
      setReassignTarget(null);
      load(false);
    } catch (e) {
      toast(errorMessage(e, 'Failed to reassign proxy'), 'error');
    } finally {
      setReassigning(false);
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await api.deleteKey(deleteTarget.id);
      toast(`Key “${deleteTarget.label}” deleted`, 'success');
      setDeleteTarget(null);
      load(false);
    } catch (e) {
      toast(errorMessage(e, 'Failed to delete key'), 'error');
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">Keys</h1>
          <p className="text-xs text-zinc-500">Every provider credential managed by this GPUHub instance.</p>
        </div>
        <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
          <RefreshCw size={14} />
          Refresh
        </Button>
      </div>

      {/* Rotation strategy card */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <RotateCw size={14} className="text-emerald-400" /> Rotation strategy
          </CardTitle>
          <Link
            to="/settings"
            className="flex items-center gap-0.5 text-[11px] font-medium uppercase tracking-wider text-emerald-400 hover:text-emerald-300"
          >
            Configure in Settings <ChevronRight size={12} />
          </Link>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-x-10 gap-y-3">
          <div>
            <div className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Key rotation</div>
            <div className="mt-1">
              <Badge variant="accent">{settings?.rotation_strategy ?? '—'}</Badge>
            </div>
          </div>
          <div>
            <div className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">Scheduler</div>
            <div className="mt-1">
              <Badge variant="info">{settings?.scheduler_strategy ?? '—'}</Badge>
            </div>
          </div>
          <p className="max-w-md text-[11px] leading-relaxed text-zinc-500">
            Read-only view. Change strategies under Settings — rotation is applied on every scheduling cycle.
          </p>
        </CardContent>
      </Card>

      {loading ? (
        <LoadingBlock label="Loading keys…" />
      ) : error ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={KeyRound}
          title="No keys yet"
          subtitle="Add a key from the Providers page. Keys without a bound proxy remain disabled."
          action={
            <Link to="/providers">
              <Button size="sm" variant="primary">
                Go to Providers <ChevronRight size={14} />
              </Button>
            </Link>
          }
        />
      ) : (
        <Table>
          <THead>
            <Tr>
              <Th>Label</Th>
              <Th>Provider</Th>
              <Th>Credential</Th>
              <Th>Bound proxy</Th>
              <Th>Pri</Th>
              <Th>Daily quota</Th>
              <Th>Weekly quota</Th>
              <Th>Last used</Th>
              <Th>Errors</Th>
              <Th>Cooldown until</Th>
              <Th>Active</Th>
              <Th className="text-right">Actions</Th>
            </Tr>
          </THead>
          <TBody>
            {items.map((k) => {
              const proxyLabel = keyProxyLabel(k);
              const errors = keyErrors(k);
              const cooldown = k.cooldown_until;
              const cooling = isFuture(cooldown);
              const active = k.active ?? true;
              return (
                <Tr key={k.id}>
                  <Td>
                    <div className="max-w-[140px] truncate text-xs font-semibold text-zinc-100" title={k.label}>
                      {k.label}
                    </div>
                    <code className="text-[10px] text-zinc-600">{k.id.slice(0, 8)}</code>
                  </Td>
                  <Td className="text-xs text-zinc-300">{k.provider_name ?? k.provider ?? '—'}</Td>
                  <Td>
                    <code className="text-[10px] text-zinc-500">{keyMaskedCredential(k)}</code>
                  </Td>
                  <Td>
                    {proxyLabel ? (
                      <span className="text-xs text-zinc-300">{proxyLabel}</span>
                    ) : (
                      <Badge variant="danger" className="border-red-500/40">
                        NO PROXY — disabled
                      </Badge>
                    )}
                  </Td>
                  <Td className="tabular-nums text-xs text-zinc-400">{k.priority ?? '—'}</Td>
                  <Td className="min-w-[110px]">
                    <QuotaCell used={k.daily_used} quota={k.daily_quota} />
                  </Td>
                  <Td className="min-w-[110px]">
                    <QuotaCell used={k.weekly_used} quota={k.weekly_quota} />
                  </Td>
                  <Td className="text-[11px] text-zinc-500">{k.last_used_at ? fmtDateTime(k.last_used_at) : '—'}</Td>
                  <Td>
                    {errors > 0 ? (
                      <span className="font-mono text-xs font-semibold text-red-400">{errors}</span>
                    ) : (
                      <span className="font-mono text-xs text-zinc-500">0</span>
                    )}
                  </Td>
                  <Td className="text-[11px]">
                    {cooldown ? (
                      <span className={cooling ? 'font-medium text-amber-400' : 'text-zinc-500'}>
                        {fmtDateTime(cooldown)}
                      </span>
                    ) : (
                      <span className="text-zinc-600">—</span>
                    )}
                  </Td>
                  <Td>
                    <Toggle
                      checked={active}
                      onChange={(next) => onToggle(k, next)}
                      disabled={toggleBusy === k.id}
                      label={`Toggle ${k.label}`}
                    />
                  </Td>
                  <Td>
                    <div className="flex items-center justify-end gap-1">
                      <Button
                        size="sm"
                        variant="ghost"
                        onClick={() => onTest(k)}
                        loading={testingId === k.id}
                        title="Test key"
                        aria-label="Test key"
                      >
                        <FlaskConical size={14} />
                      </Button>
                      {isAdmin && (
                        <Button
                          size="sm"
                          variant="ghost"
                          onClick={() => openReassign(k)}
                          title="Reassign proxy (admin)"
                          aria-label="Reassign proxy"
                        >
                          <ArrowRightLeft size={14} />
                        </Button>
                      )}
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-red-400 hover:bg-red-500/10 hover:text-red-300"
                        onClick={() => setDeleteTarget(k)}
                        title="Delete key"
                        aria-label="Delete key"
                      >
                        <Trash2 size={14} />
                      </Button>
                    </div>
                  </Td>
                </Tr>
              );
            })}
          </TBody>
        </Table>
      )}

      {/* Reassign proxy modal */}
      <Modal
        open={!!reassignTarget}
        onClose={() => setReassignTarget(null)}
        title="Reassign proxy"
        footer={
          <>
            <Button variant="ghost" onClick={() => setReassignTarget(null)}>
              Cancel
            </Button>
            <Button variant="warning" onClick={submitReassign} loading={reassigning} disabled={!reassignProxyId}>
              <ArrowRightLeft size={14} /> Reassign permanently
            </Button>
          </>
        }
      >
        {reassignTarget && (
          <div className="space-y-4">
            <p className="text-sm text-zinc-300">
              Rebinding <span className="font-semibold text-zinc-100">{reassignTarget.label}</span> to a new proxy.
            </p>
            <div className="flex items-start gap-2.5 rounded-lg border border-red-500/30 bg-red-500/[0.07] px-3 py-2.5 text-xs leading-relaxed text-red-300">
              <AlertTriangle size={15} className="mt-0.5 shrink-0" />
              <span>
                This is a permanent binding. Manual action only — the key keeps this proxy until an admin changes it
                again. Free-tier providers ban reused credentials across egress IPs.
              </span>
            </div>
            <div>
              <Label htmlFor="reassign-proxy">New proxy *</Label>
              <Select
                id="reassign-proxy"
                value={reassignProxyId}
                onChange={(e) => setReassignProxyId(e.target.value)}
                disabled={reassigning}
              >
                <option value="">Select a proxy…</option>
                {proxies.map((p) => (
                  <option key={p.id} value={p.id}>
                    {p.label} {p.country ? `· ${p.country}` : ''}
                  </option>
                ))}
              </Select>
            </div>
          </div>
        )}
      </Modal>

      {/* Delete modal */}
      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete key"
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button variant="danger" onClick={confirmDelete} loading={deleting}>
              <Trash2 size={14} /> Delete key
            </Button>
          </>
        }
      >
        {deleteTarget && (
          <p className="text-sm text-zinc-300">
            Delete key <span className="font-semibold text-zinc-100">{deleteTarget.label}</span>? The credential is
            permanently removed from this instance. This cannot be undone.
          </p>
        )}
      </Modal>
    </div>
  );
}

function QuotaCell({ used, quota }: { used?: number | null; quota?: number | null }) {
  if (quota == null) {
    return <span className="text-xs text-zinc-600">{used != null ? `${used} used · no cap` : '—'}</span>;
  }
  const u = used ?? 0;
  const pct = quota > 0 ? (u / quota) * 100 : 0;
  const tone = pct >= 90 ? 'red' : pct >= 70 ? 'amber' : 'emerald';
  return (
    <div className="w-28">
      <div className="mb-1 flex items-center justify-between text-[10px] tabular-nums text-zinc-500">
        <span>
          {u}/{quota}
        </span>
        <span className={pct >= 90 ? 'text-red-400' : pct >= 70 ? 'text-amber-400' : 'text-zinc-500'}>
          {pct.toFixed(0)}%
        </span>
      </div>
      <Meter value={u} max={quota} tone={tone} />
    </div>
  );
}
