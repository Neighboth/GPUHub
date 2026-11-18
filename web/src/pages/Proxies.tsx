import { useCallback, useEffect, useState, type FormEvent } from 'react';
import { Network, Plus, RefreshCw, FlaskConical, Trash2, Globe2, KeyRound } from 'lucide-react';
import {
  api,
  errorMessage,
  resultSummary,
  maskProxyUrl,
  proxyLabelHealth,
  disabledKeysSignal,
  type ProxyItem,
} from '../api';
import {
  Button,
  Modal,
  Input,
  Label,
  Table,
  THead,
  TBody,
  Tr,
  Th,
  Td,
  Badge,
  LoadingBlock,
  ErrorBlock,
  EmptyState,
  FieldError,
} from '../components/ui';
import { useToast } from '../components/Toast';
import { fmtDateTime } from '../lib/format';

function healthBadge(p: ProxyItem) {
  const h = proxyLabelHealth(p);
  if (h === 'healthy') return <Badge variant="success">healthy</Badge>;
  if (h === 'unhealthy') return <Badge variant="danger">unhealthy</Badge>;
  return <Badge variant="neutral">unknown</Badge>;
}

function boundKeyOf(p: ProxyItem): { label?: string; provider?: string } | null {
  if (p.bound_key && (p.bound_key.label || p.bound_key.id)) {
    return { label: p.bound_key.label, provider: p.bound_key.provider ?? p.bound_key.provider_name };
  }
  const first = p.bound_keys?.find((k) => k.label || k.id);
  if (first) return { label: first.label, provider: first.provider ?? first.provider_name };
  return null;
}

export default function Proxies() {
  const toast = useToast();
  const [items, setItems] = useState<ProxyItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  const [testingId, setTestingId] = useState<string | null>(null);
  const [deletingId, setDeletingId] = useState<string | null>(null);

  // Delete confirm modal
  const [deleteTarget, setDeleteTarget] = useState<ProxyItem | null>(null);

  // Add proxy modal
  const [addOpen, setAddOpen] = useState(false);
  const [label, setLabel] = useState('');
  const [url, setUrl] = useState('');
  const [country, setCountry] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');

  const load = useCallback((spin = true) => {
    if (spin) setLoading(true);
    else setRefreshing(true);
    setError('');
    api
      .proxies()
      .then(setItems)
      .catch((e) => setError(errorMessage(e, 'Failed to load proxies')))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function onTest(p: ProxyItem) {
    setTestingId(p.id);
    try {
      const res = await api.testProxy(p.id);
      toast(`${p.label}: ${resultSummary(res)}`, 'success');
      load(false);
    } catch (e) {
      toast(`${p.label} test failed: ${errorMessage(e)}`, 'error');
    } finally {
      setTestingId(null);
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    const target = deleteTarget;
    setDeletingId(target.id);
    try {
      const res = await api.deleteProxy(target.id);
      const disabled = disabledKeysSignal(res);
      if (disabled) {
        toast(
          disabled === -1
            ? `Proxy “${target.label}” deleted. Its bound keys were disabled — re-bind them to a proxy.`
            : `Proxy “${target.label}” deleted. ${disabled} bound key(s) were disabled.`,
          'warning',
        );
      } else {
        toast(`Proxy “${target.label}” deleted`, 'success');
      }
      setDeleteTarget(null);
      load(false);
    } catch (e) {
      toast(errorMessage(e, 'Failed to delete proxy'), 'error');
    } finally {
      setDeletingId(null);
    }
  }

  async function submitAdd(e: FormEvent) {
    e.preventDefault();
    if (!label.trim()) return setFormError('Label is required.');
    if (!url.trim()) return setFormError('URL is required.');
    try {
      new URL(url.trim());
    } catch {
      return setFormError('Enter a valid URL, e.g. socks5://user:pass@host:1080');
    }
    setSubmitting(true);
    setFormError('');
    try {
      await api.createProxy({ label: label.trim(), url: url.trim(), country: country.trim() || undefined });
      toast(`Proxy “${label.trim()}” added`, 'success');
      setAddOpen(false);
      setLabel('');
      setUrl('');
      setCountry('');
      load(false);
    } catch (e) {
      setFormError(errorMessage(e, 'Failed to add proxy'));
    } finally {
      setSubmitting(false);
    }
  }

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">Proxies</h1>
          <p className="text-xs text-zinc-500">Egress proxies that bound keys are pinned to.</p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
            <RefreshCw size={14} />
            Refresh
          </Button>
          <Button size="sm" variant="primary" onClick={() => setAddOpen(true)}>
            <Plus size={14} /> Add proxy
          </Button>
        </div>
      </div>

      {loading ? (
        <LoadingBlock label="Loading proxies…" />
      ) : error ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={Network}
          title="No proxies yet"
          subtitle="Add an egress proxy. API keys can then be permanently bound to it — a binding required for a key to be scheduled."
          action={
            <Button size="sm" variant="primary" onClick={() => setAddOpen(true)}>
              <Plus size={14} /> Add your first proxy
            </Button>
          }
        />
      ) : (
        <Table>
          <THead>
            <Tr>
              <Th>Label</Th>
              <Th>URL</Th>
              <Th>Country</Th>
              <Th>Health</Th>
              <Th>Bound key</Th>
              <Th className="text-right">Actions</Th>
            </Tr>
          </THead>
          <TBody>
            {items.map((p) => {
              const bound = boundKeyOf(p);
              return (
                <Tr key={p.id}>
                  <Td>
                    <div className="font-medium text-zinc-100">{p.label}</div>
                    <code className="text-[10px] text-zinc-600">{p.id}</code>
                  </Td>
                  <Td>
                    <code className="block max-w-[280px] truncate text-[11px] text-zinc-400" title={maskProxyUrl(p.url)}>
                      {maskProxyUrl(p.url)}
                    </code>
                  </Td>
                  <Td>
                    {p.country ? (
                      <span className="inline-flex items-center gap-1.5 text-xs text-zinc-300">
                        <Globe2 size={12} className="text-zinc-500" /> {p.country}
                      </span>
                    ) : (
                      <span className="text-zinc-600">—</span>
                    )}
                  </Td>
                  <Td>
                    <div className="space-y-1">
                      {healthBadge(p)}
                      <div className="text-[10px] text-zinc-500">
                        {p.last_check_at ? `checked ${fmtDateTime(p.last_check_at)}` : 'never checked'}
                      </div>
                    </div>
                  </Td>
                  <Td>
                    {bound ? (
                      <span className="inline-flex items-center gap-1.5 text-xs text-zinc-300">
                        <KeyRound size={12} className="text-emerald-500" />
                        <span className="font-medium">{bound.label ?? 'key'}</span>
                        {bound.provider && <span className="text-zinc-500">· {bound.provider}</span>}
                      </span>
                    ) : (
                      <span className="text-zinc-600">—</span>
                    )}
                  </Td>
                  <Td>
                    <div className="flex items-center justify-end gap-1.5">
                      <Button size="sm" variant="outline" onClick={() => onTest(p)} loading={testingId === p.id} aria-label="Test proxy">
                        <FlaskConical size={13} />
                        <span className="hidden sm:inline">Test</span>
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="text-red-400 hover:bg-red-500/10 hover:text-red-300"
                        onClick={() => setDeleteTarget(p)}
                        aria-label="Delete proxy"
                      >
                        <Trash2 size={14} />
                        <span className="hidden sm:inline">Delete</span>
                      </Button>
                    </div>
                  </Td>
                </Tr>
              );
            })}
          </TBody>
        </Table>
      )}

      {/* Add proxy modal */}
      <Modal
        open={addOpen}
        onClose={() => setAddOpen(false)}
        title="Add proxy"
        footer={
          <>
            <Button variant="ghost" onClick={() => setAddOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" form="add-proxy-form" loading={submitting}>
              Add proxy
            </Button>
          </>
        }
      >
        <form id="add-proxy-form" onSubmit={submitAdd} className="space-y-4">
          <div>
            <Label htmlFor="proxy-label">Label *</Label>
            <Input
              id="proxy-label"
              placeholder="e.g. frankfurt-socks-1"
              value={label}
              onChange={(e) => setLabel(e.target.value)}
              disabled={submitting}
            />
          </div>
          <div>
            <Label htmlFor="proxy-url">URL *</Label>
            <Input
              id="proxy-url"
              placeholder="socks5://user:pass@host:1080"
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              disabled={submitting}
              className="font-mono text-xs"
            />
          </div>
          <div>
            <Label htmlFor="proxy-country">Country</Label>
            <Input
              id="proxy-country"
              placeholder="e.g. DE, TR, US"
              value={country}
              onChange={(e) => setCountry(e.target.value)}
              disabled={submitting}
              maxLength={64}
            />
          </div>
          <FieldError>{formError}</FieldError>
        </form>
      </Modal>

      {/* Delete confirm modal */}
      <Modal
        open={!!deleteTarget}
        onClose={() => setDeleteTarget(null)}
        title="Delete proxy"
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleteTarget(null)}>
              Cancel
            </Button>
            <Button variant="danger" onClick={confirmDelete} loading={!!deletingId}>
              <Trash2 size={14} /> Delete proxy
            </Button>
          </>
        }
      >
        {deleteTarget && (
          <div className="space-y-3 text-sm text-zinc-300">
            <p>
              Delete <span className="font-semibold text-zinc-100">{deleteTarget.label}</span>? This cannot be undone.
            </p>
            <div className="rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2 text-xs leading-relaxed text-amber-300">
              If any keys are bound to this proxy, they will be <strong>disabled</strong> and stop being scheduled
              until re-bound to another proxy.
            </div>
          </div>
        )}
      </Modal>
    </div>
  );
}
