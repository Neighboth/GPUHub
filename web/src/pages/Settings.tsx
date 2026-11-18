import { useCallback, useEffect, useState, type FormEvent } from 'react';
import {
  RotateCw,
  Gauge,
  Bell,
  KeySquare,
  Save,
  Loader2,
  AlertTriangle,
  CheckCircle2,
  XCircle,
  Info,
} from 'lucide-react';
import { api, errorMessage, resultSummary, type Settings as SettingsShape } from '../api';
import { Button, Card, CardContent, CardHeader, CardTitle, CardDescription, Input, Label, Select, FieldError, LoadingBlock, ErrorBlock, Badge } from '../components/ui';
import { useToast } from '../components/Toast';
import { useLang, L } from '../i18n';

type RotationOption = { value: string; enTitle: string; trTitle: string; enDesc: string; trDesc: string };

const ROTATION_OPTIONS: RotationOption[] = [
  {
    value: 'round_robin',
    enTitle: 'Round robin',
    trTitle: 'Round robin',
    enDesc: 'Alternates keys evenly. Simple and fair — good for spreading identical load across many accounts.',
    trDesc: 'Anahtarları eşit sırayla kullanır. Basit ve adil — aynı yükü birden çok hesaba yaymak için ideal.',
  },
  {
    value: 'least_used',
    enTitle: 'Least used',
    trTitle: 'En az kullanılan',
    enDesc: 'Always picks the key with the fewest runs this month — keeps every free tier warming evenly.',
    trDesc: 'Her zaman bu ay en az kullanılmış olanı seçer — tüm ücretsiz katmanları eşit kullanır.',
  },
  {
    value: 'most_quota',
    enTitle: 'Most quota',
    trTitle: 'En çok kotası kalan',
    enDesc: 'Picks the key with the most remaining daily/weekly allowance. Best overall throughput — recommended.',
    trDesc: 'Kalan günlük/haftalık hakkı en yüksek olanı seçer. En yüksek toplam verim — önerilir.',
  },
  {
    value: 'weighted',
    enTitle: 'Weighted',
    trTitle: 'Ağırlıklı',
    enDesc: 'Probability proportional to each key’s weight field — great when one account has 10× the quota of another.',
    trDesc: 'Her anahtarın weight değerine göre olasılık verir — bir hesap diğerinin 10 katı kotaya sahipse ideal.',
  },
];

export default function Settings() {
  const toast = useToast();
  const lang = useLang();

  const [raw, setRaw] = useState<SettingsShape | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const [rotation, setRotation] = useState('');
  const [scheduler, setScheduler] = useState('');
  const [watermark, setWatermark] = useState('');
  const [maxRetries, setMaxRetries] = useState('');
  const [webhookUrl, setWebhookUrl] = useState('');

  const [savingRotation, setSavingRotation] = useState(false);
  const [savingQuota, setSavingQuota] = useState(false);
  const [savingWebhook, setSavingWebhook] = useState(false);
  const [testingWebhook, setTestingWebhook] = useState(false);

  const [currentMasterKey, setCurrentMasterKey] = useState('');
  const [newMasterKey, setNewMasterKey] = useState('');
  const [rotating, setRotating] = useState(false);
  const [rotateResult, setRotateResult] = useState<{ ok: boolean; message: string } | null>(null);
  const [rotateError, setRotateError] = useState('');

  const load = useCallback(() => {
    setLoading(true);
    setError('');
    api
      .settings()
      .then((s) => {
        setRaw(s);
        setRotation(s.rotation_strategy ?? 'most_quota');
        setScheduler(s.scheduler_strategy ?? 'most_quota');
        setWatermark(s.quota_high_watermark_percent != null ? String(s.quota_high_watermark_percent) : '90');
        setMaxRetries(s.max_retries != null ? String(s.max_retries) : '3');
        setWebhookUrl(s.notifications_webhook_url ?? s.webhook_url ?? '');
      })
      .catch((e) => setError(errorMessage(e, lang === 'tr' ? 'Ayarlar yüklenemedi' : 'Failed to load settings')))
      .finally(() => setLoading(false));
  }, [lang]);

  useEffect(() => {
    load();
  }, [load]);

  async function saveSection(body: Partial<SettingsShape>, which: 'rotation' | 'quota' | 'webhook') {
    const setBusy = which === 'rotation' ? setSavingRotation : which === 'quota' ? setSavingQuota : setSavingWebhook;
    setBusy(true);
    try {
      await api.updateSettings(body);
      toast(L(lang, { en: 'Settings saved', tr: 'Ayarlar kaydedildi' }), 'success');
      load();
    } catch (e) {
      toast(errorMessage(e, L(lang, { en: 'Failed to save settings', tr: 'Ayarlar kaydedilemedi' })), 'error');
    } finally {
      setBusy(false);
    }
  }

  async function saveRotation(e: FormEvent) {
    e.preventDefault();
    await saveSection({ rotation_strategy: rotation, scheduler_strategy: scheduler }, 'rotation');
  }

  async function saveQuota(e: FormEvent) {
    e.preventDefault();
    const wm = Number(watermark);
    if (!Number.isFinite(wm) || wm < 1 || wm > 100) {
      toast(L(lang, { en: 'Watermark must be between 1 and 100 (%)', tr: 'Watermark 1–100 (%) arasında olmalı' }), 'error');
      return;
    }
    const retries = Number(maxRetries);
    if (!Number.isInteger(retries) || retries < 0) {
      toast(L(lang, { en: 'Max retries must be a non-negative integer', tr: 'Max retries negatif olmayan bir tam sayı olmalı' }), 'error');
      return;
    }
    await saveSection({ quota_high_watermark_percent: wm, max_retries: retries }, 'quota');
  }

  async function saveWebhook(e: FormEvent) {
    e.preventDefault();
    await saveSection({ notifications_webhook_url: webhookUrl.trim() }, 'webhook');
  }

  async function testWebhook() {
    setTestingWebhook(true);
    try {
      await api.testWebhook(webhookUrl.trim() || undefined);
      toast(L(lang, { en: 'Test webhook sent — check your channel', tr: 'Test webhook gönderildi — kanalınızı kontrol edin' }), 'success');
    } catch (e) {
      toast(errorMessage(e, L(lang, { en: 'Webhook test failed', tr: 'Webhook testi başarısız' })), 'error');
    } finally {
      setTestingWebhook(false);
    }
  }

  async function submitRotate(e: FormEvent) {
    e.preventDefault();
    setRotateError('');
    setRotateResult(null);
    if (!currentMasterKey.trim() || !newMasterKey.trim()) {
      setRotateError(L(lang, { en: 'Both current and new master key are required.', tr: 'Mevcut ve yeni master key ikisi de gerekli.' }));
      return;
    }
    if (newMasterKey.trim().length < 16) {
      setRotateError(L(lang, { en: 'New master key must be at least 16 characters.', tr: 'Yeni master key en az 16 karakter olmalı.' }));
      return;
    }
    setRotating(true);
    try {
      const res = await api.rotateMasterKey(currentMasterKey.trim(), newMasterKey.trim());
      setRotateResult({ ok: true, message: L(lang, {
        en: `${resultSummary(res)} — now set GPUHUB_MASTER_KEY=<new> in .env and restart GPUHub.`,
        tr: `${resultSummary(res)} — ardından GPUHUB_MASTER_KEY=<yeni> değerini .env’e yazıp GPUHub’ı yeniden başlatın.`,
      }) });
      setCurrentMasterKey('');
      setNewMasterKey('');
      toast(L(lang, { en: 'Master key rotated', tr: 'Master key döndürüldü' }), 'success');
    } catch (e2) {
      setRotateResult({ ok: false, message: errorMessage(e2, L(lang, { en: 'Master key rotation failed', tr: 'Master key rotasyonu başarısız' })) });
    } finally {
      setRotating(false);
    }
  }

  if (loading) return <LoadingBlock label={L(lang, { en: 'Loading settings…', tr: 'Ayarlar yükleniyor…' })} />;
  if (error) return <ErrorBlock message={error} onRetry={load} />;

  const selectedRotation = ROTATION_OPTIONS.find((o) => o.value === rotation);

  return (
    <div className="space-y-5">
      {/* Header */}
      <div>
        <h1 className="text-xl font-bold tracking-tight text-zinc-50">
          {L(lang, { en: 'Settings', tr: 'Ayarlar' })}
        </h1>
        <p className="text-xs text-zinc-500">
          {L(lang, {
            en: 'Instance-wide behavior: rotation, scheduling, quotas and notifications.',
            tr: 'Örnek geneli davranış: rotation, scheduler, kotalar ve bildirimler.',
          })}
        </p>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        {/* Rotation & scheduler */}
        <Card>
          <CardHeader>
            <div>
              <CardTitle className="flex items-center gap-2">
                <RotateCw size={14} className="text-emerald-400" /> {L(lang, { en: 'Rotation & scheduler', tr: 'Rotation & scheduler' })}
              </CardTitle>
              <CardDescription>
                {L(lang, {
                  en: 'How keys are picked and how jobs are placed.',
                  tr: 'Anahtarlar nasıl seçilir, işler nereye yerleştirilir.',
                })}
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <form onSubmit={saveRotation} className="space-y-5">
              <div>
                <Label htmlFor="rotation-strategy">{L(lang, { en: 'Key rotation strategy', tr: 'Anahtar rotation stratejisi' })}</Label>
                <Select id="rotation-strategy" value={rotation} onChange={(e) => setRotation(e.target.value)}>
                  {ROTATION_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>{o.value}</option>
                  ))}
                </Select>
                {/* Live description of the selected strategy */}
                {selectedRotation && (
                  <div className="mt-2 rounded-lg border border-emerald-500/20 bg-emerald-500/5 px-3 py-2 text-[11px] leading-relaxed text-emerald-300">
                    <div className="font-semibold">{lang === 'tr' ? selectedRotation.trTitle : selectedRotation.enTitle}</div>
                    {lang === 'tr' ? selectedRotation.trDesc : selectedRotation.enDesc}
                  </div>
                )}
                <div className="mt-2 space-y-1.5 text-[11px] text-zinc-500">
                  <p className="flex items-start gap-1.5"><Info size={12} className="mt-0.5 shrink-0" />
                    {L(lang, { en: 'Rotation picks WHICH key handles the request — proxies never rotate.', tr: 'Rotation HANGİ anahtarın kullanılacağına karar verir — proxy asla rotate edilmez.' })}</p>
                  <p className="flex items-start gap-1.5"><Info size={12} className="mt-0.5 shrink-0" />
                    {L(lang, { en: 'Keys are tried in priority order first, then by this strategy.', tr: 'Anahtarlar önce priority sırasına, sonra bu stratejiye göre denenir.' })}</p>
                </div>
              </div>

              <div>
                <Label htmlFor="scheduler-strategy">{L(lang, { en: 'Scheduler selection (provider choice)', tr: 'Scheduler seçimi (sağlayıcı tercihi)' })}</Label>
                <Select id="scheduler-strategy" value={scheduler} onChange={(e) => setScheduler(e.target.value)}>
                  {ROTATION_OPTIONS.map((o) => (
                    <option key={o.value} value={o.value}>{o.value}</option>
                  ))}
                </Select>
                <div className="mt-2 space-y-1.5 text-[11px] text-zinc-500">
                  <p className="flex items-start gap-1.5"><Info size={12} className="mt-0.5 shrink-0" />
                    {L(lang, {
                      en: 'Scheduler picks the PROVIDER+KEY pair for a job: same 4 strategies, applied after GPU-type/VRAM/count filtering.',
                      tr: 'Scheduler, bir iş için SAĞLAYICI+ANAHTAR çiftini seçer: aynı 4 strateji, GPU tipi/VRAM/GPU adedi filtresinden SONRA uygulanır.',
                    })}</p>
                  <p className="flex items-start gap-1.5"><Info size={12} className="mt-0.5 shrink-0" />
                    {L(lang, {
                      en: 'GPU filter rules: exact type (or “any”) → VRAM ≥ min_vram_gb → GPU count ≤ offering max → lowest-VRAM fitting GPU wins → quota watermark ≥90% skips the key.',
                      tr: 'GPU filtresi: exact tip (veya “any”) → VRAM ≥ min_vram_gb → adet ≤ sunulan max → en düşük VRAMli uygun GPU seçilir → kota watermark ≥%90 anahtarı atlar.',
                    })}</p>
                </div>
              </div>

              <div className="flex justify-end">
                <Button type="submit" variant="primary" loading={savingRotation}>
                  <Save size={14} /> {L(lang, { en: 'Save strategies', tr: 'Stratejileri kaydet' })}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>

        {/* Quota & retries */}
        <Card>
          <CardHeader>
            <div>
              <CardTitle className="flex items-center gap-2">
                <Gauge size={14} className="text-cyan-400" /> {L(lang, { en: 'Quota & limits', tr: 'Kota & limitler' })}
              </CardTitle>
              <CardDescription>
                {L(lang, { en: 'High watermark and failover retries.', tr: 'High watermark ve failover denemesi.' })}
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <form onSubmit={saveQuota} className="space-y-4">
              <div>
                <Label htmlFor="watermark">{L(lang, { en: 'Quota high watermark (%)', tr: 'Kota high watermark (%)' })}</Label>
                <Input id="watermark" type="number" min={1} max={100} placeholder="90" value={watermark} onChange={(e) => setWatermark(e.target.value)} />
                <p className="mt-1.5 text-[11px] text-zinc-500">
                  {L(lang, {
                    en: 'A key whose provider-reported quota reaches this % is skipped during scheduling (default 90).',
                    tr: 'Sağlayıcının bildirdiği kota bu % seviyesine ulaşırsa anahtar scheduling sırasında atlanır (varsayılan 90).',
                  })}
                </p>
              </div>
              <div>
                <Label htmlFor="max-retries">{L(lang, { en: 'Max retries (failover)', tr: 'Max failover denemesi' })}</Label>
                <Input id="max-retries" type="number" min={0} step={1} placeholder="3" value={maxRetries} onChange={(e) => setMaxRetries(e.target.value)} />
                <p className="mt-1.5 text-[11px] text-zinc-500">
                  {L(lang, {
                    en: 'How many times a failing/timing-out attempt is retried on the NEXT provider/key before the job is marked failed (or unschedulable).',
                    tr: 'Hata/timeout alan bir denemenin, iş failed (veya unschedulable) olarak işaretlenmeden ÖNCE bir sonraki sağlayıcı/anahtar ile kaç kez deneneceği.',
                  })}
                </p>
              </div>
              <div className="flex justify-end">
                <Button type="submit" variant="primary" loading={savingQuota}>
                  <Save size={14} /> {L(lang, { en: 'Save limits', tr: 'Limitleri kaydet' })}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>

        {/* Notifications webhook (Discord embed support) */}
        <Card>
          <CardHeader>
            <div>
              <CardTitle className="flex items-center gap-2">
                <Bell size={14} className="text-amber-400" /> {L(lang, { en: 'Notifications webhook', tr: 'Bildirim webhook’u' })}
              </CardTitle>
              <CardDescription>
                {L(lang, {
                  en: 'Fires on job failure, quota exhaustion, key auto-disable and proxy health failures.',
                  tr: 'İş hatası, kota tükenmesi, anahtar oto-pasifleştirme ve proxy sağlık sorunlarında tetiklenir.',
                })}
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <form onSubmit={saveWebhook} className="space-y-4">
              <div>
                <Label htmlFor="webhook-url">{L(lang, { en: 'Webhook URL', tr: 'Webhook URL' })}</Label>
                <Input id="webhook-url" type="url" placeholder="https://discord.com/api/webhooks/… or https://hooks.slack.com/services/…" value={webhookUrl} onChange={(e) => setWebhookUrl(e.target.value)} className="font-mono text-xs" />
                <p className="mt-1.5 text-[11px] text-zinc-500">
                  {L(lang, {
                    en: 'Discord webhooks are auto-detected and sent as a colored embed (red=critical, yellow=warn, green=info, username “GPUHub”). Any other URL gets a plain JSON POST.',
                    tr: 'Discord webhook’ları otomatik algılanır ve renkli embed olarak gönderilir (kırmızı=critical, sarı=warn, yeşil=info, kullanıcı adı “GPUHub”). Diğer URL’lere düz JSON POST gider.',
                  })}
                </p>
              </div>
              <div className="flex flex-wrap items-center justify-end gap-2">
                <Button type="button" variant="outline" onClick={testWebhook} loading={testingWebhook}>
                  <Bell size={14} /> {L(lang, { en: 'Send test', tr: 'Test gönder' })}
                </Button>
                <Button type="submit" variant="primary" loading={savingWebhook}>
                  <Save size={14} /> {L(lang, { en: 'Save webhook', tr: 'Webhook’u kaydet' })}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>

        {/* Master key rotation */}
        <Card className="border-red-500/20">
          <CardHeader>
            <div>
              <CardTitle className="flex items-center gap-2 text-red-300">
                <KeySquare size={14} /> {L(lang, { en: 'Master key rotation', tr: 'Master key rotasyonu' })}
              </CardTitle>
              <CardDescription>
                {L(lang, { en: 'Re-encrypts all stored provider credentials with the new master key.', tr: 'Tüm sağlayıcı credential’larını yeni master key ile yeniden şifreler.' })}
              </CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <div className="mb-4 flex items-start gap-2.5 rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2.5 text-[11px] leading-relaxed text-amber-300">
              <AlertTriangle size={14} className="mt-0.5 shrink-0" />
              {L(lang, {
                en: 'Destructive if mishandled: after rotation you must set GPUHUB_MASTER_KEY to the new value and restart — otherwise nothing decrypts. Back up first.',
                tr: 'Yanlış kullanılırsa yıkıcıdır: rotasyondan sonra GPUHUB_MASTER_KEY’i yeni değerle değiştirip yeniden başlatmanız gerekir — yoksa hiçbir şey çözülemez. Önce yedekleyin.',
              })}
            </div>
            <form onSubmit={submitRotate} className="space-y-4">
              <div>
                <Label htmlFor="current-master-key">{L(lang, { en: 'Current master key', tr: 'Mevcut master key' })}</Label>
                <Input id="current-master-key" type="password" autoComplete="off" placeholder={L(lang, { en: 'Current master key', tr: 'Mevcut master key' })} value={currentMasterKey} onChange={(e) => setCurrentMasterKey(e.target.value)} disabled={rotating} />
              </div>
              <div>
                <Label htmlFor="new-master-key">{L(lang, { en: 'New master key', tr: 'Yeni master key' })}</Label>
                <Input id="new-master-key" type="password" autoComplete="off" placeholder={L(lang, { en: 'At least 16 characters', tr: 'En az 16 karakter' })} value={newMasterKey} onChange={(e) => setNewMasterKey(e.target.value)} disabled={rotating} />
              </div>
              <FieldError>{rotateError}</FieldError>
              {rotateResult && (
                <div
                  className={`flex items-start gap-2 rounded-lg border px-3 py-2 text-xs ${
                    rotateResult.ok ? 'border-emerald-500/25 bg-emerald-500/5 text-emerald-300' : 'border-red-500/25 bg-red-500/5 text-red-300'
                  }`}
                >
                  {rotateResult.ok ? <CheckCircle2 size={14} className="mt-0.5 shrink-0" /> : <XCircle size={14} className="mt-0.5 shrink-0" />}
                  <span>{rotateResult.message}</span>
                </div>
              )}
              <div className="flex justify-end">
                <Button type="submit" variant="danger" loading={rotating}>
                  {rotating ? <Loader2 size={14} className="animate-spin" /> : <KeySquare size={14} />}
                  {L(lang, { en: 'Rotate master key', tr: 'Master key’i döndür' })}
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
