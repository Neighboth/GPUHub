#!/usr/bin/env python3
"""
1B Q4 model demo - open a local 1B Q4 model and say hello, from Python.

Runner: Ollama (local AI system on this machine).
Model:  qwen2.5:1.5b - Qwen2.5-1.5B, Q4_K_M quantization (~1 GB), CPU (no GPU here).

Prereq: Ollama installed (winget install Ollama.Ollama, or downloaded from
https://ollama.com) and model present: `ollama pull qwen2.5:1.5b`.
Ollama API endpoint: http://127.0.0.1:11434

Usage:
    python examples/q4_model_demo.py
"""
import time

import requests

BASE = "http://127.0.0.1:11434"
MODEL = "qwen2.5:1.5b"
PROMPT = "Selam, kısaca kendini tanıt."  # Turkish greeting


def main() -> None:
    # 0. verify the model is pulled
    tags = requests.get(f"{BASE}/api/tags", timeout=10).json()
    names = [m["name"] for m in tags.get("models", [])]
    print("ollama modeller:", names if names else "(boş)")
    assert MODEL in names, f"model {MODEL} çekilmemiş. Queue: ollama pull {MODEL}"

    print(f'request gönderiliyor -> {MODEL}: "{PROMPT}"')
    t0 = time.time()
    r = requests.post(
        f"{BASE}/api/generate",
        json={
            "model": MODEL,
            "prompt": PROMPT,
            "stream": False,
            "options": {"num_ctx": 2048, "temperature": 0.6, "num_predict": 256},
        },
        timeout=300,
    )
    r.raise_for_status()
    d = r.json()
    text = d.get("response", "")
    load = d.get("total_duration", 0) / 1e9
    prompt_tok = d.get("prompt_eval_count", 0)
    eval_tok = d.get("eval_count", 0)
    eval_sec = d.get("eval_duration", 0) / 1e9

    print("\n======== model cevabı ========")
    print(text)
    print("================================")
    print(f"prompt_tokens={prompt_tok} completion_tokens={eval_tok}")
    print(f"yükleme+yanit süresi: {round(time.time() - t0, 2)} s (çalışma süresi: {load:.2f} s)")
    if eval_sec > 0:
        print(f"yanit hızı: {round(eval_tok / eval_sec, 1)} token/s (16B/s gibi)")
    print("model:", d.get("model"))
    print("bitti_yesil/done:", d.get("done_reason"))


if __name__ == "__main__":
    main()
