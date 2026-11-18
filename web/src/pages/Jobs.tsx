import { useCallback, useEffect, useMemo, useState, type FormEvent } from 'react';
import { useNavigate } from 'react-router-dom';
import { Plus, RefreshCw, ChevronRight, ListChecks, Rocket } from 'lucide-react';
import {
  api,
  errorMessage,
  jobProviderName,
  jobCreatedAt,
  jobCostMicros,
  type Job,
  type Provider,
  type CreateJobBody,
} from '../api';
import {
  Button,
  Modal,
  Input,
  Label,
  Select,
  Textarea,
  Table,
  THead,
  TBody,
  Tr,
  Th,
  Td,
  StatusBadge,
  LoadingBlock,
  ErrorBlock,
  EmptyState,
  FieldError,
} from '../components/ui';
import { useToast } from '../components/Toast';
import { usd, fmtDateTime, shortId } from '../lib/format';

const GPU_TYPES = ['Any', 'T4', 'P100', 'V100', 'A100', 'H100', 'L4', 'L40S', 'RTX4090', 'RTX6000Ada', 'MI300X', 'Gaudi2'];
const JOB_TYPES = [
  { value: 'script', label: 'Script', payloadLabel: 'Script payload', placeholder: '#!/bin/bash\npython train.py --epochs 3' },
  { value: 'notebook', label: 'Notebook', payloadLabel: 'Notebook reference / definition', placeholder: 'Paste notebook JSON, a URL, or a repository path…' },
  { value: 'docker', label: 'Docker', payloadLabel: 'Docker image', placeholder: 'e.g. pytorch/pytorch:2.4.0-cuda12.1-cudnn9-runtime' },
  { value: 'command', label: 'Command', payloadLabel: 'Command', placeholder: 'e.g. python inference.py --input data.parquet' },
];
const STATUS_FILTERS = ['all', 'queued', 'running', 'succeeded', 'failed', 'cancelled'];

export default function Jobs() {
  const navigate = useNavigate();
  const toast = useToast();

  const [jobs, setJobs] = useState<Job[]>([]);
  const [providers, setProviders] = useState<Provider[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);
  const [statusFilter, setStatusFilter] = useState('all');

  // New job modal
  const [modalOpen, setModalOpen] = useState(false);
  const [type, setType] = useState('script');
  const [payload, setPayload] = useState('');
  const [gpuType, setGpuType] = useState('Any');
  const [minVram, setMinVram] = useState('');
  const [gpuCount, setGpuCount] = useState('1');
  const [maxDuration, setMaxDuration] = useState('');
  const [providerPref, setProviderPref] = useState<string[]>([]);
  const [maxCostUsd, setMaxCostUsd] = useState('');
  const [submitting, setSubmitting] = useState(false);
  const [formError, setFormError] = useState('');

  const load = useCallback(
    (spin = true) => {
      if (spin) setLoading(true);
      else setRefreshing(true);
      setError('');
      api
        .jobs(statusFilter)
        .then(setJobs)
        .catch((e) => setError(errorMessage(e, 'Failed to load jobs')))
        .finally(() => {
          setLoading(false);
          setRefreshing(false);
        });
    },
    [statusFilter],
  );

  useEffect(() => {
    load();
  }, [load]);

  useEffect(() => {
    // Providers power the provider_preference checkboxes in the New Job form.
    api.providers().then(setProviders).catch(() => setProviders([]));
  }, []);

  const activeType = useMemo(() => JOB_TYPES.find((t) => t.value === type) ?? JOB_TYPES[0], [type]);

  function openModal() {
    setType('script');
    setPayload('');
    setGpuType('Any');
    setMinVram('');
    setGpuCount('1');
    setMaxDuration('');
    setProviderPref([]);
    setMaxCostUsd('');
    setFormError('');
    setModalOpen(true);
  }

  async function submitJob(e: FormEvent) {
    e.preventDefault();
    if (!payload.trim()) return setFormError('Payload is required.');
    const vram = Number(minVram);
    if (!Number.isFinite(vram) || vram <= 0) return setFormError('Min VRAM (GB) is required and must be > 0.');
    const count = Number(gpuCount);
    if (!Number.isInteger(count) || count < 1 || count > 8) return setFormError('GPU count must be an integer between 1 and 8.');
    const dur = maxDuration.trim() === '' ? null : Number(maxDuration);
    if (dur !== null && (!Number.isFinite(dur) || dur <= 0)) return setFormError('Max duration must be a positive number of seconds.');
    const maxCost = maxCostUsd.trim() === '' ? null : Number(maxCostUsd);
    if (maxCost !== null && (!Number.isFinite(maxCost) || maxCost < 0)) return setFormError('Max cost must be a non-negative number.');

    const body: CreateJobBody = {
      type,
      payload: payload.trim(),
      gpu_type: gpuType,
      min_vram_gb: vram,
      gpu_count: count,
      max_duration_seconds: dur,
      max_cost_usd: maxCost,
    };
    if (providerPref.length > 0) body.provider_preference = providerPref;

    setSubmitting(true);
    setFormError('');
    try {
      const created = await api.createJob(body);
      toast(`Job ${shortId(created?.id) || 'created'} submitted`, 'success');
      setModalOpen(false);
      load(false);
    } catch (e2) {
      setFormError(errorMessage(e2, 'Failed to submit job'));
    } finally {
      setSubmitting(false);
    }
  }

  function toggleProviderPref(id: string) {
    setProviderPref((prev) => (prev.includes(id) ? prev.filter((x) => x !== id) : [...prev, id]));
  }

  return (
    <div className="space-y-5">
      {/* Header */}
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="text-xl font-bold tracking-tight text-zinc-50">Jobs</h1>
          <p className="text-xs text-zinc-500">Submit GPU workloads and track them to completion.</p>
        </div>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => load(false)} loading={refreshing}>
            <RefreshCw size={14} />
            Refresh
          </Button>
          <Button size="sm" variant="primary" onClick={openModal}>
            <Plus size={14} /> New Job
          </Button>
        </div>
      </div>

      {/* Status filter pills */}
      <div className="flex flex-wrap items-center gap-1.5">
        {STATUS_FILTERS.map((s) => (
          <button
            key={s}
            onClick={() => setStatusFilter(s)}
            className={`rounded-full border px-3 py-1.5 text-[11px] font-semibold uppercase tracking-wider transition-colors ${
              statusFilter === s
                ? 'border-emerald-500/40 bg-emerald-500/15 text-emerald-300'
                : 'border-zinc-800 bg-zinc-900 text-zinc-500 hover:border-zinc-700 hover:text-zinc-300'
            }`}
          >
            {s}
          </button>
        ))}
      </div>

      {loading ? (
        <LoadingBlock label="Loading jobs…" />
      ) : error ? (
        <ErrorBlock message={error} onRetry={() => load()} />
      ) : jobs.length === 0 ? (
        <EmptyState
          icon={ListChecks}
          title={statusFilter === 'all' ? 'No jobs yet' : `No ${statusFilter} jobs`}
          subtitle="Define a payload, pick a GPU profile and submit. GPUHub schedules it onto free-tier providers."
          action={
            <Button size="sm" variant="primary" onClick={openModal}>
              <Plus size={14} /> New Job
            </Button>
          }
        />
      ) : (
        <Table>
          <THead>
            <Tr>
              <Th>ID</Th>
              <Th>Type</Th>
              <Th>GPU</Th>
              <Th>Status</Th>
              <Th>Provider</Th>
              <Th>Created</Th>
              <Th>Cost</Th>
              <Th className="w-10" />
            </Tr>
          </THead>
          <TBody>
            {jobs.map((j) => (
              <Tr
                key={j.id}
                className="cursor-pointer"
                onClick={() => navigate(`/jobs/${j.id}`)}
                tabIndex={0}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') navigate(`/jobs/${j.id}`);
                }}
              >
                <Td>
                  <code className="text-xs font-semibold text-emerald-400">{shortId(j.id)}</code>
                </Td>
                <Td className="capitalize">{j.type}</Td>
                <Td>
                  <div className="text-xs text-zinc-200">{j.gpu_type ?? 'Any'}</div>
                  <div className="text-[10px] text-zinc-500">
                    ≥{j.min_vram_gb ?? '?'} GB VRAM · ×{j.gpu_count ?? 1}
                  </div>
                </Td>
                <Td>
                  <StatusBadge status={j.status} />
                </Td>
                <Td className="text-xs text-zinc-300">{jobProviderName(j)}</Td>
                <Td className="text-xs text-zinc-400">{fmtDateTime(jobCreatedAt(j))}</Td>
                <Td className="font-mono text-xs text-zinc-300">{usd(jobCostMicros(j))}</Td>
                <Td>
                  <ChevronRight size={15} className="text-zinc-600" />
                </Td>
              </Tr>
            ))}
          </TBody>
        </Table>
      )}

      {/* New Job modal */}
      <Modal
        open={modalOpen}
        onClose={() => setModalOpen(false)}
        title={
          <span className="flex items-center gap-2">
            <Rocket size={15} className="text-emerald-400" /> New Job
          </span>
        }
        wide
        footer={
          <>
            <Button variant="ghost" onClick={() => setModalOpen(false)}>
              Cancel
            </Button>
            <Button variant="primary" type="submit" form="new-job-form" loading={submitting}>
              Submit job
            </Button>
          </>
        }
      >
        <form id="new-job-form" onSubmit={submitJob} className="space-y-4">
          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <Label htmlFor="job-type">Type *</Label>
              <Select id="job-type" value={type} onChange={(e) => setType(e.target.value)} disabled={submitting}>
                {JOB_TYPES.map((t) => (
                  <option key={t.value} value={t.value}>
                    {t.label}
                  </option>
                ))}
              </Select>
            </div>
            <div className="grid grid-cols-3 gap-3">
              <div className="col-span-1 sm:col-span-1">
                <Label htmlFor="job-gpu-type">GPU type *</Label>
                <Select id="job-gpu-type" value={gpuType} onChange={(e) => setGpuType(e.target.value)} disabled={submitting}>
                  {GPU_TYPES.map((g) => (
                    <option key={g} value={g}>
                      {g}
                    </option>
                  ))}
                </Select>
              </div>
              <div>
                <Label htmlFor="job-vram">Min VRAM GB *</Label>
                <Input
                  id="job-vram"
                  type="number"
                  min={1}
                  placeholder="16"
                  value={minVram}
                  onChange={(e) => setMinVram(e.target.value)}
                  disabled={submitting}
                  required
                />
              </div>
              <div>
                <Label htmlFor="job-count">GPUs *</Label>
                <Input
                  id="job-count"
                  type="number"
                  min={1}
                  max={8}
                  step={1}
                  value={gpuCount}
                  onChange={(e) => setGpuCount(e.target.value)}
                  disabled={submitting}
                  required
                />
              </div>
            </div>
          </div>

          <div>
            <Label htmlFor="job-payload">{activeType.payloadLabel} *</Label>
            <Textarea
              id="job-payload"
              placeholder={activeType.placeholder}
              value={payload}
              onChange={(e) => setPayload(e.target.value)}
              disabled={submitting}
              className="min-h-[120px]"
            />
          </div>

          <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <Label htmlFor="job-max-duration">Max duration (seconds)</Label>
              <Input
                id="job-max-duration"
                type="number"
                min={1}
                placeholder="e.g. 3600"
                value={maxDuration}
                onChange={(e) => setMaxDuration(e.target.value)}
                disabled={submitting}
              />
            </div>
            <div>
              <Label htmlFor="job-max-cost">Max cost (USD)</Label>
              <Input
                id="job-max-cost"
                type="number"
                min={0}
                step="0.0001"
                placeholder="e.g. 0.50"
                value={maxCostUsd}
                onChange={(e) => setMaxCostUsd(e.target.value)}
                disabled={submitting}
              />
            </div>
          </div>

          {providers.length > 0 && (
            <div>
              <Label>Provider preference (optional)</Label>
              <div className="flex flex-wrap gap-2">
                {providers.map((p) => {
                  const checked = providerPref.includes(p.id);
                  return (
                    <button
                      key={p.id}
                      type="button"
                      onClick={() => toggleProviderPref(p.id)}
                      aria-pressed={checked}
                      className={`flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-xs font-medium transition-colors ${
                        checked
                          ? 'border-emerald-500/40 bg-emerald-500/15 text-emerald-300'
                          : 'border-zinc-800 bg-zinc-900 text-zinc-400 hover:border-zinc-700 hover:text-zinc-200'
                      }`}
                    >
                      <span
                        className={`flex h-3.5 w-3.5 items-center justify-center rounded border ${
                          checked ? 'border-emerald-400 bg-emerald-500 text-emerald-950' : 'border-zinc-700'
                        }`}
                      >
                        {checked && (
                          <svg viewBox="0 0 10 8" className="h-2.5 w-2.5 fill-none stroke-current stroke-2">
                            <path d="M1 4l2.5 2.5L9 1" strokeLinecap="round" strokeLinejoin="round" />
                          </svg>
                        )}
                      </span>
                      {p.name}
                    </button>
                  );
                })}
              </div>
              <p className="mt-1.5 text-[11px] text-zinc-500">Restrict scheduling to these providers. Empty = any provider.</p>
            </div>
          )}

          <FieldError>{formError}</FieldError>
        </form>
      </Modal>
    </div>
  );
}
