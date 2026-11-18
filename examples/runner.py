#!/usr/bin/env python3
"""
GPUHub Self-hosted Runner - deploy this inside ANY environment that gives you a GPU:
  • a Kaggle / Colab notebook cell (paste it and run)
  • a Modal container (@app.function(gpu="T4"))
  • a Lightning studio / Saturn / Paperspace notebook
  • your own PC/server with an NVIDIA GPU (RTX 4090, A100...) or an AMD GPU (ROCm)
  • a cheap/always-free CPU box to smoke the flow without GPU

It exposes a tiny authenticated HTTP API that GPUHub talks to:

  GET  /health            → {"status":"ok","gpus":[...], "gpu_type":...}
  POST /jobs              → {"id":"r-xxxx","status":"queued"}
  GET  /jobs/{id}         → {"id":..., "state":"running|succeeded|failed|timeout|cancelled", "cost_usd_micros":0}
  GET  /jobs/{id}/logs    → text/plain
  POST /jobs/{id}/cancel  → {"status":"cancelled"}

Auth: every request must carry `Authorization: Bearer <GPUHUB_RUNNER_TOKEN>`.

Usage:
  pip install fastapi uvicorn    (or just run with --stdlib)
  export GPUHUB_RUNNER_TOKEN=$(openssl rand -hex 24)
  python runner.py --port 8787 &        # or in a notebook cell
  # expose it:  ngrok http 8787   (or cloudflared tunnel --url http://localhost:8787)
  # GPUHub → Providers → Add key on "Self-hosted Runner":
  #   credential = {"base_url":"https://xxxx.ngrok.io","token":"<GPUHUB_RUNNER_TOKEN>"}
  #   (proxy binding is OPTIONAL now - bind one for sticky egress, or leave direct)

Env vars:
  GPUHUB_RUNNER_TOKEN   bearer token GPUHub sends (required)
  GPUHUB_GPU_TYPES      comma list to ADVERTISE, e.g. "T4,RTX4090" (default: autodetect via nvidia-smi)
  GPUHUB_MAX_JOBS       parallel jobs cap (default 1 on free machines)
  GPUHUB_ALLOWED_ROOTS  comma paths for payload file execution (default: cwd, /tmp)
  GPUHUB_DOCKER         1 = allow payload type=docker (docker run) (default: 0)

Job payload semantics (type field):
  script   → payload is a python script body; written to /tmp and run
  command  → payload is run via /bin/sh -c
  docker   → payload is a docker image reference (needs GPUHUB_DOCKER=1)
  notebook → payload is a path/URL to a .ipynb (nbconvert --execute, best effort)

Checkpoints across the ~9h session wall:
  Free providers kill your session (Kaggle 9h cap, Colab ~12h, Studio Lab 4h).
  GPUHub re-queues on unexpected death (same failover machinery). Your script sees:
    GPUHUB_ATTEMPT=<n>   attempt number (0 = first)
    GPUHUB_RESUME=1      on retry (n>0)
  …so if your payload saves a checkpoint every N minutes (torch.save) and reloads it
  when GPUHUB_RESUME=1, a long run survives session breaks. See README "9h sınırını aşmak".
"""
import argparse
import json
import os
import shlex
import subprocess
import sys
import tempfile
import threading
import time
import uuid
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

TMPDIR = tempfile.gettempdir()

TOKEN = os.environ.get("GPUHUB_RUNNER_TOKEN", "")
MAX_JOBS = int(os.environ.get("GPUHUB_MAX_JOBS", "1"))
DOCKER_OK = os.environ.get("GPUHUB_DOCKER", "0") == "1"

JOBS = {}
LOCK = threading.Lock()


def running_jobs():
    return sum(1 for j in JOBS.values() if j["state"] in ("queued", "running"))


def detect_gpus():
    types_env = os.environ.get("GPUHUB_GPU_TYPES", "").strip()
    if types_env:
        out = []
        for t in types_env.split(","):
            t = t.strip()
            if t:
                out.append({"type": t, "vram_gb": 16 if t in ("T4", "P100") else 24})
        return out
    try:
        result = subprocess.run(["nvidia-smi", "--query-gpu=name,memory.total", "--format=csv,noheader,nounits"],
                                capture_output=True, text=True, timeout=10)
        gpus = []
        for line in result.stdout.strip().splitlines():
            name, mem = [x.strip() for x in line.split(",")[:2]]
            gpus.append(gpu_entry(name, int(float(mem) // 1024)))
        return gpus
    except Exception:
        return []


def gpu_entry(name: str, vram_gb: int):
    n = name.upper()
    if "T4" in n: name = "T4"
    elif "P100" in n: name = "P100"
    elif "V100" in n: name = "V100"
    elif "A100" in n: name = "A100"
    elif "A10" in n: name = "A10"
    elif "L4" in n: name = "L4"
    elif "L40S" in n or "L40 S" in n: name = "L40S"
    elif "H100" in n: name = "H100"
    elif "H200" in n: name = "H200"
    elif "RTX 4090" in n or "RTX4090" in n: name = "RTX4090"
    elif "RTX PRO 6000" in n: name = "RTX6000Pro"
    elif "MI300X" in n or "MI300" in n: name = "MI300X"
    elif "GAUDI" in n: name = "Gaudi2"
    elif "RTX A" in n: name = "RTX6000Ada"
    else: name = name.replace("NVIDIA ", "").replace(" ", "_")
    return {"type": name, "vram_gb": vram_gb}


def render_command(job):
    jtype = job["type"]
    payload = job["payload"]
    if jtype == "script":
        path = TMPDIR + f"/gpuhub_job_{job['id']}.py"
        with open(path, "w", encoding="utf-8") as f:
            f.write(payload)
        return [sys.executable, path], None
    if jtype == "command":
        return ["/bin/sh", "-c", payload], None
    if jtype == "docker":
        if not DOCKER_OK:
            return None, "docker payload requested but GPUHUB_DOCKER!=1"
        # payload is an image reference; GPU passed through if nvidia-smi works
        return ["docker", "run", "--rm", "--gpus", "all", "--net=host", payload], None
    if jtype == "notebook":
        target = payload.strip()
        if target.startswith(("http://", "https://")):
            out = TMPDIR + f"/gpuhub_job_{job['id']}.ipynb"
            code = (
                "import urllib.request,sys\n"
                f"urllib.request.urlretrieve({target!r}, {out!r})\n"
            )
            return [sys.executable, "-c", code + f"\nimport os\nos.system('jupyter nbconvert --to notebook --execute --inplace ' + {out!r})"], None
        return ["jupyter", "nbconvert", "--to", "notebook", "--execute", "--inplace", target], None
    return None, f"unknown job type {jtype!r}"


class Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def _auth(self):
        h = self.headers.get("Authorization", "")
        return TOKEN and h == f"Bearer {TOKEN}"

    def _send(self, code, body, ctype="application/json"):
        if isinstance(body, (dict, list)):
            body = json.dumps(body).encode()
        else:
            body = body.encode() if isinstance(body, str) else body
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, fmt, *args):
        sys.stderr.write("[runner] " + fmt % args + "\n")

    def do_GET(self):
        if not self._auth():
            return self._send(401, {"error": "unauthorized"})
        if self.path == "/health":
            return self._send(200, {"status": "ok", "gpus": detect_gpus(),
                                    "max_jobs": MAX_JOBS, "running": running_jobs()})
        parts = self.path.split("/")
        # /jobs/{id} or /jobs/{id}/logs
        if len(parts) >= 3 and parts[1] == "jobs":
            jid = parts[2]
            with LOCK:
                job = JOBS.get(jid)
            if not job:
                return self._send(404, {"error": "job not found"})
            if len(parts) == 4 and parts[3] == "logs":
                return self._send(200, job["log"], "text/plain; charset=utf-8")
            return self._send(200, {"id": job["id"], "state": job["state"],
                                    "message": job.get("message", ""),
                                    "cost_usd_micros": job.get("cost", 0)})
        self._send(404, {"error": "not found"})

    def do_POST(self):
        if not self._auth():
            return self._send(401, {"error": "unauthorized"})
        length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(length) if length else b""
        try:
            body = json.loads(raw or b"{}")
        except json.JSONDecodeError:
            return self._send(400, {"error": "invalid JSON"})

        if self.path == "/jobs":
            with LOCK:
                if running_jobs() >= MAX_JOBS:
                    return self._send(429, {"error": "runner busy (max_jobs)"})
                jid = "r-" + uuid.uuid4().hex[:10]
                job = {
                    "id": jid, "state": "queued", "payload": body.get("payload", ""),
                    "type": body.get("type", "script"), "gpu_type": body.get("gpu_type", "any"),
                    "gpu_count": int(body.get("gpu_count", 1)), "min_vram_gb": body.get("min_vram_gb", 0),
                    "max_duration_seconds": int(body.get("max_duration_seconds", 3600)),
                    "env": body.get("env", {}), "log": "", "message": "",
                    "created": time.time(),
                }
                JOBS[jid] = job
            threading.Thread(target=self._execute, args=(job,), daemon=True).start()
            return self._send(201, {"id": jid, "status": "queued"})

        parts = self.path.split("/")
        if len(parts) == 4 and parts[1] == "jobs" and parts[3] == "cancel":
            jid = parts[2]
            with LOCK:
                job = JOBS.get(jid)
                if not job:
                    return self._send(404, {"error": "job not found"})
                job["cancel_requested"] = True
                proc = job.get("proc")
                if proc and job["state"] == "running":
                    try:
                        proc.terminate()
                    except Exception:
                        pass
                job["state"] = "cancelled"
                job["message"] = "cancelled by gpuhub"
            return self._send(200, {"status": "cancelled"})
        self._send(404, {"error": "not found"})

    def _execute(self, job):
        env = os.environ.copy()
        env.update({k: str(v) for k, v in job.get("env", {}).items()})
        cmd, err = render_command(job)
        if err:
            with LOCK:
                job["state"] = "failed"; job["message"] = err
                job["log"] += "[runner] " + err + "\n"
            return
        with LOCK:
            job["state"] = "running"
        timeout = job["max_duration_seconds"]
        try:
            proc = subprocess.Popen(cmd, stdout=subprocess.PIPE, stderr=subprocess.STDOUT,
                                    text=True, env=env, bufsize=1)
            job["proc"] = proc
            assert proc.stdout is not None
            deadline = time.time() + timeout
            for line in proc.stdout:
                with LOCK:
                    job["log"] += line
                    if len(job["log"]) > 8 << 20:
                        job["log"] = job["log"][-4 << 20:]
                if job.get("cancel_requested"):
                    proc.terminate(); break
                if time.time() > deadline:
                    proc.kill()
                    with LOCK:
                        job["state"] = "timeout"; job["message"] = "max_duration exceeded"
                    return
            rc = proc.wait(timeout=30)
            with LOCK:
                if job["state"] in ("cancelled", "timeout"):
                    return
                job["state"] = "succeeded" if rc == 0 else "failed"
                job["message"] = f"exit code {rc}"
                job["log"] += f"\n[runner] exited with code {rc}\n"
        except Exception as e:
            with LOCK:
                job["state"] = "failed"; job["message"] = str(e)
                job["log"] += f"[runner] error: {e}\n"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--port", type=int, default=8787)
    ap.add_argument("--host", default="127.0.0.1")
    args = ap.parse_args()
    if not TOKEN:
        print("GPUHUB_RUNNER_TOKEN is required (openssl rand -hex 24)", file=sys.stderr)
        sys.exit(1)
    print(f"GPUHub runner on {args.host}:{args.port} - gpus: {detect_gpus()}", flush=True)
    srv = ThreadingHTTPServer((args.host, args.port), Handler)
    try:
        srv.serve_forever()
    except KeyboardInterrupt:
        print("bye")


if __name__ == "__main__":
    main()
