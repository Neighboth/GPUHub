import { useCallback, useEffect, useState, type FormEvent } from 'react';
import {
  Server,
  KeyRound,
  FlaskConical,
  Gauge,
  Plus,
  RefreshCw,
  Layers,
  Globe2,
  Info,
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
import ProviderIcon from '../components/ProviderIcon';
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
import { useLang, L } from '../i18n';

export default function Providers() {
  const toast = useToast();
  const lang = useLang();
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
      .catch((e) => setError(errorMessage(e, lang === 'tr' ? 'Sağlayıcılar yüklenemedi' : 'Failed to load providers')))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, [lang]);

  useEffect(() => {
    load();
  }, [load]);

  function onToggle(p: Provider, next: boolean) {
    setToggleBusy(p.id);
    api
      .updateProvider(p.id, next)
      .then(() => {
        setProviders((prev) => prev.map((x) => (x.id === p.id ? { ...x, enabled: next } : x)));
        toast(
          lang === 'tr'
            ? `${p.name} ${next ? 'etkinleştirildi' : 'devre dışı bırakıldı'}`
            : `${p.name} ${next ? 'enabled' : 'disabled'}`,
          'success',
        );
      })
      .catch((e) => toast(errorMessage(e, lang === 'tr' ? 'Güncellenemedi' : 'Update failed'), 'error'))
      .finally(() => setToggleBusy(null));
  }

  function onTest(p: Provider) {
    setTestingId(p.id);
    api
      .testProvider(p.id)
      .then((res) => {
        const summary = resultSummary(res);
        const ok = typeof res === 'object' && res !== null && ((res as { ok?: boolean }).ok ?? (res as { results?: { ok: boolean }[] }).results?.every((r) => r.ok));
        toast(
          lang === 'tr' ? `${p.name}: ${summary || 'Test tamamlandı'}` : `${p.name}: ${summary || 'Test done'}`,
          ok === false ? 'error' : 'success',
        );
      })
      .catch((e) => toast(`${p.name}: ${errorMessage(e, lang === 'tr' ? 'Test başarısız' : 'Test failed')}`, 'error'))
      .finally(() => setTestingId(null));
  }

  function openKeyModal(p: Provider) {
    setKeyModalFor(p);
    setLabel(p.id.slice(0, 12));
    setCredential('');
    setBoundProxyId('');
    setDailyQuota('');
    setWeeklyQuota('');
    setPriority('100');
    setFormError('');
  }

  function openQuota(p: Provider) {
    setQuotaFor(p);
    setQuotaLoading(true);
    setQuotaError('');
    setQuotaData(null);
    api
      .providerQuota(p.id)
      .then(setQuotaData)
      .catch((e) => setQuotaError(errorMessage(e, lang === 'tr' ? 'Kota alınamadı' : 'Quota fetch failed')))
      .finally(() => setQuotaLoading(false));
  }

  function submitKey(e: FormEvent) {
    e.preventDefault();
    if (!keyModalFor) return;
    if (!credential.trim()) {
      setFormError(lang === 'tr' ? 'Credential zorunludur.' : 'Credential is required.');
      return;
    }
    setSubmitting(true);
    setFormError('');
    api
      .addProviderKey(keyModalFor.id, {
        label: label.trim() || undefined,
        credential: credential.trim(),
        // Empty selection = no proxy (direct egress) - sticky binding is optional.
        bound_proxy_id: boundProxyId ? Number(boundProxyId) : undefined,
        daily_quota: dailyQuota.trim() ? Number(dailyQuota) : undefined,
        weekly_quota: weeklyQuota.trim() ? Number(weeklyQuota) : undefined,
        priority: priority.trim() ? Number(priority) : 100,
      })
      .then(() => {
        toast(
          lang === 'tr' ? `${keyModalFor.name} için anahtar eklendi` : `Key added for ${keyModalFor.name}`,
          'success',
        );
        setKeyModalFor(null);
        load(false);
      })
      .catch((e) => setFormError(errorMessage(e, lang === 'tr' ? 'Anahtar eklenemedi' : 'Failed to add key')))
      .finally(() => setSubmitting(false));
  }

  const credentialHint = keyModalFor?.id === 'runner'
    ? L(lang, {
        en: 'Self-hosted Runner credential: {"base_url":"https://xxxx.ngrok.app","token":"…"} - start examples/runner.py in any GPU environment, or examples/modal_runner.py inside your Modal account.',
        tr: 'Self-hosted Runner credential: {"base_url":"https://xxxx.ngrok.app","token":"…"} - examples/runner.py dosyasını herhangi bir GPU ortamında başlatın (veya Modal hesabınızda examples/modal_runner.py).',
      })
    : keyModalFor?.id === 'kaggle'
      ? L(lang, {
          en: 'Kaggle API token: {"token":"KGAT_…"} (recommended - works with Bearer) or legacy kaggle.json {"username":"…","key":"…"}',
          tr: 'Kaggle API token\'ı: {"token":"KGAT_…"} (önerilen, Bearer ile çalışır) veya klasik kaggle.json {"username":"…","key":"…"}',
        })
      : keyModalFor?.id === 'modal'
        ? L(lang, {
            en: 'Modal credential: {"id":"ak-…","secret":"as-…"} - both tokens from `modal token new`. NOTE: Modal has no public job REST API; deploy examples/modal_runner.py to your Modal account and use Self-hosted Runner to run jobs on Modal compute.',
            tr: 'Modal credential: {"id":"ak-…","secret":"as-…"} - iki token da `modal token new` komutundan alınır. NOT: Modal public job REST API sunmaz; Modal üzerinde iş çalıştırmak için examples/modal_runner.py dosyasını Modal hesabınıza deploy edin ve Self-hosted Runner ile bağlayın.',
          })
        : keyModalFor?.id === 'saturn'
          ? L(lang, {
              en: 'Saturn Cloud credential: {"token":"…"} - Bearer access token from your Saturn Cloud instance (free community token at app.community.saturnenterprise.io). 150 free GPU+CPU hours/month on Hosted Free.',
              tr: 'Saturn Cloud credential: {"token":"…"} - Saturn Cloud hesabınızın Bearer access token\'ı (ücretsiz topluluk tokeni app.community.saturnenterprise.io üzerinden alınır). Hosted Free planında ayda 150 ücretsiz GPU+CPU saati.',
            })
          : L(lang, { en: 'Token or JSON credential (see README for this provider).', tr: 'Token veya JSON credential (sağlayıcıya göre README\'ye bakın).' });

  return (
    <div className="space-y-6">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">
            {L(lang, { en: 'Providers', tr: 'Sağlayıcılar' })}
          </h1>
          <p className="text-xs text-zinc-500">
            {L(lang, {
              en: 'Free-tier GPU compute providers and their credential health.',
              tr: 'Ücretsiz katman GPU sağlayıcıları ve credential durumları.',
            })}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
          <RefreshCw size={14} className={refreshing ? 'animate-spin' : ''} />
          {L(lang, { en: 'Refresh', tr: 'Yenile' })}
        </Button>
      </div>

      {loading ? (
        <LoadingBlock label={L(lang, { en: 'Loading providers…', tr: 'Sağlayıcılar yükleniyor…' })} />
      ) : error ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {providers.map((p) => {
            const caps = providerCapabilities(p);
            const enabled = p.enabled !== false;
            const keyCount = providerKeyCount(p);
            return (
              <Card key={p.id} className={`flex flex-col ${enabled ? '' : 'opacity-60'}`}>
                <div className="flex items-start justify-between gap-3 border-b border-zinc-800/80 px-5 py-4">
                  <div className="flex min-w-0 items-center gap-3">
                    <ProviderIcon id={p.id} size={20} />
                    <div className="min-w-0">
                      <h3 className="truncate text-sm font-semibold text-zinc-100">{p.name}</h3>
                      <code className="text-[11px] text-zinc-500">{p.id}</code>
                    </div>
                  </div>
                  <Toggle
                    checked={enabled}
                    onChange={(next) => onToggle(p, next)}
                    disabled={toggleBusy === p.id}
                    label={L(lang, { en: `Toggle ${p.name}`, tr: `${p.name} aç/kapat` })}
                  />
                </div>

                <CardContent className="flex flex-1 flex-col gap-4">
                  {/* Capabilities */}
                  <div>
                    <div className="label-base">{L(lang, { en: 'GPU capabilities', tr: 'GPU yetenekleri' })}</div>
                    <div className="flex flex-wrap gap-1.5">
                      {caps.length > 0 ? (
                        caps.map((c) => (
                          <Badge key={c} variant="info">{c}</Badge>
                        ))
                      ) : (
                        <span className="text-xs text-zinc-600">{L(lang, { en: 'No capability info', tr: 'Yetenek bilgisi yok' })}</span>
                      )}
                    </div>
                  </div>

                  {/* Key count */}
                  <div className="flex items-center justify-between rounded-lg border border-zinc-800 bg-zinc-900/50 px-3 py-2">
                    <span className="flex items-center gap-2 text-xs text-zinc-400">
                      <KeyRound size={13} className="text-zinc-500" /> {L(lang, { en: 'API keys', tr: 'API anahtarları' })}
                    </span>
                    <span className="text-sm font-bold tabular-nums text-zinc-100">{keyCount}</span>
                  </div>

                  {/* Actions */}
                  <div className="mt-auto flex flex-wrap items-center gap-2 pt-1">
                    <Button size="sm" variant="primary" onClick={() => openKeyModal(p)}>
                      <Plus size={13} /> {L(lang, { en: 'Add key', tr: 'Anahtar ekle' })}
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => onTest(p)} loading={testingId === p.id}>
                      <FlaskConical size={13} /> {L(lang, { en: 'Test', tr: 'Test' })}
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => openQuota(p)}>
                      <Gauge size={13} /> {L(lang, { en: 'Quota', tr: 'Kota' })}
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
        title={keyModalFor ? L(lang, { en: `Add key - ${keyModalFor.name}`, tr: `Anahtar ekle - ${keyModalFor.name}` }) : ''}
        wide
        footer={
          <>
            <Button variant="ghost" onClick={() => setKeyModalFor(null)}>
              {L(lang, { en: 'Cancel', tr: 'Vazgeç' })}
            </Button>
            <Button variant="primary" type="submit" form="add-key-form" loading={submitting}>
              {L(lang, { en: 'Add key', tr: 'Anahtar ekle' })}
            </Button>
          </>
        }
      >
        <form id="add-key-form" onSubmit={submitKey} className="space-y-4">
          <div>
            <Label htmlFor="key-label">{L(lang, { en: 'Label', tr: 'Etiket' })}</Label>
            <Input id="key-label" value={label} onChange={(e) => setLabel(e.target.value)} placeholder="my-kaggle-account" disabled={submitting} />
          </div>
          <div>
            <Label htmlFor="key-credential">{L(lang, { en: 'Credential *', tr: 'Credential *' })}</Label>
            <Textarea id="key-credential" value={credential} onChange={(e) => setCredential(e.target.value)} rows={4} placeholder="…" disabled={submitting} />
            <p className="mt-1.5 flex items-start gap-1.5 text-[11px] text-zinc-500"><Info size={12} className="mt-0.5 shrink-0" />{credentialHint}</p>
          </div>
          <div>
            <Label htmlFor="key-proxy">
              {L(lang, { en: 'Bound proxy (optional - leave empty for direct egress)', tr: 'Bağlı proxy (opsiyonel - boş bırakırsanız direkt bağlantı)' })}
            </Label>
            <Select id="key-proxy" value={boundProxyId} onChange={(e) => setBoundProxyId(e.target.value)} disabled={submitting}>
              <option value="">{L(lang, { en: '- Direct (no proxy)', tr: '- Direkt (proxy yok)' })}</option>
              {proxies.map((px) => (
                <option key={px.id} value={px.id}>
                  #{px.id} - {px.label} ({px.health_status ?? 'unknown'})
                </option>
              ))}
            </Select>
            <p className="mt-1.5 text-[11px] text-amber-500/90">
              {L(lang, {
                en: 'A bound proxy is pinned permanently (sticky 1:1) - it never rotates; your code calls go out via the same account IP forever. Choose direct for no proxy at all.',
                tr: 'Bağlanan proxy kalıcı olarak (sticky 1:1) sabitlenir - asla rotate edilmez; çağrılarınız hep aynı IP\'den çıkar. Hiç proxy istemiyorsanız direkt bırakın.',
              })}
            </p>
          </div>
          <div className="grid grid-cols-3 gap-3">
            <div>
              <Label htmlFor="key-daily">{L(lang, { en: 'Daily quota', tr: 'Günlük kota' })}</Label>
              <Input id="key-daily" type="number" min={0} value={dailyQuota} onChange={(e) => setDailyQuota(e.target.value)} placeholder="0 = ∞" disabled={submitting} />
            </div>
            <div>
              <Label htmlFor="key-weekly">{L(lang, { en: 'Weekly quota', tr: 'Haftalık kota' })}</Label>
              <Input id="key-weekly" type="number" min={0} value={weeklyQuota} onChange={(e) => setWeeklyQuota(e.target.value)} placeholder="0 = ∞" disabled={submitting} />
            </div>
            <div>
              <Label htmlFor="key-priority">{L(lang, { en: 'Priority', tr: 'Öncelik' })}</Label>
              <Input id="key-priority" type="number" min={1} value={priority} onChange={(e) => setPriority(e.target.value)} placeholder="100" disabled={submitting} />
            </div>
          </div>
          <p className="text-[11px] text-zinc-500">
            {L(lang, {
              en: 'Priority: lower wins. Keys are tried in priority order, then by your rotation strategy (Settings → Rotation).',
              tr: 'Öncelik: düşük değer önce denenir. Anahtarlar önce öncelik sırasına, sonra rotation stratejinize göre denenir (Ayarlar → Rotation).',
            })}
          </p>
          <FieldError>{formError}</FieldError>
        </form>
      </Modal>

      {/* Quota modal */}
      <Modal
        open={!!quotaFor}
        onClose={() => setQuotaFor(null)}
        title={quotaFor ? L(lang, { en: `Quota - ${quotaFor.name}`, tr: `Kota - ${quotaFor.name}` }) : ''}
        footer={<Button variant="ghost" onClick={() => setQuotaFor(null)}>{L(lang, { en: 'Close', tr: 'Kapat' })}</Button>}
      >
        {quotaLoading ? (
          <LoadingBlock label={L(lang, { en: 'Fetching quota…', tr: 'Kota alınıyor…' })} />
        ) : quotaError ? (
          <ErrorBlock message={quotaError} onRetry={() => quotaFor && openQuota(quotaFor)} />
        ) : (
          <pre className="max-h-96 overflow-auto rounded-lg border border-zinc-800 bg-zinc-950 p-3 text-[11px] leading-relaxed text-zinc-300">
            {JSON.stringify(quotaData, null, 2)}
          </pre>
        )}
      </Modal>
    </div>
  );
}
