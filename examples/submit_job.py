#!/usr/bin/env python3
"""
GPUHub job submission example - submit from your own Python code.

Shows:
  1. Exact GPU request (T4 + 16GB + 2 GPUs)
  2. GPU fallback chain: A100 → H100 → L40S → T4 (scheduler picks the first available)
  3. VRAM-only request (any GPU type, just >= 24GB)
  4. Restricted providers (provider_preference)
  5. session_renew for long training runs on ~9h providers (checkpoint/resume)

Usage:
  pip install requests
  export GPUHUB_URL=http://localhost:8080
  export GPUHUB_USER=admin
  export GPUHUB_PASSWORD=change-me
  python examples/submit_job.py
"""
import os
import sys

try:
    import requests
except ImportError:
    sys.exit("pip install requests")

BASE = os.environ.get("GPUHUB_URL", "http://localhost:8080").rstrip("/")
USER = os.environ.get("GPUHUB_USER", "admin")
PASSWORD = os.environ.get("GPUHUB_PASSWORD", "admin")

HEADERS = {
    "Content-Type": "application/json",
    "X-Requested-With": "XMLHttpRequest",  # CSRF protection - mandatory on mutations
}

def login() -> str:
    r = requests.post(f"{BASE}/api/v1/auth/login", headers=HEADERS,
                      json={"username": USER, "password": PASSWORD}, timeout=15)
    r.raise_for_status()
    return r.json()["access_token"]

def auth(token: str) -> dict:
    return {**HEADERS, "Authorization": f"Bearer {token}"}

def submit(token: str, **kwargs) -> dict:
    r = requests.post(f"{BASE}/api/v1/jobs", headers=auth(token), json=kwargs, timeout=15)
    print("→", r.status_code, r.text[:300])
    r.raise_for_status()
    return r.json()

if __name__ == "__main__":
    tok = login()

    # 1) Exact GPU: 1x T4, 16GB
    submit(tok, type="script",
           payload="print('gpuhub ok')",
           gpu_type="T4", min_vram_gb=16, gpu_count=1)

    # 2) GPU fallback chain - tries A100 first, then H100, then L40S, then T4
    submit(tok, type="script",
           payload="print('fallback chain')",
           gpu_type="A100",
           gpu_type_fallback=["H100", "L40S", "T4"],
           min_vram_gb=16, gpu_count=1,
           provider_preference=["modal", "lightning", "saturn", "kaggle", "paperspace", "runner"])

    # 3) VRAM-only: ANY GPU with >= 24GB, no type preference
    submit(tok, type="script",
           payload="print('any big gpu')",
           gpu_type="any", min_vram_gb=24, gpu_count=1)

    # 4) VRAM-only with explicit GPU chain under the hood (multi-GPU training on any card)
    submit(tok, type="script",
           payload="print('2x big gpu')",
           gpu_type="any", min_vram_gb=24, gpu_count=2,
           provider_preference=["modal"])

    # 5) Long training with session renew (checkpoint/resume across ~9h providers)
    submit(tok, type="script",
           payload=(
               "import os, torch\n"
               "print('attempt', os.environ.get('GPUHUB_ATTEMPT'), 'resume', os.environ.get('GPUHUB_RESUME'))\n"
               # ... your training loop + torch.save checkpoint every N minutes ...
           ),
           gpu_type="A100", gpu_type_fallback=["H100", "L40S"],
           min_vram_gb=40, gpu_count=1, session_renew=True,
           max_duration_seconds=43200)

    # 6) Docker image job
    submit(tok, type="docker",
           payload="nvidia/cuda:12.4.0-base-ubuntu22.04 nvidia-smi",
           gpu_type="any", min_vram_gb=8, gpu_count=1)
