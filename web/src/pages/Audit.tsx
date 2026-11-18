import { useCallback, useEffect, useMemo, useState } from 'react';
import { ScrollText, Search, RefreshCw, X } from 'lucide-react';
import { api, errorMessage, auditTime, type AuditEntry } from '../api';
import {
  Button,
  Input,
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
} from '../components/ui';
import { fmtDateTime } from '../lib/format';

function actionVariant(action?: string): 'success' | 'warning' | 'danger' | 'info' | 'neutral' {
  const a = (action ?? '').toLowerCase();
  if (/(delete|remove|revoke|rotate|fail|error)/.test(a)) return 'danger';
  if (/(create|add|register|setup|enable)/.test(a)) return 'success';
  if (/(update|patch|reassign|toggle|test)/.test(a)) return 'warning';
  if (/(login|auth|refresh)/.test(a)) return 'info';
  return 'neutral';
}

export default function Audit() {
  const [entries, setEntries] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const [query, setQuery] = useState('');

  const load = useCallback((spin = true) => {
    if (spin) setLoading(true);
    else setRefreshing(true);
    setError('');
    api
      .audit(200)
      .then(setEntries)
      .catch((e) => setError(errorMessage(e, 'Failed to load audit log')))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return entries;
    return entries.filter((e) => {
      const hay = [e.actor, e.user, e.action, e.detail, e.message, auditTime(e)]
        .filter(Boolean)
        .join(' ')
        .toLowerCase();
      return hay.includes(q);
    });
  }, [entries, query]);

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">Audit Log</h1>
          <p className="text-xs text-zinc-500">Latest 200 actions across this GPUHub instance.</p>
        </div>
        <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
          <RefreshCw size={14} />
          Refresh
        </Button>
      </div>

      {/* Search */}
      <div className="relative max-w-md">
        <Search size={15} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-zinc-500" />
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder="Filter by actor, action or detail…"
          className="pl-9 pr-9"
          aria-label="Filter audit log"
        />
        {query && (
          <button
            onClick={() => setQuery('')}
            className="absolute right-2.5 top-1/2 -translate-y-1/2 rounded p-1 text-zinc-500 hover:text-zinc-200"
            aria-label="Clear filter"
          >
            <X size={14} />
          </button>
        )}
      </div>

      {loading ? (
        <LoadingBlock label="Loading audit log…" />
      ) : error ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : entries.length === 0 ? (
        <EmptyState icon={ScrollText} title="No audit entries" subtitle="Actions on this instance will appear here." />
      ) : filtered.length === 0 ? (
        <EmptyState icon={Search} title="No matches" subtitle={`Nothing in the last 200 entries matches “${query}”.`} />
      ) : (
        <>
          <div className="text-[11px] font-medium uppercase tracking-wider text-zinc-500">
            {filtered.length} of {entries.length} entries
          </div>
          <Table>
            <THead>
              <Tr>
                <Th className="w-44">Time</Th>
                <Th className="w-40">Actor</Th>
                <Th className="w-48">Action</Th>
                <Th>Detail</Th>
              </Tr>
            </THead>
            <TBody>
              {filtered.map((e, i) => (
                <Tr key={e.id ?? i}>
                  <Td className="whitespace-nowrap font-mono text-[11px] tabular-nums text-zinc-400">
                    {fmtDateTime(auditTime(e))}
                  </Td>
                  <Td className="text-xs font-medium text-zinc-200">{e.actor ?? e.user ?? 'system'}</Td>
                  <Td>
                    {e.action ? (
                      <Badge variant={actionVariant(e.action)}>{e.action}</Badge>
                    ) : (
                      <span className="text-xs text-zinc-600">—</span>
                    )}
                  </Td>
                  <Td className="max-w-xl break-words text-xs text-zinc-400">
                    {e.detail ?? e.message ?? '—'}
                  </Td>
                </Tr>
              ))}
            </TBody>
          </Table>
        </>
      )}
    </div>
  );
}
