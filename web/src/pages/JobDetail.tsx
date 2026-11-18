import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { ArrowLeft, Ban, Copy, Terminal, Unplug, History, ShieldAlert } from 'lucide-react';
import {
  api,
  errorMessage,
  getAccessToken,
  jobProviderName,
  jobCreatedAt,
  jobCostMicros,
  attemptStartedAt,
  attemptKeyLabel,
  attemptProxyLabel,
  type Job,
} from '../api';
import { Button, Card, CardContent, CardHeader, CardTitle, StatusBadge, LoadingBlock, ErrorBlock, Table, THead, TBody, Tr, Th, Td, Modal } from '../components/ui';
import { useToast } from '../components/Toast';
import { useLang, L } from '../i18n';
import { usd, fmtDateTime, shortId } from '../lib/format';

type WsState = 'connecting' | 'live' | 'error' | 'closed';

export default function JobDetail() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const toast = useToast();
  const lang = useLang();

  const [job, setJob] = useState<Job | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const [cancelOpen, setCancelOpen] = useState(false);
  const [cancelling, setCancelling] = useState(false);

  const [lines, setLines] = useState<string[]>([]);
  const [wsState, setWsState] = useState<WsState>('connecting');
  const logRef = useRef<HTMLDivElement>(null);

  const load = useCallback(() => {
    if (!id) return;
    setLoading(true);
    setError('');
    api
      .job(id)
      .then(setJob)
      .catch((e) => setError(errorMessage(e, lang === 'tr' ? 'İş yüklenemedi' : 'Failed to load job')))
      .finally(() => setLoading(false));
  }, [id, lang]);

  useEffect(() => {
    load();
  }, [load]);

  // WebSocket log stream
  useEffect(() => {
    if (!id) return;
    const token = getAccessToken();
    if (!token) {
      setWsState('error');
      return;
    }
    const proto = window.location.protocol === 'https:' ? 'wss' : 'ws';
    let ws: WebSocket | null = null;
    let disposed = false;
    try {
      ws = new WebSocket(
        `${proto}://${window.location.host}/api/v1/jobs/${encodeURIComponent(id)}/logs?token=${encodeURIComponent(token)}`,
      );
    } catch {
      setWsState('error');
      return;
    }
    ws.onopen = () => {
      if (!disposed) setWsState('live');
    };
    ws.onmessage = (ev: MessageEvent) => {
      if (disposed) return;
      const text = typeof ev.data === 'string' ? ev.data : String(ev.data);
      setLines((prev) => [...prev, ...text.split(/\r?\n/).filter((l) => l.length >= 0)]);
    };
    ws.onerror = () => {
      if (!disposed) setWsState('error');
    };
    ws.onclose = () => {
      if (disposed) return;
      setWsState((s) => (s === 'error' ? 'error' : 'closed'));
    };
    return () => {
      disposed = true;
      try {
        ws?.close();
      } catch {
        /* ignore */
      }
    };
  }, [id]);

  // Autoscroll logs
  useEffect(() => {
    const el = logRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  async function onCancel() {
    if (!id) return;
    setCancelling(true);
    try {
      await api.cancelJob(id);
      toast(L(lang, { en: 'Cancel requested', tr: 'İptal isteği gönderildi' }), 'success');
      setCancelOpen(false);
      load();
    } catch (e) {
      toast(errorMessage(e, L(lang, { en: 'Failed to cancel job', tr: 'İş iptal edilemedi' })), 'error');
    } finally {
      setCancelling(false);
    }
  }

  function copyId() {
    if (!job?.id) return;
    navigator.clipboard
      ?.writeText(job.id)
      .then(() => toast(L(lang, { en: 'Job ID copied', tr: 'İş ID’si kopyalandı' }), 'info'))
      .catch(() => toast(L(lang, { en: 'Could not copy', tr: 'Kopyalanamadı' }), 'error'));
  }

  if (loading) return <LoadingBlock label={L(lang, { en: 'Loading job…', tr: 'İş yükleniyor…' })} />;
  if (error || !job) {
    return (
      <div className="space-y-4">
        <BackButton onClick={() => navigate('/')}>{L(lang, { en: 'Back to dashboard', tr: 'Panele dön' })}</BackButton>
        <ErrorBlock message={error || L(lang, { en: 'Job not found', tr: 'İş bulunamadı' })} onRetry={load} />
      </div>
    );
  }

  const status = (job.status ?? '').toLowerCase();
  const cancelable = status === 'queued' || status === 'running' || status === 'pending';
  const rejections = job.rejections && typeof job.rejections === 'object' ? Object.entries(job.rejections) : [];
  const attempts = Array.isArray(job.attempts) ? job.attempts : [];
  const cost = jobCostMicros(job);
  const wsFailed = wsState === 'error' || (wsState === 'closed' && lines.length === 0);
  const storedLogs = typeof job.logs === 'string' ? job.logs.trim() : '';

  return (
    <div className="space-y-5">
      <BackButton onClick={() => navigate('/')}>{L(lang, { en: 'Back to dashboard', tr: 'Panele dön' })}</BackButton>

      {/* Header */}
      <div className="flex flex-wrap items-start justify-between gap-4">
        <div className="min-w-0">
          <div className="flex flex-wrap items-center gap-3">
            <h1 className="flex items-center gap-2 font-mono text-lg font-bold text-zinc-50">
              job/{shortId(job.id)}
              <button onClick={copyId} className="rounded p-1 text-zinc-500 hover:bg-zinc-800 hover:text-zinc-200" aria-label="Copy full job ID">
                <Copy size={13} />
              </button>
            </h1>
            <StatusBadge status={job.status} />
          </div>
          <p className="mt-1 text-xs capitalize text-zinc-500">
            {job.type} · {L(lang, { en: 'created', tr: 'oluşturuldu' })} {fmtDateTime(jobCreatedAt(job))}
          </p>
        </div>
        {cancelable && (
          <Button variant="danger" onClick={() => setCancelOpen(true)}>
            <Ban size={14} /> {L(lang, { en: 'Cancel job', tr: 'İşi iptal et' })}
          </Button>
        )}
      </div>

      {/* Metadata grid */}
      <Card>
        <CardContent className="grid grid-cols-2 gap-4 sm:grid-cols-3 lg:grid-cols-6">
          <Meta label={L(lang, { en: 'GPU type', tr: 'GPU tipi' })} value={job.gpu_type ?? 'Any'} />
          <Meta label={L(lang, { en: 'Min VRAM', tr: 'Min VRAM' })} value={job.min_vram_gb != null ? `${job.min_vram_gb} GB` : '-'} />
          <Meta label={L(lang, { en: 'GPU count', tr: 'GPU adedi' })} value={job.gpu_count != null ? String(job.gpu_count) : '-'} />
          <Meta label={L(lang, { en: 'Max duration', tr: 'Maks süre' })} value={job.max_duration_seconds ? `${job.max_duration_seconds}s` : '-'} />
          <Meta label={L(lang, { en: 'Provider', tr: 'Sağlayıcı' })} value={jobProviderName(job)} />
          <Meta label={L(lang, { en: 'Cost', tr: 'Maliyet' })} value={cost != null ? usd(cost) : '-'} tone="emerald" />
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-5">
        {/* Left: failover history + rejections */}
        <div className="space-y-4 xl:col-span-2">
          {/* Why unschedulable */}
          {rejections.length > 0 && (
            <div className="rounded-xl border border-amber-500/25 bg-amber-500/5 p-4">
              <div className="mb-2 flex items-center gap-2 text-xs font-semibold uppercase tracking-wider text-amber-400">
                <ShieldAlert size={14} /> {L(lang, { en: 'Why unschedulable - provider rejections', tr: 'Neden unschedulable - sağlayıcı retleri' })}
              </div>
              <ul className="space-y-1.5">
                {rejections.map(([provider, reason]) => (
                  <li key={provider} className="flex flex-col gap-0.5 rounded-lg border border-amber-500/10 bg-zinc-950/40 px-3 py-2 sm:flex-row sm:items-baseline sm:gap-3">
                    <span className="shrink-0 font-mono text-[11px] font-semibold text-amber-300">{provider}</span>
                    <span className="text-[11px] leading-snug text-amber-200/80">
                      {Array.isArray(reason) ? reason.join('; ') : String(reason)}
                    </span>
                  </li>
                ))}
              </ul>
            </div>
          )}

          {/* Failover history */}
          <Card>
            <CardHeader>
              <CardTitle className="flex items-center gap-2">
                <History size={14} className="text-zinc-500" /> {L(lang, { en: 'Failover history', tr: 'Failover geçmişi' })}
              </CardTitle>
              <span className="text-[10px] font-medium uppercase tracking-wider text-zinc-500">
                {attempts.length} {L(lang, { en: 'attempt(s)', tr: 'deneme' })}
              </span>
            </CardHeader>
            <CardContent className="p-0">
              {attempts.length === 0 ? (
                <div className="px-5 py-6 text-center text-xs text-zinc-600">
                  {L(lang, { en: 'No attempts yet - the scheduler has not placed this job.', tr: 'Henüz deneme yok - scheduler bu işi henüz yerleştiremedi.' })}
                </div>
              ) : (
                <Table className="rounded-none border-0">
                  <THead>
                    <Tr>
                      <Th>{L(lang, { en: 'Provider', tr: 'Sağlayıcı' })}</Th>
                      <Th>{L(lang, { en: 'Key', tr: 'Anahtar' })}</Th>
                      <Th>Proxy</Th>
                      <Th>{L(lang, { en: 'Status', tr: 'Durum' })}</Th>
                      <Th>{L(lang, { en: 'Error', tr: 'Hata' })}</Th>
                      <Th>{L(lang, { en: 'Started', tr: 'Başlangıç' })}</Th>
                    </Tr>
                  </THead>
                  <TBody>
                    {attempts.map((a, i) => (
                      <Tr key={i}>
                        <Td className="text-xs text-zinc-200">{a.provider_name ?? a.provider ?? '-'}</Td>
                        <Td className="font-mono text-[11px] text-zinc-400">{attemptKeyLabel(a)}</Td>
                        <Td className="font-mono text-[11px] text-zinc-400">{attemptProxyLabel(a)}</Td>
                        <Td><StatusBadge status={a.status ?? 'unknown'} /></Td>
                        <Td className="max-w-[160px] truncate text-[11px] text-red-300/90" title={a.error ?? ''}>{a.error ?? '-'}</Td>
                        <Td className="text-[11px] text-zinc-500">{fmtDateTime(attemptStartedAt(a))}</Td>
                      </Tr>
                    ))}
                  </TBody>
                </Table>
              )}
            </CardContent>
          </Card>
        </div>

        {/* Right / full width: logs */}
        <Card className="xl:col-span-3">
          <CardHeader>
            <CardTitle className="flex items-center gap-2">
              <Terminal size={14} className="text-zinc-500" /> {L(lang, { en: 'Logs', tr: 'Loglar' })}
            </CardTitle>
            <WsIndicator state={wsState} failed={wsFailed} />
          </CardHeader>
          <CardContent>
            {wsFailed && storedLogs ? (
              <>
                <div className="mb-2 flex items-center gap-1.5 text-[10px] font-medium uppercase tracking-wider text-amber-500/90">
                  <Unplug size={11} /> {L(lang, { en: 'Live stream unavailable - showing stored logs', tr: 'Canlı akış yok - kayıtlı loglar gösteriliyor' })}
                </div>
                <pre className="h-96 overflow-auto whitespace-pre-wrap rounded-lg border border-zinc-800 bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed text-zinc-300">
                  {storedLogs}
                </pre>
              </>
            ) : (
              <div
                ref={logRef}
                className="h-96 overflow-auto whitespace-pre-wrap rounded-lg border border-zinc-800 bg-zinc-950 p-3 font-mono text-[11px] leading-relaxed text-zinc-300"
              >
                {lines.length === 0 ? (
                  <span className="text-zinc-600">
                    {wsState === 'connecting'
                      ? L(lang, { en: 'Connecting to log stream…', tr: 'Log akışına bağlanılıyor…' })
                      : wsState === 'live'
                        ? L(lang, { en: 'Connected - waiting for log output…', tr: 'Bağlandı - log çıktısı bekleniyor…' })
                        : wsFailed
                          ? L(lang, { en: 'Live stream unavailable and no stored logs were returned.', tr: 'Canlı akış yok ve kayıtlı log da dönmedi.' })
                          : L(lang, { en: 'Stream closed. No log output received.', tr: 'Akış kapandı. Log çıktısı alınmadı.' })}
                  </span>
                ) : (
                  lines.map((l, i) => (
                    <div key={i} className="hover:bg-zinc-900/60">{l || ' '}</div>
                  ))
                )}
              </div>
            )}
          </CardContent>
        </Card>
      </div>

      {/* Cancel confirm modal */}
      <Modal
        open={cancelOpen}
        onClose={() => setCancelOpen(false)}
        title={L(lang, { en: 'Cancel job', tr: 'İşi iptal et' })}
        footer={
          <>
            <Button variant="ghost" onClick={() => setCancelOpen(false)}>
              {L(lang, { en: 'Keep running', tr: 'Çalışmaya devam et' })}
            </Button>
            <Button variant="danger" onClick={onCancel} loading={cancelling}>
              <Ban size={14} /> {L(lang, { en: 'Cancel job', tr: 'İşi iptal et' })}
            </Button>
          </>
        }
      >
        <div className="space-y-3 text-sm text-zinc-300">
          <p>
            {L(lang, { en: 'Cancel job', tr: 'İş iptal edilsin' })} <span className="font-mono text-zinc-100">{shortId(job.id, 12)}</span>?
          </p>
          <p className="text-xs text-zinc-500">
            {L(lang, {
              en: 'The scheduler will stop placing this job and signal the provider to abort the workload. This cannot be undone.',
              tr: 'Scheduler bu işi yerleştirmeyi bırakır ve sağlayıcıya yükü durdur sinyali gönderir. Bu işlem geri alınamaz.',
            })}
          </p>
        </div>
      </Modal>
    </div>
  );
}

function Meta({ label, value, tone }: { label: string; value: string; tone?: 'emerald' }) {
  return (
    <div className="min-w-0">
      <div className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">{label}</div>
      <div className={`mt-1 truncate text-sm font-medium ${tone === 'emerald' ? 'text-emerald-400' : 'text-zinc-100'}`}>
        {value}
      </div>
    </div>
  );
}

function BackButton({ onClick, children }: { onClick: () => void; children?: ReactNode }) {
  return (
    <button
      onClick={onClick}
      className="inline-flex items-center gap-1.5 text-xs font-medium text-zinc-500 transition-colors hover:text-zinc-100"
    >
      <ArrowLeft size={13} />
      {children ?? 'Back'}
    </button>
  );
}

function WsIndicator({ state, failed }: { state: WsState; failed: boolean }) {
  const map: Record<WsState, { label: string; cls: string; dot: string }> = {
    connecting: { label: 'CONNECTING', cls: 'border-amber-500/30 bg-amber-500/10 text-amber-400', dot: 'bg-amber-400 animate-pulse' },
    live: { label: 'LIVE', cls: 'border-emerald-500/30 bg-emerald-500/10 text-emerald-400', dot: 'bg-emerald-400 animate-pulse' },
    error: { label: 'ERROR', cls: 'border-red-500/30 bg-red-500/10 text-red-400', dot: 'bg-red-400' },
    closed: { label: 'CLOSED', cls: 'border-zinc-700 bg-zinc-800/60 text-zinc-400', dot: 'bg-zinc-500' },
  };
  const m = map[state] ?? map.closed;
  return (
    <span className={`inline-flex items-center gap-1.5 rounded-full border px-2 py-0.5 text-[9px] font-bold uppercase tracking-wider ${m.cls}`}>
      <span className={`h-1.5 w-1.5 rounded-full ${m.dot}`} />
      {failed ? 'OFF' : m.label}
    </span>
  );
}
