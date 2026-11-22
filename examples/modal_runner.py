#!/usr/bin/env python3
"""
GPUHub -- Modal Runner.

Deploys an HTTP endpoint inside your Modal account; GPUHub's "Self-hosted Runner"
provider then drives your jobs through Modal's GPU infrastructure ($30/mo free compute).

Usage:
  1. pip install modal fastapi
  2. modal token new                (writes MODAL_TOKEN_ID + MODAL_TOKEN_SECRET to ~/.modal.toml)
  3. modal deploy examples/modal_runner.py
  4. Copy the printed URL into GPUHub -> Providers -> Self-hosted Runner -> Add key
     credential = {"base_url": "https://<your-name>--gpuhub-runner.modal.run", "token": "<the token you set below>"}
  5. security: set GPUHUB_RUNNER_TOKEN env var on this Modal app (secrets)

Endpoints:
  GET  /health          -> {"status":"ok","token":...,"gpus":[...]}
  POST /jobs            -> {"id":"r-..."}
  GET  /jobs/{id}
  GET  /jobs/{id}/logs
  POST /jobs/{id}/cancel
"""
import os
import sys
import subprocess
import json
import time
import uuid

import modal
import fastapi

image = (
    modal.Image.debian_slim(python_version="3.11")
    .pip_install(["fastapi>=0.110", "pynvml>=11.4", "psutil>=5.9"][:2])
)

app = modal.App("gpuhub-runner", image=image)

TOKEN = os.environ.get("GPUHUB_RUNNER_TOKEN", "change-me-set-a-random-string")
JOBS = {}  # id -> dict(process, log, state)


def run_command(job_id: str, spec: dict):
    job = JOBS[job_id]
    job["state"] = "running"
    job["log"] = "job started\n"
    payload = spec.get("payload", "")
    jtype = spec.get("type", "script")

    if jtype == "script":
        body = payload
        script_path = f"/tmp/gpuhub_modal_{job_id}.py"
        with open(script_path, "w", encoding="utf-8") as f:
            f.write(body)
        cmd = [sys.executable, script_path]
    elif jtype == "command":
        cmd = ["/bin/sh", "-c", payload]
    elif jtype == "notebook":
        cmd = ["python", "-c", "import os,sys; print('notebook payload unsupported on modal runner, use script')"]
    elif jtype == "docker":
        cmd = ["/bin/sh", "-c", f"echo 'docker payload passed to modal runtime as-is: {payload!r}'"]
    else:
        cmd = ["/bin/sh", "-c", payload]

    env = os.environ.copy()
    for k, v in (spec.get("env") or {}).items():
        env[str(k)] = str(v)

    try:
        proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, env=env)
        for line in proc.stdout:
            job["log"] += line
        rc = proc.wait()
        if job["state"] != "cancelled":
            job["state"] = "succeeded" if rc == 0 else "failed"
        job["log"] += f"\n[gpuhub-modal] exit code: {rc}\n"
    except Exception as e:
        job["log"] += f"[gpuhub-modal] error: {e}\n"
        job["state"] = "failed"


@app.function(
    image=image,
    gpu=os.environ.get("GPUHUB_MODAL_GPU", "T4"),
)
@modal.concurrent(max_inputs=5)
@modal.asgi_app()
def fastapi_app() -> fastapi.FastAPI:
    api = fastapi.FastAPI()

    @api.get("/health")
    def health():
        try:
            import pynvml  # type: ignore
            pynvml.nvmlInit()
            gpus = []
            for i in range(pynvml.nvmlDeviceGetCount()):
                h = pynvml.nvmlDeviceGetHandleByIndex(i)
                name = pynvml.nvmlDeviceGetName(h).decode()
                mem = pynvml.nvmlDeviceGetMemoryInfo(h).total >> 20
                gpus.append({"type": _canon(name), "vram_gb": int(mem // 1024)})
            pynvml.nvmlShutdown()
        except Exception:
            gpus = [{"type": "T4", "vram_gb": 16}]
        return {"status": "ok", "runner": "modal", "gpus": gpus}

    @api.post("/jobs")
    async def submit(req: dict, authorization: str = fastapi.Header(default="")):
        if authorization != f"Bearer {TOKEN}":
            raise fastapi.HTTPException(401, "bad token")
        job_id = "r-" + uuid.uuid4().hex[:10]
        JOBS[job_id] = {"state": "queued", "log": "", "spec": req}
        import threading
        threading.Thread(target=run_command, args=(job_id, req), daemon=True).start()
        return {"id": job_id, "status": "queued"}

    @api.get("/jobs/{job_id}")
    def job_status(job_id: str, authorization: str = fastapi.Header(default="")):
        if authorization != f"Bearer {TOKEN}":
            raise fastapi.HTTPException(401, "bad token")
        j = JOBS.get(job_id)
        if not j:
            raise fastapi.HTTPException(404, "not found")
        return {"id": job_id, "state": j["state"], "cost_usd_micros": 0}

    @api.get("/jobs/{job_id}/logs")
    def job_logs(job_id: str, authorization: str = fastapi.Header(default="")):
        if authorization != f"Bearer {TOKEN}":
            raise fastapi.HTTPException(401, "bad token")
        j = JOBS.get(job_id)
        if not j:
            raise fastapi.HTTPException(404, "not found")
        return fastapi.Response(j.get("log", ""), media_type="text/plain; charset=utf-8")

    @api.post("/jobs/{job_id}/cancel")
    def job_cancel(job_id: str, authorization: str = fastapi.Header(default="")):
        if authorization != f"Bearer {TOKEN}":
            raise fastapi.HTTPException(401, "bad token")
        j = JOBS.get(job_id)
        if not j:
            raise fastapi.HTTPException(404, "not found")
        j["state"] = "cancelled"
        return {"status": "cancelled"}

    return api


def _canon(name: str) -> str:
    n = name.upper()
    if "A100" in n: return "A100"
    if "H100" in n: return "H100"
    if "L4" in n: return "L4"
    if "T4" in n: return "T4"
    if "A10" in n: return "A10"
    if "L40" in n: return "L40S"
    if "RTX 4090" in n: return "RTX4090"
    if "RTX A6000" in n or "RTX 6000" in n: return "RTX6000Ada"
    return name.replace("NVIDIA ", "").replace(" ", "_")
