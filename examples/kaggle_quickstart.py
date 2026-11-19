#!/usr/bin/env python3
"""
GPUHub + Kaggle quickstart - run a tiny model on Kaggle's free T4, from your own Python code.

What it does:
  1. logs into GPUHub (env vars below)
  2. submits a script job restricted to the Kaggle provider (T4, 1 GPU, 16GB)
  3. polls the job until it finishes
  4. prints the job logs (device info + training loss curve)

The payload (TRAIN_PAYLOAD) is a self-contained tiny PyTorch linear-regression run that
executes INSIDE a private Kaggle kernel with 1x T4. Your Kaggle quota is only consumed
while the kernel actually runs (a few minutes).

Prereqs:
  - Kaggle key added in GPUHub: Providers -> Kaggle -> Add key
    credential: {"token":"KGAT_...","username":"<your-kaggle-username>"}
    (username is recommended so GPUHub never has to probe your account)
  - pip install requests
  - env:
      export GPUHUB_URL=http://localhost:8080
      export GPUHUB_USER=admin
      export GPUHUB_PASSWORD=change-me
  - run:
      python examples/kaggle_quickstart.py

Notes:
  - Kaggle free tier: ~30 GPU-hours/week, max 12h per session, phone-verified account.
  - The kernel is created as a PRIVATE kernel named "gpuhub-<timestamp>" on your Kaggle account.
    Delete it anytime at https://www.kaggle.com/code (filter: private, owner: you).
  - Long runs (>~9h) get killed by Kaggle; for resumable training submit with session_renew=True
    and read GPUHUB_ATTEMPT/GPUHUB_RESUME from the environment (see examples/submit_job.py, section 5).
"""
import os
import sys
import time

try:
    import requests
except ImportError:
    sys.exit("pip install requests")

BASE = os.environ.get("GPUHUB_URL", "http://localhost:8080").rstrip("/")
USER = os.environ.get("GPUHUB_USER", "admin")
PASSWORD = os.environ.get("GPUHUB_PASSWORD", "change-me")
TIMEOUT_S = int(os.environ.get("GPUHUB_QUICKSTART_TIMEOUT_S", str(20 * 60)))  # 20 min default

HEADERS = {
    "Content-Type": "application/json",
    "X-Requested-With": "XMLHttpRequest",  # CSRF protection - mandatory on mutations
}

# Runs ON the Kaggle kernel. Self-contained; only Kaggle's preinstalled packages are used.
TRAIN_PAYLOAD = r'''
import sys
import time

print("python:", sys.version.split()[0])

try:
    import torch
    print("torch:", torch.__version__)
    print("cuda available:", torch.cuda.is_available())
    device = "cuda" if torch.cuda.is_available() else "cpu"
    if device == "cuda":
        print("gpu:", torch.cuda.get_device_name(0))
        print("gpu count:", torch.cuda.device_count())
except ImportError:
    print("torch not preinstalled on this image; falling back to pure Python")
    torch = None
    device = "cpu"

if torch is not None:
    torch.manual_seed(0)
    # y = 2*x0 + 3*x1 + ... + 1*x9 + 1.0   -> learn the 10 coefficients
    true_w = torch.arange(1, 11, dtype=torch.float32, device=device)
    model = torch.nn.Linear(10, 1).to(device)
    opt = torch.optim.SGD(model.parameters(), lr=0.01)
    loss_fn = torch.nn.MSELoss()

    X = torch.randn(1024, 10, device=device)
    y = X @ true_w.view(10, 1) + 1.0

    t0 = time.time()
    for step in range(101):
        pred = model(X)
        loss = loss_fn(pred, y)
        opt.zero_grad()
        loss.backward()
        opt.step()
        if step % 25 == 0:
            print(f"step={step} loss={loss.item():.6f}")
    print(f"final loss: {loss.item():.6f} (trained in {time.time() - t0:.1f}s on {device})")
    print("GPUHUB_TRAIN_DONE")
else:
    data = [(i, i * 2 + 1) for i in range(1, 51)]
    preds = [w * 2 + 1 for w in range(1, 51)]
    err = sum((p - y) ** 2 for (x, y), p in zip(data, preds)) / len(data)
    print(f"pure-python mse: {err:.6f}")
    print("GPUHUB_TRAIN_DONE")
'''


def login() -> str:
    r = requests.post(f"{BASE}/api/v1/auth/login", headers=HEADERS,
                      json={"username": USER, "password": PASSWORD}, timeout=15)
    r.raise_for_status()
    return r.json()["access_token"]


def auth(token: str) -> dict:
    return {**HEADERS, "Authorization": f"Bearer {token}"}


def main() -> int:
    print(f"GPUHub: {BASE}  user: {USER}")
    token = login()

    print("submitting tiny-model job to Kaggle (T4)...")
    r = requests.post(
        f"{BASE}/api/v1/jobs",
        headers=auth(token),
        json={
            "type": "script",
            "payload": TRAIN_PAYLOAD,
            "gpu_type": "T4",
            "min_vram_gb": 16,
            "gpu_count": 1,
            "provider_preference": ["kaggle"],
            "max_duration_seconds": 1800,
        },
        timeout=15,
    )
    if r.status_code != 201:
        print("submit failed:", r.status_code, r.text[:500])
        return 1
    job_id = r.json()["id"]
    print(f"job #{job_id} queued")

    print("polling (Kaggle kernels are queued; typically starts within a few minutes)...")
    deadline = time.time() + TIMEOUT_S
    last_state = ""
    while time.time() < deadline:
        r = requests.get(f"{BASE}/api/v1/jobs/{job_id}", headers=auth(token), timeout=15)
        r.raise_for_status()
        job = r.json()["job"]
        state = job.get("status", "unknown")
        if state != last_state:
            print(f"state: {state} (attempt {job.get('attempts')}, provider={job.get('provider_id')})")
            last_state = state
        if state in ("succeeded", "failed", "cancelled", "timeout", "unschedulable"):
            print("\n================ job logs ================")
            print(job.get("logs") or "(no logs yet - kernel may still be finalizing output)")
            print("==========================================")
            if job.get("fail_reason"):
                print("fail_reason:", job["fail_reason"])
            for a in r.json().get("attempts", []):
                if a.get("error"):
                    print(f"attempt[{a.get('provider_id')}] error: {a['error'][:300]}")
            return 0 if state == "succeeded" else 1
        time.sleep(15)

    print(f"timed out after {TIMEOUT_S}s; job #{job_id} is still '{last_state}'. Check the UI: {BASE}/jobs/{job_id}")
    return 2


if __name__ == "__main__":
    sys.exit(main())
