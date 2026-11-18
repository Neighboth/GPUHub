import { useCallback, useEffect, useState } from 'react';
import { useNavigate } from 'react-router-dom';
import {
  Activity,
  Clock3,
  CheckCircle2,
  XCircle,
  DollarSign,
  RefreshCw,
  AlertTriangle,
  type LucideIcon,
} from 'lucide-react';
import { ResponsiveContainer, AreaChart, Area, XAxis, YAxis, CartesianGrid, Tooltip } from 'recharts';
import { api, errorMessage, type DashboardSummary, type Job } from '../api';
import { Card, CardContent, CardHeader, CardTitle, LoadingBlock, ErrorBlock, Button, Meter, StatusBadge } from '../components/ui';
import ProviderIcon from '../components/ProviderIcon';
import { useLang, L } from '../i18n';
import { usd, fmtDate, fmtDateTime, shortId } from '../lib/format';

function StatCard({
  label,
  value,
  icon: Icon,
  tone,
  sub,
}: {
  label: string;
  value: string;
  icon: LucideIcon;
  tone: 'emerald' | 'amber' | 'red' | 'cyan';
  sub?: string;
}) {
  const toneClasses = {
    emerald: 'border-emerald-500/25 bg-emerald-500/[0.07] text-emerald-400',
    amber: 'border-amber-500/25 bg-amber-500/[0.07] text-amber-400',
    red: 'border-red-500/25 bg-red-500/[0.07] text-red-400',
    cyan: 'border-cyan-500/25 bg-cyan-500/[0.07] text-cyan-400',
  } as const;
  return (
    <div className="card-surface px-4 py-4">
      <div className="flex items-center justify-between gap-2">
        <span className="text-[11px] font-semibold uppercase tracking-wider text-zinc-500">{label}</span>
        <span className={`flex h-8 w-8 items-center justify-center rounded-lg border ${toneClasses[tone]}`}>
          <Icon size={16} strokeWidth={2} />
        </span>
      </div>
      <div className="mt-2 text-2xl font-bold tabular-nums tracking-tight text-zinc-50">{value}</div>
      {sub && <div className="mt-0.5 text-[11px] text-zinc-500">{sub}</div>}
    </div>
  );
}

export default function Dashboard() {
  const navigate = useNavigate();
  const lang = useLang();
  const [data, setData] = useState<DashboardSummary | null>(null);
  const [recentJobs, setRecentJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  const load = useCallback((showSpinner = true) => {
    if (showSpinner) setLoading(true);
    else setRefreshing(true);
    setError('');
    Promise.all([api.dashboardSummary(), api.jobs(undefined, 8).catch(() => [] as Job[])])
      .then(([d, jobs]) => {
        setData(d);
        setRecentJobs(jobs);
      })
      .catch((e) => setError(errorMessage(e, lang === 'tr' ? 'Özet yüklenemedi' : 'Failed to load dashboard summary')))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, [lang]);

  useEffect(() => {
    load();
  }, [load]);

  const chartData = (data?.cost_series ?? []).map((p) => ({
    ...p,
    label: fmtDate(p.date),
    usd: (p.usd_micros ?? 0) / 1e6,
  }));

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">
            {L(lang, { en: 'Dashboard', tr: 'Panel' })}
          </h1>
          <p className="text-xs text-zinc-500">
            {L(lang, {
              en: 'Fleet-wide status, cost and quota at a glance.',
              tr: 'Filo geneli durum, maliyet ve kota bir bakışta.',
            })}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
          <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
          {L(lang, { en: 'Refresh', tr: 'Yenile' })}
        </Button>
      </div>

      {loading ? (
        <LoadingBlock label={L(lang, { en: 'Loading dashboard…', tr: 'Panel yükleniyor…' })} />
      ) : error && !data ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : (
        <>
          {error && (
            <div className="rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2 text-xs text-amber-300">
              {error}
            </div>
          )}

          {/* Stat cards */}
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-6">
            <StatCard label={L(lang, { en: 'Running jobs', tr: 'Çalışan işler' })} value={String(data?.running ?? 0)} icon={Activity} tone="cyan" />
            <StatCard label={L(lang, { en: 'Queued', tr: 'Kuyrukta' })} value={String(data?.queued ?? 0)} icon={Clock3} tone="amber" />
            <StatCard label={L(lang, { en: 'Succeeded · 24h', tr: 'Başarılı · 24s' })} value={String(data?.succeeded_24h ?? 0)} icon={CheckCircle2} tone="emerald" />
            <StatCard label={L(lang, { en: 'Failed · 24h', tr: 'Başarısız · 24s' })} value={String(data?.failed_24h ?? 0)} icon={XCircle} tone="red" />
            <div className="col-span-2 md:col-span-1 xl:col-span-2">
              <StatCard
                label={L(lang, { en: 'Cost today', tr: 'Bugünkü maliyet' })}
                value={usd(data?.cost_today_usd_micros)}
                icon={DollarSign}
                tone="emerald"
                sub={L(lang, { en: 'Aggregated across all providers', tr: 'Tüm sağlayıcıların toplamı' })}
              />
            </div>
          </div>

          <div className="grid grid-cols-1 gap-4 xl:grid-cols-3">
            {/* Cost chart */}
            <Card className="xl:col-span-2">
              <CardHeader>
                <CardTitle>{L(lang, { en: 'Cost trend', tr: 'Maliyet trendi' })}</CardTitle>
                <span className="text-[11px] font-medium uppercase tracking-wider text-zinc-500">USD / {L(lang, { en: 'day', tr: 'gün' })}</span>
              </CardHeader>
              <CardContent>
                {chartData.length === 0 ? (
                  <div className="flex h-56 items-center justify-center text-sm text-zinc-600">
                    {L(lang, { en: 'No cost data yet.', tr: 'Henüz maliyet verisi yok.' })}
                  </div>
                ) : (
                  <div className="h-64 w-full">
                    <ResponsiveContainer width="100%" height="100%">
                      <AreaChart data={chartData} margin={{ top: 10, right: 8, left: 0, bottom: 0 }}>
                        <defs>
                          <linearGradient id="costFill" x1="0" y1="0" x2="0" y2="1">
                            <stop offset="5%" stopColor="#10b981" stopOpacity={0.35} />
                            <stop offset="95%" stopColor="#10b981" stopOpacity={0.02} />
                          </linearGradient>
                        </defs>
                        <CartesianGrid strokeDasharray="3 3" stroke="#27272a" vertical={false} />
                        <XAxis dataKey="label" tick={{ fill: '#71717a', fontSize: 11 }} tickLine={false} axisLine={{ stroke: '#27272a' }} />
                        <YAxis
                          tick={{ fill: '#71717a', fontSize: 11 }}
                          tickLine={false}
                          axisLine={false}
                          width={64}
                          tickFormatter={(v: number) => `$${Number(v).toFixed(2)}`}
                        />
                        <Tooltip
                          cursor={{ stroke: '#34d399', strokeOpacity: 0.4 }}
                          contentStyle={{ background: '#18181b', border: '1px solid #27272a', borderRadius: 8, fontSize: 12, color: '#e4e4e7' }}
                          labelStyle={{ color: '#a1a1aa', marginBottom: 4 }}
                          formatter={(value: number) => [usd(Math.round(value * 1e6)), L(lang, { en: 'Cost', tr: 'Maliyet' })]}
                        />
                        <Area type="monotone" dataKey="usd" stroke="#10b981" strokeWidth={2} fill="url(#costFill)" name="Cost" />
                      </AreaChart>
                    </ResponsiveContainer>
                  </div>
                )}
              </CardContent>
            </Card>

            {/* Provider quota */}
            <Card>
              <CardHeader>
                <CardTitle>{L(lang, { en: 'Provider quota usage', tr: 'Sağlayıcı kota kullanımı' })}</CardTitle>
                <button
                  onClick={() => navigate('/providers')}
                  className="text-[11px] font-medium uppercase tracking-wider text-emerald-400 hover:text-emerald-300"
                >
                  {L(lang, { en: 'View all', tr: 'Tümü' })}
                </button>
              </CardHeader>
              <CardContent className="space-y-4">
                {(data?.providers ?? []).length === 0 ? (
                  <div className="py-8 text-center text-sm text-zinc-600">
                    {L(lang, { en: 'No providers configured.', tr: 'Henüz sağlayıcı yapılandırılmadı.' })}
                  </div>
                ) : (
                  (data?.providers ?? []).map((p) => {
                    const pct = Math.max(0, Math.min(100, p.quota_percent ?? 0));
                    const tone = pct >= 90 ? 'red' : pct >= 70 ? 'amber' : 'emerald';
                    return (
                      <div key={p.id} className="flex items-center gap-3">
                        <ProviderIcon id={p.id} size={16} />
                        <div className="min-w-0 flex-1">
                          <div className="mb-1 flex items-center justify-between gap-3 text-xs">
                            <span className="truncate font-medium text-zinc-300">{p.name}</span>
                            <span
                              className={`tabular-nums ${
                                tone === 'red' ? 'text-red-400' : tone === 'amber' ? 'text-amber-400' : 'text-emerald-400'
                              }`}
                            >
                              {pct.toFixed(0)}%
                            </span>
                          </div>
                          <Meter value={pct} max={100} tone={tone} />
                        </div>
                      </div>
                    );
                  })
                )}
              </CardContent>
            </Card>
          </div>

          {/* Recent jobs (API-created, e.g. from your Python code) */}
          <Card>
            <CardHeader>
              <CardTitle>{L(lang, { en: 'Recent jobs', tr: 'Son işler' })}</CardTitle>
              <span className="text-[11px] font-medium uppercase tracking-wider text-zinc-500">
                {L(lang, { en: 'via API / code', tr: 'API / kod ile' })}
              </span>
            </CardHeader>
            <CardContent className="p-0">
              {recentJobs.length === 0 ? (
                <div className="py-8 text-center text-sm text-zinc-600">
                  {L(lang, {
                    en: 'No jobs yet - submit one from your code (see examples/submit_job.py).',
                    tr: 'Henüz iş yok - kodunuzdan bir tane gönderin (bkz. examples/submit_job.py).',
                  })}
                </div>
              ) : (
                <ul className="divide-y divide-zinc-800/70">
                  {recentJobs.map((j) => (
                    <li key={j.id}>
                      <button
                        onClick={() => navigate(`/jobs/${j.id}`)}
                        className="flex w-full flex-col gap-1.5 px-5 py-3 text-left transition-colors hover:bg-zinc-800/30 sm:flex-row sm:items-center sm:gap-4"
                      >
                        <code className="shrink-0 font-mono text-[11px] text-zinc-500 sm:w-16">#{shortId(j.id)}</code>
                        <span className="min-w-0 flex-1 truncate font-mono text-xs text-zinc-300">
                          {String(j.type)} · {j.gpu_type ?? 'any'} · {j.gpu_count ?? 1}×GPU · ≥{j.min_vram_gb ?? '-'}GB
                        </span>
                        <StatusBadge status={j.status} />
                        <time className="shrink-0 font-mono text-[11px] tabular-nums text-zinc-500">
                          {j.created_at ? fmtDateTime(j.created_at) : ''}
                        </time>
                      </button>
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>

          {/* Recent errors */}
          <Card>
            <CardHeader>
              <CardTitle>{L(lang, { en: 'Recent errors', tr: 'Son hatalar' })}</CardTitle>
              <span className="inline-flex items-center gap-1.5 text-[11px] font-medium uppercase tracking-wider text-zinc-500">
                <AlertTriangle size={12} className="text-amber-400" /> {L(lang, { en: 'last events', tr: 'son olaylar' })}
              </span>
            </CardHeader>
            <CardContent className="p-0">
              {(data?.recent_errors ?? []).length === 0 ? (
                <div className="py-8 text-center text-sm text-zinc-600">
                  {L(lang, { en: 'No recent errors. Fleet is healthy.', tr: 'Son hata yok. Filo sağlıklı.' })}
                </div>
              ) : (
                <ul className="divide-y divide-zinc-800/70">
                  {(data?.recent_errors ?? []).map((e, i) => (
                    <li key={i} className="flex flex-col gap-1 px-5 py-3 sm:flex-row sm:items-center sm:gap-4">
                      <time className="shrink-0 font-mono text-[11px] tabular-nums text-zinc-500 sm:w-40">
                        {fmtDateTime(e.ts)}
                      </time>
                      <span className="break-words font-mono text-xs text-red-300/90">{e.message}</span>
                    </li>
                  ))}
                </ul>
              )}
            </CardContent>
          </Card>
        </>
      )}
    </div>
  );
}
