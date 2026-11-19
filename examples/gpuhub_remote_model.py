#!/usr/bin/env python3
"""
GPUHub -> remote model demo.

Sends a request from Python to the LOCAL GPUHub instance and asks it to run a
1B-class Q4 GGUF model (Qwen2.5-1.5B-Instruct, Q4_K_M) on a REMOTE provider GPU.

Provider chain: modal -> saturn.
  * modal's adapter fails honestly (Modal has no public job-submit REST API),
  * the scheduler fails over to Saturn, which creates a real Job on the Saturn
    Cloud community instance (g4dnxlarge / T4 16GB) and runs the script there.

Prereqs:
  GPUHUB_URL=http://localhost:8091  (local GPUHub, no docker: dev binary)
  Saturn key + Modal key in GPUHub DB (Providers -> Add key)
Run:
  python examples/gpuhub_remote_model.py
"""
import os
import sys
import time

try:
    import requests
except ImportError:
    sys.exit("pip install requests")

BASE = os.environ.get("GPUHUB_URL", "http://localhost:8091").rstrip("/")
USER = os.environ.get("GPUHUB_USER", "admin")
PASSWORD = os.environ.get("GPUHUB_PASSWORD", "x")
HEADERS = {"Content-Type": "application/json", "X-Requested-With": "XMLHttpRequest"}

# Runs INSIDE the Saturn Cloud job container (start_script, bash).
# Installs a CPU/GGUF runtime, downloads Qwen2.5-1.5B-Instruct Q4_K_M (~1 GB)
# and makes the model introduce itself, proving the model is actually loaded.
SCRIPT = r'''#!/bin/bash
set -e
pip install --quiet --disable-pip-version-check llama-cpp-python huggingface_hub
python - <<'PY'
import time, os
from huggingface_hub import hf_hub_download
from llama_cpp import Llama

t0 = time.time()
path = hf_hub_download("Qwen/Qwen2.5-1.5B-Instruct-GGUF",
                       filename="qwen2.5-1.5b-instruct-q4_k_m.gguf")
print(f"[gpuhub] model indirildi: {path} ({round(time.time()-t0,1)} s)", flush=True)

llm = Llama(model_path=path, n_ctx=1024, verbose=False)
print("[gpuhub] Q4_K_M model yuklendi", flush=True)

messages = [{"role": "system", "content": "You are a concise assistant. Answer in Turkish."},
            {"role": "user", "content": "Selam, kisaca kendini tanit."}]
out = llm.create_chat_completion(messages=messages, max_tokens=120, temperature=0.6)
print("==========================================", flush=True)
print("MODEL CEVABI:", out["choices"][0]["message"]["content"], flush=True)
print("==========================================", flush=True)
u = out.get("usage", {})
print(f"tokens: prompt={u.get('prompt_tokens')} completion={u.get('completion_tokens')}", flush=True)
print("GPUHUB_REMOTE_MODEL_OK", flush=True)
PY
'''


def login() -> str:
    r = requests.post(f"{BASE}/api/v1/auth/login", headers=HEADERS,
                      json={"username": USER, "password": PASSWORD}, timeout=15)
    r.raise_for_status()
    return r.json()["access_token"]


def main() -> int:
    token = login()
    auth = {**HEADERS, "Authorization": f"Bearer {token}"}

    print(f"GPUHub: {BASE}")
    print("Istek gonderiliyor: provider chain = modal -> saturn")
    r = requests.post(
        f"{BASE}/api/v1/jobs",
        headers=auth,
        json={
            "type": "script",
            "payload": SCRIPT,
            "gpu_type": "T4",
            "min_vram_gb": 16,
            "gpu_count": 1,
            "provider_preference": ["modal", "saturn"],
            "max_duration_seconds": 1800,
        },
        timeout=20,
    )
    print("submit:", r.status_code, r.text[:400])
    if r.status_code != 201:
        return 1
    jid = r.json()["id"]
    print(f"job #{jid} kuyruga alindi\n")

    deadline = time.time() + 30 * 60
    seen = ""
    while time.time() < deadline:
        r = requests.get(f"{BASE}/api/v1/jobs/{jid}", headers=auth, timeout=20)
        r.raise_for_status()
        detail = r.json()
        job = detail["job"]
        state = job.get("status", "?")
        if state != seen:
            print(f"state={state} provider={job.get('provider_id')} attempts={job.get('attempts')} remote_id={job.get('remote_id')}")
            seen = state
        if state in ("succeeded", "failed", "cancelled", "timeout", "unschedulable"):
            print("\n===== JOB LOGS =====")
            print(job.get("logs") or "(log yok)")
            print("=====================")
            if job.get("fail_reason"):
                print("fail_reason:", job["fail_reason"])
            for a in detail.get("attempts", []):
                print(f"attempt[{a['provider_id']}] status={a['status']} error={str(a.get('error'))[:300]}")
            return 0 if state == "succeeded" else 1
        time.sleep(15)
    print("zaman asimi; job hala calisiyor")
    return 2


if __name__ == "__main__":
    sys.exit(main())
