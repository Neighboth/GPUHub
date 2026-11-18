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
} from 'lucide-react';
import { api, errorMessage, resultSummary, type Settings as SettingsShape } from '../api';
import {
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  CardDescription,
  Input,
  Label,
  Select,
  FieldError,
  LoadingBlock,
  ErrorBlock,
} from '../components/ui';
import { useToast } from '../components/Toast';

const ROTATION_OPTIONS = ['round_robin', 'weighted', 'random', 'least_used', 'priority', 'cooldown_aware'];
const SCHEDULER_OPTIONS = ['priority', 'fair_share', 'fifo', 'best_fit', 'cost_optimized'];

function withCurrent(options: string[], current?: string): string[] {
  if (current && !options.includes(current)) return [current, ...options];
  return options;
}

export default function Settings() {
  const toast = useToast();

  const [raw, setRaw] = useState<SettingsShape | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // Form state
  const [rotation, setRotation] = useState('');
  const [scheduler, setScheduler] = useState('');
  const [watermark, setWatermark] = useState('');
  const [maxRetries, setMaxRetries] = useState('');
  const [maxCostUsd, setMaxCostUsd] = useState('');
  const [webhookUrl, setWebhookUrl] = useState('');

  const [savingRotation, setSavingRotation] = useState(false);
  const [savingQuota, setSavingQuota] = useState(false);
  const [savingWebhook, setSavingWebhook] = useState(false);

  // Master key rotation
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
        setRotation(s.rotation_strategy ?? '');
        setScheduler(s.scheduler_strategy ?? '');
        setWatermark(s.quota_high_watermark_percent != null ? String(s.quota_high_watermark_percent) : '');
        setMaxRetries(s.max_retries != null ? String(s.max_retries) : '');
        const costUsd =
          typeof s.default_max_cost_usd === 'number'
            ? s.default_max_cost_usd
            : typeof s.max_cost_usd === 'number'
              ? s.max_cost_usd
              : typeof s.default_max_cost_usd_micros === 'number'
                ? s.default_max_cost_usd_micros / 1e6
                : null;
        setMaxCostUsd(costUsd != null ? String(costUsd) : '');
        setWebhookUrl(s.notifications_webhook_url ?? s.webhook_url ?? '');
      })
      .catch((e) => setError(errorMessage(e, 'Failed to load settings')))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function saveSection(body: Partial<SettingsShape>, which: 'rotation' | 'quota' | 'webhook') {
    const setBusy = which === 'rotation' ? setSavingRotation : which === 'quota' ? setSavingQuota : setSavingWebhook;
    setBusy(true);
    try {
      await api.updateSettings({ ...(raw ?? {}), ...body });
      toast('Settings saved', 'success');
      load();
    } catch (e) {
      toast(errorMessage(e, 'Failed to save settings'), 'error');
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
    const wm = watermark.trim() === '' ? undefined : Number(watermark);
    if (wm !== undefined && (!Number.isFinite(wm) || wm < 1 || wm > 100)) {
      toast('Quota high watermark must be between 1 and 100', 'error');
      return;
    }
    const retries = maxRetries.trim() === '' ? undefined : Number(maxRetries);
    if (retries !== undefined && (!Number.isInteger(retries) || retries < 0)) {
      toast('Max retries must be a non-negative integer', 'error');
      return;
    }
    const cost = maxCostUsd.trim() === '' ? null : Number(maxCostUsd);
    if (cost !== null && (!Number.isFinite(cost) || cost < 0)) {
      toast('Default max cost must be a non-negative number', 'error');
      return;
    }
    const body: Partial<SettingsShape> = {
      quota_high_watermark_percent: wm,
      max_retries: retries,
    };
    // Preserve whichever cost representation the server uses.
    if (raw && typeof raw.default_max_cost_usd_micros === 'number') {
      body.default_max_cost_usd_micros = cost === null ? undefined : Math.round(cost * 1e6);
    } else if (raw && ('max_cost_usd' in raw || raw.max_cost_usd !== undefined)) {
      body.max_cost_usd = cost;
    } else {
      body.default_max_cost_usd = cost;
    }
    await saveSection(body, 'quota');
  }

  async function saveWebhook(e: FormEvent) {
    e.preventDefault();
    await saveSection({ notifications_webhook_url: webhookUrl.trim() }, 'webhook');
  }

  async function submitRotate(e: FormEvent) {
    e.preventDefault();
    setRotateError('');
    setRotateResult(null);
    if (!currentMasterKey.trim() || !newMasterKey.trim()) {
      setRotateError('Both the current and new master key are required.');
      return;
    }
    if (newMasterKey.trim().length < 16) {
      setRotateError('The new master key should be at least 16 characters.');
      return;
    }
    setRotating(true);
    try {
      const res = await api.rotateMasterKey(currentMasterKey.trim(), newMasterKey.trim());
      setRotateResult({ ok: true, message: resultSummary(res) });
      setCurrentMasterKey('');
      setNewMasterKey('');
      toast('Master key rotated', 'success');
    } catch (e2) {
      setRotateResult({ ok: false, message: errorMessage(e2, 'Master key rotation failed') });
    } finally {
      setRotating(false);
    }
  }

  if (loading) return <LoadingBlock label="Loading settings…" />;
  if (error) return <ErrorBlock message={error} onRetry={load} />;

  return (
    <div className="space-y-5">
      {/* Header */}
      <div>
        <h1 className="text-xl font-bold tracking-tight text-zinc-50">Settings</h1>
        <p className="text-xs text-zinc-500">Instance-wide behavior: rotation, scheduling, quotas and notifications.</p>
      </div>

      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        {/* Rotation & scheduler */}
        <Card>
          <CardHeader>
            <div>
              <CardTitle className="flex items-center gap-2">
                <RotateCw size={14} className="text-emerald-400" /> Rotation & scheduling
              </CardTitle>
              <CardDescription>How keys are picked and how jobs are placed.</CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <form onSubmit={saveRotation} className="space-y-4">
              <div>
                <Label htmlFor="rotation-strategy">Key rotation strategy</Label>
                <Select id="rotation-strategy" value={rotation} onChange={(e) => setRotation(e.target.value)}>
                  <option value="">— select —</option>
                  {withCurrent(ROTATION_OPTIONS, rotation).map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </Select>
              </div>
              <div>
                <Label htmlFor="scheduler-strategy">Scheduler strategy</Label>
                <Select id="scheduler-strategy" value={scheduler} onChange={(e) => setScheduler(e.target.value)}>
                  <option value="">— select —</option>
                  {withCurrent(SCHEDULER_OPTIONS, scheduler).map((o) => (
                    <option key={o} value={o}>
                      {o}
                    </option>
                  ))}
                </Select>
              </div>
              <div className="flex justify-end">
                <Button type="submit" variant="primary" loading={savingRotation}>
                  <Save size={14} /> Save strategies
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
                <Gauge size={14} className="text-cyan-400" /> Quota & limits
              </CardTitle>
              <CardDescription>High watermark, retries and default cost guardrails.</CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <form onSubmit={saveQuota} className="space-y-4">
              <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                <div>
                  <Label htmlFor="watermark">Quota high watermark (%)</Label>
                  <Input
                    id="watermark"
                    type="number"
                    min={1}
                    max={100}
                    placeholder="80"
                    value={watermark}
                    onChange={(e) => setWatermark(e.target.value)}
                  />
                  <p className="mt-1.5 text-[11px] text-zinc-500">Keys above this usage % stop receiving work.</p>
                </div>
                <div>
                  <Label htmlFor="max-retries">Max retries</Label>
                  <Input
                    id="max-retries"
                    type="number"
                    min={0}
                    step={1}
                    placeholder="3"
                    value={maxRetries}
                    onChange={(e) => setMaxRetries(e.target.value)}
                  />
                  <p className="mt-1.5 text-[11px] text-zinc-500">Failover attempts before a job is marked failed.</p>
                </div>
              </div>
              <div>
                <Label htmlFor="max-cost">Default max cost per job (USD)</Label>
                <Input
                  id="max-cost"
                  type="number"
                  min={0}
                  step="0.0001"
                  placeholder="e.g. 1.00"
                  value={maxCostUsd}
                  onChange={(e) => setMaxCostUsd(e.target.value)}
                />
                <p className="mt-1.5 text-[11px] text-zinc-500">Applied when a job does not set its own limit.</p>
              </div>
              <div className="flex justify-end">
                <Button type="submit" variant="primary" loading={savingQuota}>
                  <Save size={14} /> Save limits
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>

        {/* Notifications webhook */}
        <Card>
          <CardHeader>
            <div>
              <CardTitle className="flex items-center gap-2">
                <Bell size={14} className="text-amber-400" /> Notifications
              </CardTitle>
              <CardDescription>Webhook invoked on job failure, quota exhaustion and key cooldown.</CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <form onSubmit={saveWebhook} className="space-y-4">
              <div>
                <Label htmlFor="webhook-url">Notifications webhook URL</Label>
                <Input
                  id="webhook-url"
                  type="url"
                  placeholder="https://hooks.slack.com/services/…"
                  value={webhookUrl}
                  onChange={(e) => setWebhookUrl(e.target.value)}
                  className="font-mono text-xs"
                />
                <p className="mt-1.5 text-[11px] text-zinc-500">POST with a JSON event payload. Leave empty to disable.</p>
              </div>
              <div className="flex justify-end">
                <Button type="submit" variant="primary" loading={savingWebhook}>
                  <Save size={14} /> Save webhook
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
                <KeySquare size={14} /> Master key rotation
              </CardTitle>
              <CardDescription>Re-encrypts all stored provider credentials with the new master key.</CardDescription>
            </div>
          </CardHeader>
          <CardContent>
            <div className="mb-4 flex items-start gap-2.5 rounded-lg border border-amber-500/25 bg-amber-500/5 px-3 py-2.5 text-[11px] leading-relaxed text-amber-300">
              <AlertTriangle size={14} className="mt-0.5 shrink-0" />
              Destructive operation. Back up your master key first — credentials encrypted with the old key cannot be
              recovered if the new key is lost before rotation completes.
            </div>
            <form onSubmit={submitRotate} className="space-y-4">
              <div>
                <Label htmlFor="current-master-key">Current master key</Label>
                <Input
                  id="current-master-key"
                  type="password"
                  autoComplete="off"
                  placeholder="Current master key"
                  value={currentMasterKey}
                  onChange={(e) => setCurrentMasterKey(e.target.value)}
                  disabled={rotating}
                />
              </div>
              <div>
                <Label htmlFor="new-master-key">New master key</Label>
                <Input
                  id="new-master-key"
                  type="password"
                  autoComplete="off"
                  placeholder="At least 16 characters"
                  value={newMasterKey}
                  onChange={(e) => setNewMasterKey(e.target.value)}
                  disabled={rotating}
                />
              </div>
              <FieldError>{rotateError}</FieldError>
              {rotateResult && (
                <div
                  className={`flex items-start gap-2 rounded-lg border px-3 py-2 text-xs ${
                    rotateResult.ok
                      ? 'border-emerald-500/25 bg-emerald-500/5 text-emerald-300'
                      : 'border-red-500/25 bg-red-500/5 text-red-300'
                  }`}
                >
                  {rotateResult.ok ? (
                    <CheckCircle2 size={14} className="mt-0.5 shrink-0" />
                  ) : (
                    <XCircle size={14} className="mt-0.5 shrink-0" />
                  )}
                  <span>{rotateResult.message}</span>
                </div>
              )}
              <div className="flex justify-end">
                <Button type="submit" variant="danger" loading={rotating}>
                  {rotating ? <Loader2 size={14} className="animate-spin" /> : <KeySquare size={14} />}
                  Rotate master key
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
