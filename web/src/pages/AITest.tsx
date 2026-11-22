import { useEffect, useRef, useState } from 'react';
import { Sparkles, Send, Loader2, Info, MessageSquare, RotateCcw, Image as ImageIcon, Video, Download } from 'lucide-react';
import { api, errorMessage, providerKeyCount, type Provider, type Job } from '../api';
import ProviderIcon from '../components/ProviderIcon';
import { Button, Card, CardContent, CardHeader, CardTitle, Badge, Label, Select, Input, Tabs } from '../components/ui';
import { useLang, L } from '../i18n';

type TaskMode = 'text' | 'image' | 'video';

const TEXT_MODELS = [
  { id: 'qwen2.5-1.5b', name: 'Qwen2.5 1.5B Instruct (Hızlı · Transformers)', repo: 'Qwen/Qwen2.5-1.5B-Instruct', vram: 16 },
  { id: 'qwen3-0.6b', name: 'Qwen3 0.6B (Ultra Hızlı · Küçük)', repo: 'Qwen/Qwen3-0.6B', vram: 8 },
  { id: 'tinyllama-1.1b', name: 'TinyLlama 1.1B Chat (Hafif · CPU Dostu)', repo: 'TinyLlama/TinyLlama-1.1B-Chat-v1.0', vram: 8 },
] as const;

const IMAGE_MODELS = [
  { id: 'sd-turbo', name: 'SD Turbo (1-2 Adım · Ultra Hızlı)', repo: 'stabilityai/sd-turbo', steps: 2, guidance: 0.0, vram: 16 },
  { id: 'sdxl-turbo', name: 'SDXL Turbo (2 Adım · Yüksek Kalite)', repo: 'stabilityai/sdxl-turbo', steps: 2, guidance: 0.0, vram: 16 },
  { id: 'lcm-dreamshaper', name: 'LCM Dreamshaper v7 (4 Adım · Artistik)', repo: 'SimianLuo/LCM_Dreamshaper_v7', steps: 4, guidance: 1.5, vram: 16 },
  { id: 'tiny-sd', name: 'Tiny-SD (Hafif · CPU Dostu)', repo: 'segmind/tiny-sd', steps: 15, guidance: 7.5, vram: 8 },
] as const;

const VIDEO_MODELS = [
  { id: 'ms-t2v-1.7b', name: 'ModelScope Text-to-Video 1.7B (MP4 · 16 Kare)', repo: 'ali-vilab/text-to-video-ms-1.7b', kind: 't2v', steps: 20, vram: 16 },
  { id: 'zeroscope-v2', name: 'Zeroscope v2 576w (MP4 · Geniş Ekran)', repo: 'cerspense/zeroscope_v2_576w', kind: 't2v', steps: 20, vram: 16 },
  { id: 'animatediff-lightning', name: 'AnimateDiff-Lightning (4 Adım · Hızlı)', repo: 'ByteDance/AnimateDiff-Lightning', kind: 'animatediff', steps: 4, vram: 16 },
] as const;

function buildTextScript(repo: string, userMessage: string): string {
  const msg = JSON.stringify(userMessage);
  return `import sys, subprocess, time, os
t0 = time.time()
try:
    import torch, transformers
except ImportError:
    print("[gpuhub] torch/transformers kuruluyor...", flush=True)
    subprocess.check_call([sys.executable, "-m", "pip", "install", "-q", "torch", "transformers", "accelerate"])
    import torch, transformers

from transformers import AutoTokenizer, AutoModelForCausalLM

device = "cuda" if torch.cuda.is_available() else "cpu"
dtype = torch.float16 if device == "cuda" else torch.float32
print(f"[gpuhub] cihaz={device} dtype={dtype} model=${repo}", flush=True)

tok = AutoTokenizer.from_pretrained(${JSON.stringify(repo)}, trust_remote_code=True)
model = AutoModelForCausalLM.from_pretrained(${JSON.stringify(repo)}, torch_dtype=dtype, low_cpu_mem_usage=True, trust_remote_code=True)
model = model.to(device)
print(f"[gpuhub] model yuklendi ({round(time.time()-t0, 1)}s)", flush=True)

messages = [
    {"role": "system", "content": "You are a concise, helpful assistant. Reply in the same language as the user."},
    {"role": "user", "content": ${msg}},
]
if hasattr(tok, "apply_chat_template"):
    prompt = tok.apply_chat_template(messages, tokenize=False, add_generation_prompt=True)
else:
    prompt = f"User: {${msg}}\\nAssistant:"

inputs = tok(prompt, return_tensors="pt").to(device)
with torch.no_grad():
    out = model.generate(**inputs, max_new_tokens=220, do_sample=True, temperature=0.7, top_p=0.9)
gen_ids = out[0][inputs["input_ids"].shape[1]:]
answer = tok.decode(gen_ids, skip_special_tokens=True).strip()

print("=== GPUHUB_MODEL_ANSWER_START ===", flush=True)
print(answer, flush=True)
print("=== GPUHUB_MODEL_ANSWER_END ===", flush=True)
print(f"[gpuhub] toplam_sure={round(time.time()-t0, 1)}s", flush=True)
`;
}

function buildImageScript(repo: string, steps: number, guidance: number, prompt: string): string {
  const p = JSON.stringify(prompt);
  return `import sys, subprocess, time, io, base64
t0 = time.time()
try:
    import torch, diffusers
    from PIL import Image
except ImportError:
    print("[gpuhub] diffusers/pillow kuruluyor...", flush=True)
    subprocess.check_call([sys.executable, "-m", "pip", "install", "-q", "torch", "diffusers", "transformers", "accelerate", "pillow"])
    import torch, diffusers
    from PIL import Image

from diffusers import AutoPipelineForText2Image

device = "cuda" if torch.cuda.is_available() else "cpu"
dtype = torch.float16 if device == "cuda" else torch.float32
print(f"[gpuhub] resim modeli yukleniyor: ${repo} ({device})", flush=True)

kwargs = {"torch_dtype": dtype}
if device == "cuda":
    kwargs["variant"] = "fp16"
try:
    pipe = AutoPipelineForText2Image.from_pretrained(${JSON.stringify(repo)}, **kwargs)
except Exception:
    pipe = AutoPipelineForText2Image.from_pretrained(${JSON.stringify(repo)}, torch_dtype=dtype)
pipe = pipe.to(device)
if hasattr(pipe, "set_progress_bar_config"):
    pipe.set_progress_bar_config(disable=True)
print(f"[gpuhub] model hazir ({round(time.time()-t0, 1)}s), uretiliyor...", flush=True)

img = pipe(prompt=${p}, num_inference_steps=${steps}, guidance_scale=${guidance}, width=512, height=512).images[0]
buf = io.BytesIO()
img.save(buf, format="PNG")
b64 = base64.b64encode(buf.getvalue()).decode("ascii")

print("=== GPUHUB_IMAGE_BASE64_START ===", flush=True)
print(b64, flush=True)
print("=== GPUHUB_IMAGE_BASE64_END ===", flush=True)
print(f"[gpuhub] toplam_sure={round(time.time()-t0, 1)}s", flush=True)
`;
}

function buildVideoScript(repo: string, kind: string, steps: number, prompt: string): string {
  const p = JSON.stringify(prompt);
  return `import sys, subprocess, time, io, base64, tempfile, os
t0 = time.time()
try:
    import torch, diffusers, imageio
    from PIL import Image
except ImportError:
    print("[gpuhub] diffusers/imageio kuruluyor...", flush=True)
    subprocess.check_call([sys.executable, "-m", "pip", "install", "-q", "torch", "diffusers", "transformers", "accelerate", "imageio", "imageio-ffmpeg", "pillow"])
    import torch, diffusers, imageio

device = "cuda" if torch.cuda.is_available() else "cpu"
dtype = torch.float16 if device == "cuda" else torch.float32
print(f"[gpuhub] video modeli yukleniyor: ${repo} ({device})", flush=True)

if ${JSON.stringify(kind)} == "animatediff":
    from diffusers import AnimateDiffPipeline, MotionAdapter, EulerDiscreteScheduler
    from huggingface_hub import hf_hub_download
    from safetensors.torch import load_file
    adapter = MotionAdapter().to(device, dtype)
    ckpt = hf_hub_download(${JSON.stringify(repo)}, "animatediff_lightning_4step_diffusers.safetensors")
    adapter.load_state_dict(load_file(ckpt, device=device))
    pipe = AnimateDiffPipeline.from_pretrained("emilianJR/epiCRealism", motion_adapter=adapter, torch_dtype=dtype).to(device)
    pipe.scheduler = EulerDiscreteScheduler.from_config(pipe.scheduler.config, timestep_spacing="trailing", beta_schedule="linear")
    frames = pipe(prompt=${p}, guidance_scale=1.0, num_inference_steps=${steps}, num_frames=16).frames[0]
else:
    from diffusers import DiffusionPipeline
    try:
        pipe = DiffusionPipeline.from_pretrained(${JSON.stringify(repo)}, torch_dtype=dtype, variant="fp16")
    except Exception:
        pipe = DiffusionPipeline.from_pretrained(${JSON.stringify(repo)}, torch_dtype=dtype)
    pipe = pipe.to(device)
    if hasattr(pipe, "enable_model_cpu_offload") and device == "cuda":
        pass
    frames = pipe(${p}, num_inference_steps=${steps}, num_frames=16).frames[0]

import numpy as np
np_frames = [np.array(f) if not isinstance(f, np.ndarray) else (f * 255).astype("uint8") if f.dtype != np.uint8 else f for f in frames]

out_path = os.path.join(tempfile.gettempdir(), "gpuhub_video.mp4")
fmt = "mp4"
try:
    imageio.mimsave(out_path, np_frames, fps=8)
except Exception:
    out_path = os.path.join(tempfile.gettempdir(), "gpuhub_video.gif")
    fmt = "gif"
    imageio.mimsave(out_path, np_frames, fps=8)

with open(out_path, "rb") as f:
    b64 = base64.b64encode(f.read()).decode("ascii")

print("=== GPUHUB_VIDEO_FMT ===" + fmt, flush=True)
print("=== GPUHUB_VIDEO_BASE64_START ===", flush=True)
print(b64, flush=True)
print("=== GPUHUB_VIDEO_BASE64_END ===", flush=True)
print(f"[gpuhub] toplam_sure={round(time.time()-t0, 1)}s", flush=True)
`;
}

function extractResult(logs: string | null | undefined): {
  answer: string;
  imageB64?: string;
  videoB64?: string;
  videoFmt?: 'mp4' | 'gif';
  meta: string;
} {
  if (!logs) return { answer: '(log yok)', meta: '' };
  const s = String(logs);

  const imgA = s.indexOf('=== GPUHUB_IMAGE_BASE64_START ===');
  const imgB = s.indexOf('=== GPUHUB_IMAGE_BASE64_END ===');
  if (imgA >= 0 && imgB > imgA) {
    const b64 = s.slice(imgA + '=== GPUHUB_IMAGE_BASE64_START ==='.length, imgB).replace(/\s+/g, '');
    const meta = (s.slice(0, imgA) + '\n' + s.slice(imgB + '=== GPUHUB_IMAGE_BASE64_END ==='.length)).trim();
    return { answer: 'Resim oluşturuldu:', imageB64: b64, meta };
  }

  const vidA = s.indexOf('=== GPUHUB_VIDEO_BASE64_START ===');
  const vidB = s.indexOf('=== GPUHUB_VIDEO_BASE64_END ===');
  if (vidA >= 0 && vidB > vidA) {
    const b64 = s.slice(vidA + '=== GPUHUB_VIDEO_BASE64_START ==='.length, vidB).replace(/\s+/g, '');
    const fmtMatch = s.match(/=== GPUHUB_VIDEO_FMT ===(mp4|gif)/);
    const fmt = (fmtMatch?.[1] as 'mp4' | 'gif') || 'mp4';
    const meta = (s.slice(0, vidA) + '\n' + s.slice(vidB + '=== GPUHUB_VIDEO_BASE64_END ==='.length)).trim();
    return { answer: 'Video oluşturuldu:', videoB64: b64, videoFmt: fmt, meta };
  }

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

type ChatTurn = {
  role: 'user' | 'assistant';
  text: string;
  imageB64?: string;
  videoB64?: string;
  videoFmt?: 'mp4' | 'gif';
  job?: Job;
  meta?: string;
};

export default function AITest() {
  const lang = useLang();
  const [providers, setProviders] = useState<Provider[]>([]);
  const [mode, setMode] = useState<TaskMode>('text');
  const [textModel, setTextModel] = useState<string>(TEXT_MODELS[0].id);
  const [imageModel, setImageModel] = useState<string>(IMAGE_MODELS[0].id);
  const [videoModel, setVideoModel] = useState<string>(VIDEO_MODELS[0].id);
  const [gpu, setGpu] = useState('T4');
  const [provider, setProvider] = useState('kaggle');
  const [message, setMessage] = useState('');
  const [turns, setTurns] = useState<ChatTurn[]>([]);
  const [busy, setBusy] = useState(false);
  const [status, setStatus] = useState('');
  const [liveLogs, setLiveLogs] = useState('');
  const stopRef = useRef(false);

  useEffect(() => {
    api.providers().then((ps) => {
      setProviders(ps);
      const withKeys = ps.filter((p) => providerKeyCount(p) > 0);
      const usable = withKeys.find((p) => p.id === 'kaggle') ?? withKeys[0];
      if (usable) setProvider(usable.id);
    }).catch(() => {});
  }, []);

  const selectedProvider = providers.find((p) => p.id === provider);
  const rawGPUs = !Array.isArray(selectedProvider?.capabilities) && selectedProvider?.capabilities?.gpus?.length
    ? selectedProvider.capabilities.gpus
    : [
        { type: 'T4', vram_gb: 16 },
        { type: 'A10', vram_gb: 24 },
        { type: 'L4', vram_gb: 24 },
        { type: 'L40S', vram_gb: 48 },
        { type: 'A100', vram_gb: 40 },
        { type: 'H100', vram_gb: 80 },
        { type: 'TPU', vram_gb: 0 },
        { type: 'CPU', vram_gb: 0 },
      ];
  const providerGPUs = rawGPUs.some((g) => g.type === 'CPU')
    ? rawGPUs
    : [...rawGPUs, { type: 'CPU', vram_gb: 0 }];

  useEffect(() => {
    if (providerGPUs.length > 0 && !providerGPUs.some((g) => g.type === gpu)) {
      setGpu(providerGPUs[0].type);
    }
  }, [provider, providerGPUs, gpu]);

  const activeModelMeta =
    mode === 'text'
      ? TEXT_MODELS.find((x) => x.id === textModel) ?? TEXT_MODELS[0]
      : mode === 'image'
        ? IMAGE_MODELS.find((x) => x.id === imageModel) ?? IMAGE_MODELS[0]
        : VIDEO_MODELS.find((x) => x.id === videoModel) ?? VIDEO_MODELS[0];

  async function send() {
    const text = message.trim();
    if (!text || busy) return;
    setMessage('');
    setTurns((prev) => [...prev, { role: 'user', text }]);
    setBusy(true);
    setLiveLogs('');
    setStatus(L(lang, { en: 'Submitting job to provider…', tr: 'Sağlayıcıya job gönderiliyor…' }));
    stopRef.current = false;
    const t0 = Date.now();
    try {
      let payload = '';
      if (mode === 'text') {
        const tm = TEXT_MODELS.find((x) => x.id === textModel) ?? TEXT_MODELS[0];
        payload = buildTextScript(tm.repo, text);
      } else if (mode === 'image') {
        const im = IMAGE_MODELS.find((x) => x.id === imageModel) ?? IMAGE_MODELS[0];
        payload = buildImageScript(im.repo, im.steps, im.guidance, text);
      } else {
        const vm = VIDEO_MODELS.find((x) => x.id === videoModel) ?? VIDEO_MODELS[0];
        payload = buildVideoScript(vm.repo, vm.kind, vm.steps, text);
      }

      const fallback = gpu === 'CPU' ? [] : gpu === 'TPU' ? ['CPU'] : gpu === 'T4' ? ['TPU', 'CPU'] : ['CPU'];
      const job = await api.createJob({
        type: 'script',
        payload,
        gpu_type: gpu,
        gpu_type_fallback: fallback,
        min_vram_gb: gpu === 'CPU' || gpu === 'TPU' ? 0 : activeModelMeta.vram,
        gpu_count: 1,
        max_duration_seconds: 1800,
        provider_preference: [provider],
      });
      setStatus(`${L(lang, { en: 'Job', tr: 'Job' })} #${job.id} - ${L(lang, { en: 'waiting for provider', tr: 'sağlayıcı bekleniyor' })}…`);

      const deadline = Date.now() + 20 * 60 * 1000;
      let last: Job = job;
      while (Date.now() < deadline && !stopRef.current) {
        await new Promise((r) => setTimeout(r, 2000));
        last = await api.job(String(job.id));
        const st = String(last.status ?? '').toLowerCase();
        const secs = Math.round((Date.now() - t0) / 1000);
        const providerName = last.provider_name ?? (typeof last.provider === 'object' ? last.provider?.id : last.provider) ?? '-';
        setStatus(`Job #${job.id}: ${st} · ${secs}s · ${providerName} · attempt ${Array.isArray(last.attempts) ? last.attempts.length : 0}`);
        if (last.logs) setLiveLogs(last.logs);
        if (['succeeded', 'failed', 'cancelled', 'timeout', 'unschedulable'].includes(st)) break;
      }
      try { last = await api.job(String(job.id)); } catch { /* keep last */ }
      const parsed = extractResult(last.logs);
      setLiveLogs('');
      setTurns((prev) => [
        ...prev,
        {
          role: 'assistant',
          text: parsed.answer,
          imageB64: parsed.imageB64,
          videoB64: parsed.videoB64,
          videoFmt: parsed.videoFmt,
          job: last,
          meta: parsed.meta,
        },
      ]);
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
            {L(lang, {
              en: 'Run Text, Image, or Video generation models on your free GPU/CPU providers.',
              tr: 'Ücretsiz GPU/CPU sağlayıcılarınız üzerinde Metin, Resim ve Video oluşturma modellerini çalıştırın.',
            })}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <Tabs
            value={mode}
            onChange={(v) => setMode(v as TaskMode)}
            tabs={[
              { id: 'text', label: L(lang, { en: 'Text / Chat', tr: 'Metin / Sohbet' }) },
              { id: 'image', label: L(lang, { en: 'Image Gen', tr: 'Resim Oluşturma' }) },
              { id: 'video', label: L(lang, { en: 'Video Gen', tr: 'Video Oluşturma' }) },
            ]}
          />
          <Button size="sm" variant="outline" onClick={() => setTurns([])}>
            <RotateCcw size={13} /> {L(lang, { en: 'Clear', tr: 'Temizle' })}
          </Button>
        </div>
      </div>

      <div className="grid gap-4 lg:grid-cols-[340px_1fr]">
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
              <Label htmlFor="ai-gpu">{L(lang, { en: 'Accelerator (GPU / TPU / CPU)', tr: 'Donanım (GPU / TPU / CPU)' })}</Label>
              <Select id="ai-gpu" value={gpu} onChange={(e) => setGpu(e.target.value)}>
                {providerGPUs.map((g) => (
                  <option key={g.type} value={g.type}>
                    {g.type === 'CPU'
                      ? L(lang, { en: 'CPU (No GPU / CPU-only)', tr: 'CPU (GPU’suz / Sadece İşlemci)' })
                      : g.type === 'TPU'
                        ? 'TPU (Tensor Processing Unit)'
                        : `${g.type}${g.vram_gb ? ` (${g.vram_gb} GB VRAM)` : ''}`}
                  </option>
                ))}
              </Select>
            </div>
            <div>
              <Label htmlFor="ai-model">
                {mode === 'text'
                  ? L(lang, { en: 'Text Model', tr: 'Metin Modeli' })
                  : mode === 'image'
                    ? L(lang, { en: 'Image Model', tr: 'Resim Modeli' })
                    : L(lang, { en: 'Video Model', tr: 'Video Modeli' })}
              </Label>
              {mode === 'text' && (
                <Select id="ai-model" value={textModel} onChange={(e) => setTextModel(e.target.value)}>
                  {TEXT_MODELS.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                </Select>
              )}
              {mode === 'image' && (
                <Select id="ai-model" value={imageModel} onChange={(e) => setImageModel(e.target.value)}>
                  {IMAGE_MODELS.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                </Select>
              )}
              {mode === 'video' && (
                <Select id="ai-model" value={videoModel} onChange={(e) => setVideoModel(e.target.value)}>
                  {VIDEO_MODELS.map((x) => <option key={x.id} value={x.id}>{x.name}</option>)}
                </Select>
              )}
            </div>
            <div className="rounded-lg border border-zinc-800 bg-zinc-900/50 p-3 text-[11px] leading-relaxed text-zinc-400">
              <div className="flex items-center gap-1.5 font-medium text-zinc-300"><Info size={12} /> {L(lang, { en: 'Warm Cache & Auto-Shutdown', tr: 'Hızlı Önbellek & Oto-Kapanma' })}</div>
              <ul className="mt-2 list-disc space-y-1 pl-4">
                <li>{L(lang, { en: 'First run caches the model; subsequent messages run in seconds on warm Modal/Colab containers.', tr: 'İlk çalıştırmada model önbelleğe alınır; sonraki mesajlar sıcak Modal/Colab sunucularında saniyeler içinde çalışır.' })}</li>
                <li>{L(lang, { en: 'Containers automatically shut down after 5–10 min of idle time so your quota is never drained 24/7.', tr: 'Sunucular 5–10 dk boşta kaldığında otomatik kapanır; kotanız 7/24 boşa harcanmaz.' })}</li>
                <li>{L(lang, { en: 'Uses pre-installed PyTorch & Transformers (zero C++ compilation).', tr: 'Hazır PyTorch & Transformers kullanır (C++ derleme beklemesi yok).' })}</li>
              </ul>
            </div>
          </CardContent>
        </Card>

        {/* Chat / Output */}
        <Card className="flex min-h-[540px] flex-col">
          <CardHeader className="flex-row items-center justify-between">
            <CardTitle className="flex items-center gap-2">
              {mode === 'text' ? <MessageSquare size={15} /> : mode === 'image' ? <ImageIcon size={15} /> : <Video size={15} />}
              {mode === 'text'
                ? L(lang, { en: 'Chat', tr: 'Sohbet' })
                : mode === 'image'
                  ? L(lang, { en: 'Image Studio', tr: 'Resim Stüdyosu' })
                  : L(lang, { en: 'Video Studio', tr: 'Video Stüdyosu' })}
            </CardTitle>
            <div className="flex items-center gap-2 text-[11px] text-zinc-500">
              <ProviderIcon id={provider} size={14} /> {provider} <Badge variant="neutral">{gpu}</Badge> <Badge variant="accent">{activeModelMeta.repo.split('/')[1]}</Badge>
            </div>
          </CardHeader>
          <CardContent className="flex flex-1 flex-col">
            <div className="min-h-[340px] flex-1 space-y-3 overflow-y-auto pr-1">
              {turns.length === 0 && (
                <div className="flex h-full items-center justify-center text-sm text-zinc-600">
                  {mode === 'text'
                    ? L(lang, { en: 'Send a message to start.', tr: 'Başlamak için bir mesaj gönderin.' })
                    : mode === 'image'
                      ? L(lang, { en: 'Describe the image you want to generate (e.g. "a cyberpunk cat in neon rain").', tr: 'Oluşturmak istediğiniz resmi tarif edin (örn. "a cyberpunk cat in neon rain").' })
                      : L(lang, { en: 'Describe the video scene to generate (e.g. "astronaut riding a horse on mars").', tr: 'Oluşturmak istediğiniz video sahnesini tarif edin (örn. "astronaut riding a horse on mars").' })}
                </div>
              )}
              {turns.map((t, i) => (
                <div key={i} className={`flex ${t.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  <div className={`max-w-[85%] rounded-xl px-3.5 py-2.5 text-sm ${
                    t.role === 'user' ? 'bg-emerald-500/15 text-emerald-100' : 'bg-zinc-800/80 text-zinc-200'
                  }`}>
                    <div className="whitespace-pre-wrap break-words">{t.text}</div>

                    {t.imageB64 && (
                      <div className="mt-2 space-y-2">
                        <img
                          src={`data:image/png;base64,${t.imageB64}`}
                          alt="Generated"
                          className="max-h-96 rounded-lg border border-zinc-700 object-contain"
                        />
                        <a
                          href={`data:image/png;base64,${t.imageB64}`}
                          download={`gpuhub-image-${Date.now()}.png`}
                          className="inline-flex items-center gap-1.5 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1 text-xs font-medium text-emerald-300 hover:bg-emerald-500/20"
                        >
                          <Download size={12} /> {L(lang, { en: 'Download PNG', tr: 'Resmi İndir (PNG)' })}
                        </a>
                      </div>
                    )}

                    {t.videoB64 && (
                      <div className="mt-2 space-y-2">
                        {t.videoFmt === 'gif' ? (
                          <img
                            src={`data:image/gif;base64,${t.videoB64}`}
                            alt="Generated animation"
                            className="max-h-96 rounded-lg border border-zinc-700"
                          />
                        ) : (
                          <video
                            src={`data:video/mp4;base64,${t.videoB64}`}
                            controls
                            autoPlay
                            loop
                            muted
                            className="max-h-96 rounded-lg border border-zinc-700"
                          />
                        )}
                        <a
                          href={`data:${t.videoFmt === 'gif' ? 'image/gif' : 'video/mp4'};base64,${t.videoB64}`}
                          download={`gpuhub-video-${Date.now()}.${t.videoFmt || 'mp4'}`}
                          className="inline-flex items-center gap-1.5 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-2.5 py-1 text-xs font-medium text-emerald-300 hover:bg-emerald-500/20"
                        >
                          <Download size={12} /> {L(lang, { en: 'Download Video', tr: 'Videoyu İndir' })}
                        </a>
                      </div>
                    )}

                    {t.job && (
                      <div className="mt-2 border-t border-zinc-700/60 pt-2 text-[11px] text-zinc-400">
                        <div className="flex flex-wrap items-center gap-2">
                          <Badge variant={t.job.status === 'succeeded' ? 'success' : 'danger'}>{t.job.status}</Badge>
                          <span>Job #{t.job.id}</span>
                          {t.job.provider && <span>provider={String(t.job.provider)}</span>}
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
            {status && (
              <div className="mt-2 space-y-2">
                <div className="flex items-center gap-2 text-xs text-emerald-400"><Loader2 size={13} className="animate-spin" />{status}</div>
                {liveLogs && (
                  <pre className="max-h-40 overflow-auto whitespace-pre-wrap rounded bg-zinc-950 p-2 text-[10px] leading-relaxed text-zinc-400">{liveLogs.slice(-1500)}</pre>
                )}
              </div>
            )}
            <form
              className="mt-3 flex items-center gap-2"
              onSubmit={(e) => { e.preventDefault(); void send(); }}
            >
              <Input
                value={message}
                onChange={(e) => setMessage(e.target.value)}
                placeholder={
                  mode === 'text'
                    ? L(lang, { en: 'Ask the model something…', tr: 'Modele bir şey sorun…' })
                    : mode === 'image'
                      ? L(lang, { en: 'Enter image prompt (English works best)…', tr: 'Resim promptu girin (İngilizce en iyi sonucu verir)…' })
                      : L(lang, { en: 'Enter video prompt (English works best)…', tr: 'Video promptu girin (İngilizce en iyi sonucu verir)…' })
                }
                disabled={busy}
              />
              <Button variant="primary" type="submit" disabled={busy || !message.trim()}>
                {busy ? <Loader2 size={15} className="animate-spin" /> : <Send size={15} />}
                {mode === 'text'
                  ? L(lang, { en: 'Send', tr: 'Gönder' })
                  : mode === 'image'
                    ? L(lang, { en: 'Generate', tr: 'Oluştur' })
                    : L(lang, { en: 'Generate', tr: 'Oluştur' })}
              </Button>
            </form>
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
