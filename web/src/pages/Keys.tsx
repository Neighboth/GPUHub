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
  Unplug,
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
import ProviderIcon from '../components/ProviderIcon';
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
import { useLang, L } from '../i18n';
import { fmtDateTime, isFuture } from '../lib/format';

export default function Keys() {
  const toast = useToast();
  const lang = useLang();

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

  // Reveal credential modal
  const [revealTarget, setRevealTarget] = useState<ApiKey | null>(null);
  const [revealValue, setRevealValue] = useState('');
  const [revealLoading, setRevealLoading] = useState(false);

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
      .catch((e) => setError(errorMessage(e, lang === 'tr' ? 'Anahtarlar yüklenemedi' : 'Failed to load keys')))
      .finally(() => {
        setLoading(false);
        setRefreshing(false);
      });
  }, [lang]);

  useEffect(() => {
    load();
  }, [load]);

  async function onToggle(k: ApiKey, next: boolean) {
    setToggleBusy(k.id);
    setItems((prev) => prev.map((x) => (x.id === k.id ? { ...x, active: next } : x)));
    try {
      await api.updateKey(k.id, next);
      toast(
        lang === 'tr'
          ? `${k.label} ${next ? 'etkinleştirildi' : 'devre dışı bırakıldı'}`
          : `${k.label} ${next ? 'activated' : 'deactivated'}`,
        'success',
      );
    } catch (e) {
      setItems((prev) => prev.map((x) => (x.id === k.id ? { ...x, active: !next } : x)));
      toast(errorMessage(e, lang === 'tr' ? 'Güncellenemedi' : 'Failed to update key'), 'error');
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
      toast(`${k.label} ${lang === 'tr' ? 'test başarısız' : 'test failed'}: ${errorMessage(e)}`, 'error');
    } finally {
      setTestingId(null);
    }
  }

  function openReassign(k: ApiKey) {
    setReassignTarget(k);
    setReassignProxyId('');
  }

  async function openReveal(k: ApiKey) {
    setRevealTarget(k);
    setRevealValue('');
    setRevealLoading(true);
    try {
      const res = await api.revealKey(k.id);
      setRevealValue(res.credential ?? '');
    } catch (e) {
      toast(errorMessage(e, lang === 'tr' ? 'Credential alınamadı' : 'Failed to reveal credential'), 'error');
    } finally {
      setRevealLoading(false);
    }
  }

  async function submitReassign() {
    if (!reassignTarget) return;
    setReassigning(true);
    try {
      // Empty selection = unbind (key switches to direct egress).
      await api.reassignKeyProxy(reassignTarget.id, reassignProxyId === '' ? '0' : reassignProxyId);
      toast(
        lang === 'tr'
          ? `${reassignTarget.label} için proxy bağlaması güncellendi`
          : `Proxy binding updated for ${reassignTarget.label}`,
        'success',
      );
      setReassignTarget(null);
      load(false);
    } catch (e) {
      toast(errorMessage(e, lang === 'tr' ? 'Proxy yeniden atanamadı' : 'Failed to reassign proxy'), 'error');
    } finally {
      setReassigning(false);
    }
  }

  async function confirmDelete() {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await api.deleteKey(deleteTarget.id);
      toast(lang === 'tr' ? `${deleteTarget.label} silindi` : `${deleteTarget.label} deleted`, 'success');
      setDeleteTarget(null);
      load(false);
    } catch (e) {
      toast(errorMessage(e, lang === 'tr' ? 'Anahtar silinemedi' : 'Failed to delete key'), 'error');
    } finally {
      setDeleting(false);
    }
  }

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">
            {L(lang, { en: 'Keys', tr: 'Anahtarlar' })}
          </h1>
          <p className="text-xs text-zinc-500">
            {L(lang, { en: 'Every provider credential managed by this GPUHub instance.', tr: 'Bu GPUHub örneğinin yönettiği tüm sağlayıcı credential’ları.' })}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
          <RefreshCw size={14} />
          {L(lang, { en: 'Refresh', tr: 'Yenile' })}
        </Button>
      </div>

      {/* Rotation strategy card */}
      <Card>
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <RotateCw size={14} className="text-emerald-400" /> {L(lang, { en: 'Rotation strategy', tr: 'Rotation stratejisi' })}
          </CardTitle>
          <Link
            to="/settings"
            className="flex items-center gap-0.5 text-[11px] font-medium uppercase tracking-wider text-emerald-400 hover:text-emerald-300"
          >
            {L(lang, { en: 'Configure in Settings', tr: 'Ayarlar’da yapılandır' })} <ChevronRight size={12} />
          </Link>
        </CardHeader>
        <CardContent className="flex flex-wrap items-center gap-x-10 gap-y-3">
          <div>
            <div className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">
              {L(lang, { en: 'Key rotation', tr: 'Anahtar rotation’ı' })}
            </div>
            <div className="mt-1">
              <Badge variant="accent">{settings?.rotation_strategy ?? '-'}</Badge>
            </div>
          </div>
          <div>
            <div className="text-[10px] font-semibold uppercase tracking-wider text-zinc-500">
              {L(lang, { en: 'Scheduler', tr: 'Scheduler' })}
            </div>
            <div className="mt-1">
              <Badge variant="info">{settings?.scheduler_strategy ?? '-'}</Badge>
            </div>
          </div>
          <p className="max-w-md text-[11px] leading-relaxed text-zinc-500">
            {L(lang, {
              en: 'Read-only view. Rotation picks WHICH key (never the proxy). Every rotation strategy is explained in Settings → Rotation & scheduler.',
              tr: 'Salt-okunur görünüm. Rotation HANGİ anahtarı seçeceğini belirler (asla proxy’yi değil). Tüm stratejilerin açıklaması: Ayarlar → Rotation & scheduler.',
            })}
          </p>
        </CardContent>
      </Card>

      {loading ? (
        <LoadingBlock label={L(lang, { en: 'Loading keys…', tr: 'Anahtarlar yükleniyor…' })} />
      ) : error ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : items.length === 0 ? (
        <EmptyState
          icon={KeyRound}
          title={L(lang, { en: 'No keys yet', tr: 'Henüz anahtar yok' })}
          subtitle={L(lang, { en: 'Add a key from the Providers page (proxy optional).', tr: 'Providers sayfasından bir anahtar ekleyin (proxy opsiyonel).' })}
          action={
            <Link to="/providers">
              <Button size="sm" variant="primary">
                {L(lang, { en: 'Go to Providers', tr: 'Sağlayıcılara git' })} <ChevronRight size={14} />
              </Button>
            </Link>
          }
        />
      ) : (
        <Table>
          <THead>
            <Tr>
              <Th>{L(lang, { en: 'Label', tr: 'Etiket' })}</Th>
              <Th>{L(lang, { en: 'Provider', tr: 'Sağlayıcı' })}</Th>
              <Th>Credential</Th>
              <Th>{L(lang, { en: 'Bound proxy', tr: 'Bağlı proxy' })}</Th>
              <Th>{L(lang, { en: 'Pri', tr: 'Öncelik' })}</Th>
              <Th>{L(lang, { en: 'Daily quota', tr: 'Günlük kota' })}</Th>
              <Th>{L(lang, { en: 'Weekly quota', tr: 'Haftalık kota' })}</Th>
              <Th>{L(lang, { en: 'Last used', tr: 'Son kullanım' })}</Th>
              <Th>{L(lang, { en: 'Errors', tr: 'Hatalar' })}</Th>
              <Th>{L(lang, { en: 'Cooldown until', tr: 'Cooldown bitişi' })}</Th>
              <Th>{L(lang, { en: 'Active', tr: 'Aktif' })}</Th>
              <Th className="text-right">{L(lang, { en: 'Actions', tr: 'İşlemler' })}</Th>
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
                    <code className="text-[10px] text-zinc-600">{String(k.id).slice(0, 8)}</code>
                  </Td>
                  <Td className="text-xs text-zinc-300">
                    <span className="flex items-center gap-2">
                      <ProviderIcon id={k.provider_id ?? ''} size={14} />
                      {k.provider_name ?? k.provider ?? k.provider_id ?? '-'}
                    </span>
                  </Td>
                  <Td>
                    <code className="text-[10px] text-zinc-500">{keyMaskedCredential(k)}</code>
                  </Td>
                  <Td>
                    {proxyLabel ? (
                      <span className="text-xs text-zinc-300">{proxyLabel}</span>
                    ) : (
                      <Badge variant="neutral">
                        {L(lang, { en: 'Direct (no proxy)', tr: 'Direkt (proxy yok)' })}
                      </Badge>
                    )}
                  </Td>
                  <Td className="tabular-nums text-xs text-zinc-400">{k.priority ?? '-'}</Td>
                  <Td className="min-w-[110px]"><QuotaCell used={k.usage_today} quota={k.daily_quota} /></Td>
                  <Td className="min-w-[110px]"><QuotaCell used={k.usage_week} quota={k.weekly_quota} /></Td>
                  <Td className="text-[11px] text-zinc-500">{k.last_used_at ? fmtDateTime(k.last_used_at) : '-'}</Td>
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
                      <span className="text-zinc-600">-</span>
                    )}
                  </Td>
                  <Td>
                    <Toggle
                      checked={active}
                      onChange={(next) => onToggle(k, next)}
                      disabled={toggleBusy === k.id}
                      label={L(lang, { en: `Toggle ${k.label}`, tr: `${k.label} aç/kapat` })}
                    />
                  </Td>
                  <Td>
                    <div className="flex items-center justify-end gap-1">
                      <Button size="sm" variant="ghost" onClick={() => onTest(k)} loading={testingId === k.id}
                        title={L(lang, { en: 'Test key', tr: 'Anahtarı test et' })} aria-label="Test key">
                        <FlaskConical size={14} />
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => openReveal(k)}
                        title={L(lang, { en: 'Show saved credential', tr: 'Kayıtlı credential\'ı göster' })} aria-label="Show credential">
                        <KeyRound size={14} />
                      </Button>
                      {isAdmin && (
                        <Button size="sm" variant="ghost" onClick={() => openReassign(k)}
                          title={L(lang, { en: 'Bind / unbind proxy (admin)', tr: 'Proxy ata / kaldır (admin)' })} aria-label="Reassign proxy">
                          <ArrowRightLeft size={14} />
                        </Button>
                      )}
                      <Button size="sm" variant="ghost" className="text-red-400 hover:bg-red-500/10 hover:text-red-300"
                        onClick={() => setDeleteTarget(k)}
                        title={L(lang, { en: 'Delete key', tr: 'Anahtarı sil' })} aria-label="Delete key">
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
        title={L(lang, { en: 'Bind / unbind proxy', tr: 'Proxy ata / kaldır' })}
        footer={
          <>
            <Button variant="ghost" onClick={() => setReassignTarget(null)}>
              {L(lang, { en: 'Cancel', tr: 'Vazgeç' })}
            </Button>
            <Button variant="warning" onClick={submitReassign} loading={reassigning}>
              <ArrowRightLeft size={14} />
              {reassignProxyId === ''
                ? L(lang, { en: 'Unbind (direct egress)', tr: 'Bağ kaldır (direkt çıkış)' })
                : L(lang, { en: 'Bind permanently', tr: 'Kalıcı bağla' })}
            </Button>
          </>
        }
      >
        {reassignTarget && (
          <div className="space-y-4">
            <p className="text-sm text-zinc-300">
              {L(lang, { en: 'Proxy binding for', tr: 'Proxy bağlaması:' })}{' '}
              <span className="font-semibold text-zinc-100">{reassignTarget.label}</span>
            </p>
            <div className="flex items-start gap-2.5 rounded-lg border border-amber-500/30 bg-amber-500/[0.07] px-3 py-2.5 text-xs leading-relaxed text-amber-300">
              <AlertTriangle size={15} className="mt-0.5 shrink-0" />
              <span>
                {L(lang, {
                  en: 'Sticky binding: a bound proxy never rotates automatically - the key egresses through it forever until an admin changes it. Unbind to switch the key to direct egress.',
                  tr: 'Sticky binding: bağlanan proxy asla otomatik rotate edilmez - anahtar sonsuza kadar aynı IP\'den çıkar, admin değiştirene kadar. Direkt çıkış istiyorsanız bağ kaldırın.',
                })}
              </span>
            </div>
            <div>
              <Label htmlFor="reassign-proxy">{L(lang, { en: 'Proxy', tr: 'Proxy' })}</Label>
              <Select id="reassign-proxy" value={reassignProxyId} onChange={(e) => setReassignProxyId(e.target.value)} disabled={reassigning}>
                <option value="">
                  {L(lang, { en: '- Direct (unbind, no proxy)', tr: '- Direkt (bağ kaldır, proxy yok)' })}
                </option>
                {proxies.map((p) => (
                  <option key={p.id} value={p.id}>
                    #{p.id} - {p.label} {p.country ? `(${p.country})` : ''}
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
        title={L(lang, { en: 'Delete key', tr: 'Anahtarı sil' })}
        footer={
          <>
            <Button variant="ghost" onClick={() => setDeleteTarget(null)}>
              {L(lang, { en: 'Cancel', tr: 'Vazgeç' })}
            </Button>
            <Button variant="danger" onClick={confirmDelete} loading={deleting}>
              <Trash2 size={14} /> {L(lang, { en: 'Delete key', tr: 'Anahtarı sil' })}
            </Button>
          </>
        }
      >
        {deleteTarget && (
          <p className="text-sm text-zinc-300">
            {L(lang, {
              en: `Permanently delete "${deleteTarget.label}"? Any jobs already submitted with this key are not cancelled, but no new job will use it.`,
              tr: `"${deleteTarget.label}" kalıcı olarak silinsin mi? Bu anahtarla gönderilmiş işler iptal edilmez ama yeni işlerde kullanılmaz.`,
            })}
          </p>
        )}
      </Modal>

      {/* Reveal credential modal */}
      <Modal
        open={!!revealTarget}
        onClose={() => setRevealTarget(null)}
        title={revealTarget ? L(lang, { en: `Credential - ${revealTarget.label}`, tr: `Credential - ${revealTarget.label}` }) : ''}
        footer={<Button variant="ghost" onClick={() => setRevealTarget(null)}>{L(lang, { en: 'Close', tr: 'Kapat' })}</Button>}
      >
        {revealLoading ? (
          <LoadingBlock label={L(lang, { en: 'Decrypting…', tr: 'Şifre çözülüyor…' })} />
        ) : (
          <>
            <p className="mb-2 text-[11px] text-zinc-500">
              {L(lang, { en: 'Saved credential (decrypted). Anyone with this value can use your account - keep it private.', tr: 'Kayıtlı credential (şifresi çözüldü). Bu değeri gören herkes hesabınızı kullanabilir - gizli tutun.' })}
            </p>
            <pre className="max-h-80 overflow-auto rounded-lg border border-zinc-800 bg-zinc-950 p-3 text-[11px] leading-relaxed text-emerald-300">{revealValue}</pre>
          </>
        )}
      </Modal>
    </div>
  );
}

function QuotaCell({ used, quota }: { used?: number | null; quota?: number | null }) {
  const u = used ?? 0;
  const q = quota ?? 0;
  const lang = useLang();
  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2 text-[11px] tabular-nums text-zinc-400">
        <span>{u} / {q > 0 ? q : '∞'}</span>
        {q > 0 && <span className="text-zinc-600">{Math.round((u / q) * 100)}%</span>}
      </div>
      {q > 0 && (
        <div className="h-1.5 w-full overflow-hidden rounded-full bg-zinc-800">
          <div
            className={`h-full rounded-full ${u / q >= 0.9 ? 'bg-red-500' : u / q >= 0.7 ? 'bg-amber-500' : 'bg-emerald-500'}`}
            style={{ width: `${Math.min(100, (u / q) * 100)}%` }}
          />
        </div>
      )}
      <div className="hidden text-[10px] text-zinc-600">
        {L(lang, { en: 'usage today (resets daily)', tr: 'bugünkü kullanım (günlük sıfırlanır)' })}
      </div>
    </div>
  );
}
