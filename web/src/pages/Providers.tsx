import { useCallback, useEffect, useState, type FormEvent } from 'react';
import {
  Server,
  KeyRound,
  FlaskConical,
  Gauge,
  Plus,
  RefreshCw,
  ExternalLink,
  Info,
} from 'lucide-react';
import {
  api,
  errorMessage,
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
  Toggle,
  LoadingBlock,
  ErrorBlock,
  EmptyState,
  FieldError,
} from '../components/ui';
import { useToast } from '../components/Toast';
import { useLang, L } from '../i18n';

type CredField = {
  key: string;
  label: { en: string; tr: string };
  placeholder: string;
  secret?: boolean;
};

// Per-provider credential boxes. Each box holds exactly ONE piece of information.
// The values are assembled into the JSON credential the API expects on submit.
function credentialFieldsFor(providerId: string): CredField[] {
  switch (providerId) {
    case 'kaggle':
      return [
        { key: 'token', label: { en: 'API token (KGAT_…)', tr: 'API token (KGAT_…)' }, placeholder: 'KGAT_…', secret: true },
        { key: 'username', label: { en: 'Kaggle username (optional)', tr: 'Kaggle kullanıcı adı (opsiyonel)' }, placeholder: 'kullanici-adiniz' },
      ];
    case 'colab':
      return [
        {
          key: 'token',
          label: {
            en: 'Redirected localhost URL (http://localhost…/?code=4/…), OAuth code (4/…), or refresh token (1//…)',
            tr: 'Yönlendirilen localhost URL’si (http://localhost…/?code=4/…), yetki kodu (4/…) veya refresh token (1//…)',
          },
          placeholder: 'http://localhost:8080/oauth/callback?code=4/0A… veya 4/0A…',
          secret: false,
        },
      ];
    case 'modal':
      return [
        { key: 'id', label: { en: 'Token ID (ak-…)', tr: 'Token ID (ak-…)' }, placeholder: 'ak-…' },
        { key: 'secret', label: { en: 'Token secret (as-…)', tr: 'Token secret (as-…)' }, placeholder: 'as-…', secret: true },
      ];
    case 'saturn':
      return [
        { key: 'token', label: { en: 'Bearer access token', tr: 'Bearer erişim tokenı' }, placeholder: 'eyJhbG…', secret: true },
      ];
    case 'paperspace':
      return [
        { key: 'key', label: { en: 'API key', tr: 'API anahtarı' }, placeholder: 'x-api-key', secret: true },
      ];
    case 'hf-spaces':
      return [
        { key: 'token', label: { en: 'Hugging Face token', tr: 'Hugging Face token' }, placeholder: 'hf_…', secret: true },
      ];
    case 'runner':
      return [
        { key: 'base_url', label: { en: 'Runner URL', tr: 'Runner URL' }, placeholder: 'https://…' },
        { key: 'token', label: { en: 'Runner token', tr: 'Runner token' }, placeholder: '••••••••', secret: true },
      ];
    default:
      return [
        { key: 'token', label: { en: 'Token', tr: 'Token' }, placeholder: '…', secret: true },
      ];
  }
}

/** Render quota/test payloads as itemized rows (never a raw JSON blob). */
function ItemizedResult({ data }: { data: unknown }) {
  const rows: { label: string; value: string; tone?: 'ok' | 'bad' }[] = [];
  const obj = (data ?? {}) as Record<string, unknown>;
  const items = (obj.keys ?? obj.results) as unknown[] | undefined;
  if (Array.isArray(items)) {
    items.forEach((it, i) => {
      const r = (it ?? {}) as Record<string, unknown>;
      const name = String(r.label ?? r.key_label ?? r.provider_id ?? `#${i + 1}`);
      if ('ok' in r) {
        rows.push({ label: name, value: r.ok ? 'OK' : String(r.error ?? 'Failed'), tone: r.ok ? 'ok' : 'bad' });
      } else if ('quota' in r) {
        const q = (r.quota ?? {}) as Record<string, unknown>;
        const used = q.daily_used ?? q.DailyUsed ?? 0;
        const limit = q.daily_limit ?? q.DailyLimit ?? 0;
        rows.push({ label: name, value: `used ${String(used)} / limit ${String(limit)}` + (q.raw ? ` - ${String(q.raw)}` : '') });
      } else if ('error' in r) {
        rows.push({ label: name, value: String(r.error), tone: 'bad' });
      } else {
        rows.push({ label: name, value: Object.entries(r).map(([k, v]) => `${k}=${String(v)}`).join(', ') });
      }
    });
  } else if (typeof obj === 'object') {
    Object.entries(obj).forEach(([k, v]) => {
      if (v && typeof v === 'object') rows.push({ label: k, value: JSON.stringify(v) });
      else rows.push({ label: k, value: String(v) });
    });
  } else {
    rows.push({ label: 'Result', value: String(data) });
  }
  if (rows.length === 0) return <p className="text-sm text-zinc-500">No data.</p>;
  return (
    <div className="divide-y divide-zinc-800/80">
      {rows.map((r, i) => (
        <div key={i} className="flex items-start justify-between gap-4 py-2 text-xs">
          <span className="font-medium text-zinc-300">{r.label}</span>
          <span className={`max-w-[70%] text-right break-words ${r.tone === 'ok' ? 'text-emerald-400' : r.tone === 'bad' ? 'text-red-400' : 'text-zinc-400'}`}>{r.value}</span>
        </div>
      ))}
    </div>
  );
}

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
  const [credValues, setCredValues] = useState<Record<string, string>>({});
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

  // Test results modal state (itemized per key, never raw JSON)
  const [testFor, setTestFor] = useState<Provider | null>(null);
  const [testData, setTestData] = useState<unknown>(null);
  const [testLoading, setTestLoading] = useState(false);
  const [testError, setTestError] = useState('');

  const [toggleBusy, setToggleBusy] = useState<string | null>(null);
  const [colabOAuthUrl, setColabOAuthUrl] = useState<string>('');

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

  useEffect(() => {
    function onMsg(ev: MessageEvent) {
      if (ev.data && typeof ev.data === 'object' && ev.data.type === 'gpuhub_colab_code' && ev.data.code) {
        setCredValues((prev) => ({ ...prev, token: String(ev.data.code) }));
      }
    }
    window.addEventListener('message', onMsg);
    return () => window.removeEventListener('message', onMsg);
  }, []);

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
    setTestFor(p);
    setTestData(null);
    setTestError('');
    setTestLoading(true);
    api
      .testProvider(p.id)
      .then(setTestData)
      .catch((e) => setTestError(errorMessage(e, lang === 'tr' ? 'Test başarısız' : 'Test failed')))
      .finally(() => setTestLoading(false));
  }

  function openKeyModal(p: Provider) {
    setKeyModalFor(p);
    setLabel(p.id.slice(0, 12));
    setCredValues({});
    setBoundProxyId('');
    setDailyQuota('');
    setWeeklyQuota('');
    setPriority('100');
    setFormError('');
    if (p.id === 'colab') {
      api.colabOAuthURL().then((r) => setColabOAuthUrl(r.url)).catch(() => {});
    }
  }

  function handleCredInput(fieldKey: string, val: string) {
    const trimmed = val.trim();
    if (trimmed.startsWith('{') && trimmed.endsWith('}') && keyModalFor) {
      try {
        const parsed = JSON.parse(trimmed) as Record<string, unknown>;
        const fields = credentialFieldsFor(keyModalFor.id);
        const next: Record<string, string> = { ...credValues };
        let matched = false;
        for (const f of fields) {
          if (parsed[f.key] != null) {
            next[f.key] = String(parsed[f.key]);
            matched = true;
          }
        }
        if (matched) {
          setCredValues(next);
          return;
        }
      } catch {
        /* not valid JSON yet, treat as normal text */
      }
    }
    setCredValues((prev) => ({ ...prev, [fieldKey]: val }));
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
    const fields = credentialFieldsFor(keyModalFor.id);
    const cred: Record<string, string> = {};
    for (const f of fields) {
      const v = (credValues[f.key] ?? '').trim();
      if (v) cred[f.key] = v;
    }
    if (Object.keys(cred).length === 0) {
      setFormError(lang === 'tr' ? 'En az bir bilgi kutusunu doldurun.' : 'Fill at least one credential field.');
      return;
    }
    setSubmitting(true);
    setFormError('');
    api
      .addProviderKey(keyModalFor.id, {
        label: label.trim() || undefined,
        credential: JSON.stringify(cred),
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

                  {/* Key count & notes */}
                  <div className="space-y-2">
                    <div className="flex items-center justify-between rounded-lg border border-zinc-800 bg-zinc-900/50 px-3 py-2">
                      <span className="flex items-center gap-2 text-xs text-zinc-400">
                        <KeyRound size={13} className="text-zinc-500" /> {L(lang, { en: 'API keys', tr: 'API anahtarları' })}
                      </span>
                      <span className="text-sm font-bold tabular-nums text-zinc-100">
                        {p.active_keys != null ? (
                          <span>{p.active_keys} <span className="text-xs font-normal text-zinc-500">/ {keyCount}</span></span>
                        ) : keyCount}
                      </span>
                    </div>
                    {!Array.isArray(p.capabilities) && p.capabilities?.free_tier_notes && (
                      <p className="line-clamp-2 text-[11px] leading-relaxed text-zinc-500" title={p.capabilities.free_tier_notes}>
                        {p.capabilities.free_tier_notes}
                      </p>
                    )}
                  </div>

                  {/* Actions */}
                  <div className="mt-auto flex flex-wrap items-center gap-2 pt-1">
                    <Button size="sm" variant="primary" onClick={() => openKeyModal(p)}>
                      <Plus size={13} /> {L(lang, { en: 'Add key', tr: 'Anahtar ekle' })}
                    </Button>
                    <Button size="sm" variant="secondary" onClick={() => onTest(p)}>
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
          <div className="space-y-3">
            <p className="text-xs font-medium text-zinc-300">{L(lang, { en: 'Credential fields', tr: 'Credential alanları' })}</p>
            {keyModalFor?.id === 'colab' && (
              <div className="space-y-2.5 rounded-xl border border-emerald-500/25 bg-emerald-500/[0.06] p-3.5">
                <div className="text-xs font-semibold text-emerald-300">
                  {L(lang, { en: 'Google Colab OAuth2 (No App Password Needed)', tr: 'Google Colab OAuth2 (Uygulama Şifresi Gerekmez)' })}
                </div>
                <p className="whitespace-pre-line text-[11px] leading-relaxed text-zinc-400">
                  {L(lang, {
                    en: '1. Click the button below and sign in with your Google account.\n2. Google will redirect your browser to http://localhost:8080/oauth/callback?code=4/0A… (even if that page says "This site can’t be reached" when GPUHub is on a remote server/CasaOS, that is normal!).\n3. Copy the entire http://localhost:8080/oauth/callback?code=… URL from your browser’s address bar and paste it into the box below.',
                    tr: '1. Aşağıdaki butona tıklayıp Google hesabınızla giriş yapın ve izin verin.\n2. Google tarayıcınızı http://localhost:8080/oauth/callback?code=4/0A… adresine yönlendirecektir (GPUHub CasaOS/uzak sunucuda çalışıyorsa "Bu siteye ulaşılamıyor" yazması normaldir!).\n3. Tarayıcınızın en üstündeki adres çubuğundan o http://localhost:8080/oauth/callback?code=… URL’sinin tamamını kopyalayıp alttaki kutuya yapıştırın.',
                  })}
                </p>
                {colabOAuthUrl && (
                  <a
                    href={colabOAuthUrl}
                    target="_blank"
                    rel="noreferrer"
                    className="inline-flex items-center gap-1.5 rounded-lg border border-emerald-500/40 bg-emerald-500/15 px-3 py-1.5 text-xs font-semibold text-emerald-300 transition-colors hover:bg-emerald-500/25"
                  >
                    <ExternalLink size={13} />
                    {L(lang, { en: '1. Get Google OAuth Code (New Tab)', tr: '1. Google OAuth Yetki Kodu Al (Yeni Sekme)' })}
                  </a>
                )}
              </div>
            )}
            {keyModalFor && credentialFieldsFor(keyModalFor.id).map((f) => (
              <div key={f.key}>
                <Label htmlFor={`key-cred-${f.key}`}>{L(lang, f.label)}</Label>
                <Input
                  id={`key-cred-${f.key}`}
                  type={f.secret ? 'password' : 'text'}
                  autoComplete="off"
                  value={credValues[f.key] ?? ''}
                  onChange={(e) => handleCredInput(f.key, e.target.value)}
                  placeholder={f.placeholder}
                  disabled={submitting}
                />
              </div>
            ))}
            <p className="mt-1.5 flex items-start gap-1.5 text-[11px] text-zinc-500"><Info size={12} className="mt-0.5 shrink-0" />{L(lang, { en: 'Tip: You can also paste raw JSON directly into any box (e.g. {"id":"ak-...","secret":"as-..."}) to auto-fill all fields.', tr: 'İpucu: Herhangi bir kutuya doğrudan JSON yapıştırabilirsiniz (örn. {"id":"ak-...","secret":"as-..."}); alanlar otomatik doldurulur.' })}</p>
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
          <ItemizedResult data={quotaData} />
        )}
      </Modal>

      {/* Test results modal - itemized per key */}
      <Modal
        open={!!testFor}
        onClose={() => setTestFor(null)}
        title={testFor ? L(lang, { en: `Test - ${testFor.name}`, tr: `Test - ${testFor.name}` }) : ''}
        footer={<Button variant="ghost" onClick={() => setTestFor(null)}>{L(lang, { en: 'Close', tr: 'Kapat' })}</Button>}
      >
        {testLoading ? (
          <LoadingBlock label={L(lang, { en: 'Testing keys…', tr: 'Anahtarlar test ediliyor…' })} />
        ) : testError ? (
          <ErrorBlock message={testError} onRetry={() => testFor && onTest(testFor)} />
        ) : (
          <ItemizedResult data={testData} />
        )}
      </Modal>
    </div>
  );
}
