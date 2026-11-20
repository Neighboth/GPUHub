import { useCallback, useEffect, useRef, useState } from 'react';
import { Sparkles, Send, Loader2, Cpu, Info, Server, MessageSquare, RotateCcw } from 'lucide-react';
import { api, errorMessage, providerKeyCount, type Provider, type Job } from '../api';
import ProviderIcon from '../components/ProviderIcon';
import { Button, Card, CardContent, CardHeader, CardTitle, Badge, Label, Select, Input, LoadingBlock } from '../components/ui';
import { useLang, L } from '../i18n';

// 1B-class GGUF models in Q4 (quantized) - small enough for every free-tier GPU.
const MODELS = [
  { id: 'qwen2.5-1.5b', name: 'Qwen2.5 1.5B Instruct (Q4_K_M)', repo: 'Qwen/Qwen2.5-1.5B-Instruct-GGUF', file: 'qwen2.5-1.5b-instruct-q4_k_m.gguf', vram: 16 },
  { id: 'qwen3-0.6b', name: 'Qwen3 0.6B (Q4_K_M)', repo: 'Qwen/Qwen3-0.6B-GGUF', file: 'Qwen3-0.6B-Q4_K_M.gguf', vram: 8 },
  { id: 'tinyllama-1.1b', name: 'TinyLlama 1.1B Chat (Q4_K_M)', repo: 'TheBloke/TinyLlama-1.1B-Chat-v1.0-GGUF', file: 'tinyllama-1.1b-chat-v1.0.Q4_K_M.gguf', vram: 8 },
] as const;

type ModelId = (typeof MODELS)[number]['id'];

function buildScript(modelId: ModelId, userMessage: string): string {
  const m = MODELS.find((x) => x.id === modelId)!;
  // message is JSON-encoded so quoting is safe inside the Python heredoc.
  const msg = JSON.stringify(userMessage);
  return `#!/bin/bash
set -e
pip install --quiet --disable-pip-version-check llama-cpp-python huggingface_hub
python - <<'PY'
import json, time
from huggingface_hub import hf_hub_download
from llama_cpp import Llama

t0 = time.time()
print("[gpuhub] model indiriliyor: ${m.repo} / ${m.file}", flush=True)
path = hf_hub_download("${m.repo}", filename="${m.file}")
print(f"[gpuhub] model hazir ({round(time.time()-t0,1)}s)", flush=True)

llm = Llama(model_path=path, n_ctx=2048, verbose=False)
print("[gpuhub] model yuklendi, q4", flush=True)

messages = [
  {"role": "system", "content": "You are a concise, friendly assistant. Reply in the same language as the user."},
  {"role": "user", "content": ${msg}},
]
out = llm.create_chat_completion(messages=messages, max_tokens=256, temperature=0.7)
answer = out["choices"][0]["message"]["content"]
u = out.get("usage", {})
print("=== GPUHUB_MODEL_ANSWER_START ===", flush=True)
print(answer, flush=True)
print("=== GPUHUB_MODEL_ANSWER_END ===", flush=True)
print(f"[gpuhub] prompt_tokens={u.get('prompt_tokens')} completion_tokens={u.get('completion_tokens')}", flush=True)
print(f"[gpuhub] toplam_sure={round(time.time()-t0,1)}s", flush=True)
PY
`;
}

function extractAnswer(logs: string | null | undefined): { answer: string; meta: string } {
  if (!logs) return { answer: '(log yok)', meta: '' };
  const s = String(logs);
  const a = s.indexOf('=== GPUHUB_MODEL_ANSWER_START ===');
  const b = s.indexOf('=== GPUHUB_MODEL_ANSWER_END ===');
  let answer = s;
  let meta = '';
  if (a >= 0 && b > a) {
    answer = s.slice(a + '=== GPUHUB_MODEL_ANSWER_START ==='.length, b).trim();
    meta = (s.slice(0, a) + '\n' + s.slice(b + '=== GPUHUB_MODEL_ANSWER_END ==='.length)).trim();
  }
  return { answer, meta };
}

type ChatTurn = { role: 'user' | 'assistant'; text: string; job?: Job; meta?: string };

export default function AITest() {
  const lang = useLang();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [model, setModel] = useState<ModelId>('qwen2.5-1.5b');
  const [gpu, setGpu] = useState('T4');
  const [provider, setProvider] = useState('kaggle');
  const [message, setMessage] = useState('');
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const stopRef = useRef(false);

  useEffect(() => {
    api.providers().then((ps) => {
      setProviders(ps);
      const usable = ps.find((p) => p.id === 'kaggle' && providerKeyCount(p) > 0);
      if (usable) setProvider('kaggle');
    }).catch(() => {});
  }, []);

  const m = MODELS.find((x) => x.id === model)!;

  async function send() {
    const text = message.trim();
    if (!text || busy) return;
    setMessage('');
    setTurns((prev) => [...prev, { role: 'user', text }]);
    setBusy(true);
    setStatus(L(lang, { en: 'Submitting job to provider…', tr: 'Sağlayıcıya job gönderiliyor…' }));
    stopRef.current = false;
    try {
      const job = await api.createJob({
        type: 'script',
        payload: buildScript(model, text),
        gpu_type: gpu,
        min_vram_gb: m.vram,
        gpu_count: 1,
        max_duration_seconds: 1800,
        provider_preference: [provider],
      });
      setStatus(`${L(lang, { en: 'Job', tr: 'Job' })} #${job.id} - ${L(lang, { en: 'waiting for provider', tr: 'sağlayıcı bekleniyor' })}…`);

      const deadline = Date.now() + 20 * 60 * 1000;
      let last: Job = job;
      while (Date.now() < deadline && !stopRef.current) {
        await new Promise((r) => setTimeout(r, 5000));
        last = await api.job(String(job.id));
        const st = String(last.status ?? '').toLowerCase();
        setStatus(`Job #${job.id}: ${st} (provider=${last.provider ?? '-'}, attempts=${Array.isArray(last.attempts) ? last.attempts.length : 0})`);
        if (['succeeded', 'failed', 'cancelled', 'timeout', 'unschedulable'].includes(st)) break;
      }
      const { answer, meta } = extractAnswer(last.logs);
      setTurns((prev) => [...prev, { role: 'assistant', text: answer, job: last, meta }]);
      setStatus('');
    } catch (e) {
      setTurns((prev) => [...prev, { role: 'assistant', text: `Hata: ${errorMessage(e)}` }]);
      setStatus('');
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="space-y-6">
      <div className="flex flex-wrap items-center justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 text-xl font-bold tracking-tight text-zinc-50">
            <Sparkles size={20} className="text-emerald-400" />
            {L(lang, { en: 'AI Test', tr: 'AI Testi' })}
          </h1>
          <p className="mt-1 text-xs text-zinc-500">
            {L(lang, { en: 'Chat with a 1B Q4 model running on a free GPU provider server.', tr: 'Ücretsiz GPU sağlayıcısı sunucusunda çalışan 1B Q4 model ile sohbet edin.' })}
          </p>
        </div>
        <Button size="sm" variant="outline" onClick={() => setTurns([])}>
          <RotateCcw size={13} /> {L(lang, { en: 'Clear chat', tr: 'Sohbeti temizle' })}
        </Button>
      </div>

      <div className="grid gap-4 lg:grid-cols-[320px_1fr]">
        {/* Controls */}
        <Card>
          <CardHeader><CardTitle>{L(lang, { en: 'Configuration', tr: 'Yapılandırma' })}</CardTitle></CardHeader>
          <CardContent className="space-y-4">
            <div>
              <Label htmlFor="ai-provider">{L(lang, { en: 'Provider', tr: 'Sağlayıcı' })}</Label>
              <Select id="ai-provider" value={provider} onChange={(e) => setProvider(e.target.value)}>
                {providers.filter((p) => providerKeyCount(p) > 0).map((p) => (
                  <option key={p.id} value={p.id}>{p.name} ({providerKeyCount(p)} key)</option>
                ))}
              </Select>
            </div>
            <div>
              <Label htmlFor="ai-gpu">{L(lang, { en: 'GPU', tr: 'GPU' })}</Label>
              <Select id="ai-gpu" value={gpu} onChange={(e) => setGpu(e.target.value)}>
                <option value="T4">T4 (16 GB)</option>
                <option value="A10">A10 (24 GB)</option>
                <option value="L4">L4 (24 GB)</option>
                <option value="L40S">L40S (48 GB)</option>
                <option value="A100">A100 (40 GB)</option>
                <option value="H100">H100 (80 GB)</option>
              </Select>
            </div>
            <div>
              <Label htmlFor="ai-model">{L(lang, { en: 'Model (1B, Q4)', tr: 'Model (1B, Q4)' })}</Label>
              <Select id="ai-model" value={model} onChange={(e) => setModel(e.target.value as ModelId)}>
                {MODELS.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
              </Select>
            </div>
            <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-3 text-[11px] leading-relaxed text-zinc-400">
              <div className="flex items-center gap-1.5 font-medium text-zinc-300"><Info size={12} /> {L(lang, { en: 'How it works', tr: 'Nasıl çalışır' })}</div>
              <ul className="mt-2 list-disc space-y-1 pl-4">
                <li>{L(lang, { en: 'GPUHub submits a script job to the selected provider.', tr: 'GPUHub seçilen sağlayıcıya script job gönderir.' })}</li>
                <li>{L(lang, { en: 'The provider downloads the Q4 GGUF and runs inference on its GPU.', tr: 'Sağlayıcı Q4 GGUF dosyasını indirip GPU\'sunda çalıştırır.' })}</li>
                <li>{L(lang, { en: 'Logs are parsed and the model answer is shown here.', tr: 'Loglar ayrıştırılıp model cevabı burada gösterilir.' })}</li>
              </ul>
              <div className="mt-2 text-zinc-500">Repos: {MODELS.map((x) => x.repo).join(', ')}</div>
            </div>
          </CardContent>
        </Card>

        {/* Chat */}
        <Card className="flex min-h-[520px] flex-col">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle className="flex items-center gap-2"><MessageSquare size={15} /> {L(lang, { en: 'Chat', tr: 'Sohbet' })}</CardTitle>
            <div className="flex items-center gap-2 text-[11px] text-zinc-500">
              <ProviderIcon id={provider} size={14} /> {provider} <Badge variant="neutral">{gpu}</Badge> <Badge variant="neutral">{m.name.split(' ')[0]}</Badge>
            </div>
          </CardHeader>
          <CardContent className="flex flex-1 flex-col">
            <div className="min-h-[320px] flex-1 space-y-3 overflow-y-auto pr-1">
              {turns.length === 0 && (
                <div className="flex h-full items-center justify-center text-sm text-zinc-600">
                  {L(lang, { en: 'Send a message to start.', tr: 'Başlamak için bir mesaj gönderin.' })}
                </div>
              )}
              {turns.map((t, i) => (
                <div key={i} className={`flex ${t.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  <div className={`max-w-[85%] rounded-xl px-3 py-2 text-sm ${
                    t.role === 'user' ? 'bg-emerald-500/15 text-emerald-100' : 'bg-zinc-800/80 text-zinc-200'
                  }`}>
                    <div className="whitespace-pre-wrap break-words">{t.text}</div>
                    {t.job && (
                      <div className="mt-2 border-t border-zinc-700/60 pt-2 text-[11px] text-zinc-400">
                        <div className="flex flex-wrap items-center gap-2">
                          <Badge variant={t.job.status === 'succeeded' ? 'success' : 'danger'}>{t.job.status}</Badge>
                          <span>Job #{t.job.id}</span>
                          {t.job.provider && <span>provider={String(t.job.provider)}</span>}
                          {t.job.min_vram_gb != null && <span>VRAM≥{t.job.min_vram_gb}GB</span>}
                        </div>
                        {t.meta && (
                          <details className="mt-2">
                            <summary className="cursor-pointer text-zinc-500">{L(lang, { en: 'Detailed info', tr: 'Detaylı bilgi' })}</summary>
                            <pre className="mt-1 max-h-56 overflow-auto whitespace-pre-wrap rounded bg-zinc-950 p-2 text-[10px] leading-relaxed text-zinc-400">{t.meta}</pre>
                          </details>
                        )}
                      </div>
                    )}
                  </div>
                </div>
              ))}
            </div>
            {status && <div className="mt-2 flex items-center gap-2 text-xs text-emerald-400"><Loader2 size={13} className="animate-spin" />{status}</div>}
            <form
              className="mt-3 flex items-center gap-2"
              onSubmit={(e) => { e.preventDefault(); void send(); }}
            >
              <Input value={message} onChange={(e) => setMessage(e.target.value)} placeholder={L(lang, { en: 'Ask the model something…', tr: 'Modele bir şey sorun…' })} disabled={busy} />
              <Button variant="primary" type="submit" disabled={busy || !message.trim()}>
                {busy ? <Loader2 size={15} className="animate-spin" /> : <Send size={15} />}
                {L(lang, { en: 'Send', tr: 'Gönder' })}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
