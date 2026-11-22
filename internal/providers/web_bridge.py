#!/usr/bin/env python3
"""
GPUHub provider bridge for Modal (Warm Sandbox SDK + Volume cache) and Google Colab (OAuth2 + /tun/m/*).

Reads one JSON request on stdin, writes one JSON response on stdout.
"""

import base64
import json
import os
import socket
import ssl
import struct
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid

# Official Google Colab CLI OAuth2 client configuration (public desktop client).
COLAB_CLIENT_ID = "764086051850-6qr4p6gpi6hn506pt8ejuq83di341hur.apps.googleusercontent.com"
COLAB_CLIENT_SECRET = "d-FL95Q19q7MQmFpd7hHD0Ty"
COLAB_REDIRECT_URI = "http://localhost:8080/oauth/callback"
COLAB_DOMAIN = "https://colab.research.google.com"
COLAB_STATE_FILE = os.path.join(tempfile.gettempdir(), "gpuhub_colab_sessions.json")
MODAL_WARM_FILE = os.path.join(tempfile.gettempdir(), "gpuhub_modal_warm.json")
COLAB_IDLE_SECONDS = 600   # 10 min idle auto-shutdown (prevents 24/7 quota drain)
MODAL_IDLE_SECONDS = 300   # 5 min idle auto-shutdown enforced by Modal


def fail(msg):
    print(json.dumps({"error": msg}))
    sys.exit(1)


# ---------------------------------------------------------------------------
# Google Colab (OAuth2 + /tun/m/* + Warm Runtime Reuse + Jupyter WSS)
# ---------------------------------------------------------------------------

def _http_json(method, url, headers=None, data=None, timeout=30):
    hdrs = dict(headers or {})
    try:
        import requests as _req
        kw = {"headers": hdrs, "timeout": timeout}
        if data is not None:
            if isinstance(data, (dict, list)):
                kw["json"] = data
            else:
                kw["data"] = data
        resp = _req.request(method, url, **kw)
        raw = resp.text or ""
        if resp.status_code >= 400:
            path_only = urllib.parse.urlparse(url).path
            raise RuntimeError(f"HTTP {resp.status_code} ({method} {path_only}): {raw[:300]}")
        if raw.startswith(")]}'\n"):
            raw = raw[5:]
        return resp.status_code, (json.loads(raw) if raw.strip() else {})
    except ImportError:
        pass

    body = None
    if data is not None:
        if isinstance(data, (dict, list)):
            body = json.dumps(data).encode("utf-8")
            hdrs.setdefault("Content-Type", "application/json")
        elif isinstance(data, str):
            body = data.encode("utf-8")
        else:
            body = data
    elif method in ("POST", "PUT"):
        body = b""
        hdrs.setdefault("Content-Length", "0")
    req = urllib.request.Request(url, data=body, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            if raw.startswith(")]}'\n"):
                raw = raw[5:]
            return resp.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        path_only = urllib.parse.urlparse(url).path
        raise RuntimeError(f"HTTP {e.code} ({method} {path_only}): {raw[:300]}")


def _colab_exchange_token(req):
    """Return a fresh Google OAuth2 access_token from refresh_token, code, or access_token."""
    tok = (req.get("refresh_token") or req.get("token") or req.get("password") or "").strip()
    code = (req.get("code") or "").strip()
    redirect_uri = COLAB_REDIRECT_URI
    for candidate in (code, tok):
        if "code=" in candidate:
            parsed = urllib.parse.urlparse(candidate if "://" in candidate else "http://" + candidate)
            qs = urllib.parse.parse_qs(parsed.query)
            if qs.get("code"):
                code = qs["code"][0]
                if parsed.scheme and parsed.netloc:
                    redirect_uri = f"{parsed.scheme}://{parsed.netloc}{parsed.path}"
                break
    if tok.startswith("ya29."):
        return tok
    if tok.startswith("4/") and not code:
        code = tok

    if code:
        form = urllib.parse.urlencode({
            "code": code,
            "client_id": COLAB_CLIENT_ID,
            "client_secret": COLAB_CLIENT_SECRET,
            "redirect_uri": redirect_uri,
            "grant_type": "authorization_code",
        }).encode("utf-8")
        _, res = _http_json("POST", "https://oauth2.googleapis.com/token",
                            headers={"Content-Type": "application/x-www-form-urlencoded"}, data=form)
        if not res.get("access_token"):
            raise RuntimeError("OAuth code exchange failed: " + json.dumps(res)[:200])
        return res["access_token"]

    if not tok:
        raise RuntimeError("Google Colab credential must include an OAuth refresh_token (1//...) or auth code (4/...)")

    form = urllib.parse.urlencode({
        "refresh_token": tok,
        "client_id": COLAB_CLIENT_ID,
        "client_secret": COLAB_CLIENT_SECRET,
        "grant_type": "refresh_token",
    }).encode("utf-8")
    _, res = _http_json("POST", "https://oauth2.googleapis.com/token",
                        headers={"Content-Type": "application/x-www-form-urlencoded"}, data=form)
    if not res.get("access_token"):
        raise RuntimeError("OAuth token refresh failed: " + json.dumps(res)[:200])
    return res["access_token"]


def _colab_headers(access_token, extra=None):
    h = {
        "Authorization": f"Bearer {access_token}",
        "Accept": "application/json",
        "X-Colab-Client-Agent": "colab-cli",
    }
    if extra:
        h.update(extra)
    return h


def _runtime_headers(proxy_token, extra=None):
    h = {
        "Authorization": f"Bearer {proxy_token}",
        "X-Colab-Runtime-Proxy-Token": proxy_token,
        "X-Colab-Client-Agent": "colab-cli",
        "Accept": "application/json",
    }
    if extra:
        h.update(extra)
    return h


def _uuid_web_safe_b64():
    s = str(uuid.uuid4()).replace("-", "_")
    return s + "." * (44 - len(s))


def _load_json(path):
    try:
        with open(path, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}


def _save_json(path, data):
    try:
        with open(path, "w", encoding="utf-8") as f:
            json.dump(data, f)
    except Exception:
        pass


def _ws_send_frame(sock, text):
    payload = text.encode("utf-8")
    mask = os.urandom(4)
    header = bytearray([0x81])
    length = len(payload)
    if length < 126:
        header.append(0x80 | length)
    elif length < 65536:
        header.append(0x80 | 126)
        header.extend(struct.pack("!H", length))
    else:
        header.append(0x80 | 127)
        header.extend(struct.pack("!Q", length))
    header.extend(mask)
    masked = bytes(b ^ mask[i % 4] for i, b in enumerate(payload))
    sock.sendall(bytes(header) + masked)


def _ws_recv_exact(sock, n):
    buf = bytearray()
    while len(buf) < n:
        chunk = sock.recv(n - len(buf))
        if not chunk:
            raise EOFError("WebSocket closed")
        buf.extend(chunk)
    return bytes(buf)


def _ws_recv_frame(sock):
    b1, b2 = _ws_recv_exact(sock, 2)
    opcode = b1 & 0x0F
    masked = bool(b2 & 0x80)
    length = b2 & 0x7F
    if length == 126:
        length = struct.unpack("!H", _ws_recv_exact(sock, 2))[0]
    elif length == 127:
        length = struct.unpack("!Q", _ws_recv_exact(sock, 8))[0]
    mask = _ws_recv_exact(sock, 4) if masked else b""
    data = _ws_recv_exact(sock, length) if length else b""
    if masked:
        data = bytes(b ^ mask[i % 4] for i, b in enumerate(data))
    return opcode, data


def _jupyter_ws_execute(runtime_url, proxy_token, kernel_id, code, wait_timeout=15):
    """Execute one Jupyter execute_request over WSS and wait for execution ack."""
    parsed = urllib.parse.urlparse(runtime_url)
    host = parsed.hostname
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    base_path = parsed.path.rstrip("/")
    sess_id = uuid.uuid4().hex
    qs = urllib.parse.urlencode({
        "session_id": sess_id,
        "token": proxy_token,
        "colab-runtime-proxy-token": proxy_token,
    })
    ws_path = f"{base_path}/api/kernels/{kernel_id}/channels?{qs}"

    raw_sock = socket.create_connection((host, port), timeout=20)
    if parsed.scheme == "https":
        ctx = ssl.create_default_context()
        sock = ctx.wrap_socket(raw_sock, server_hostname=host)
    else:
        sock = raw_sock

    try:
        key = base64.b64encode(os.urandom(16)).decode("ascii")
        req_lines = [
            f"GET {ws_path} HTTP/1.1",
            f"Host: {host}",
            "Upgrade: websocket",
            "Connection: Upgrade",
            f"Sec-WebSocket-Key: {key}",
            "Sec-WebSocket-Version: 13",
            f"Authorization: Bearer {proxy_token}",
            f"X-Colab-Runtime-Proxy-Token: {proxy_token}",
            "X-Colab-Client-Agent: colab-cli",
            "",
            "",
        ]
        sock.sendall("\r\n".join(req_lines).encode("ascii"))
        buf = b""
        while b"\r\n\r\n" not in buf:
            chunk = sock.recv(4096)
            if not chunk:
                raise RuntimeError("WebSocket handshake closed")
            buf += chunk
        status_line = buf.split(b"\r\n", 1)[0].decode("ascii", errors="replace")
        if "101" not in status_line:
            raise RuntimeError(f"WebSocket handshake failed: {status_line}")

        msg_id = uuid.uuid4().hex
        msg = {
            "header": {
                "msg_id": msg_id,
                "username": "gpuhub",
                "session": sess_id,
                "msg_type": "execute_request",
                "version": "5.3",
            },
            "parent_header": {},
            "metadata": {},
            "content": {
                "code": code,
                "silent": False,
                "store_history": False,
                "user_expressions": {},
                "allow_stdin": False,
                "stop_on_error": True,
            },
            "buffers": [],
            "channel": "shell",
        }
        _ws_send_frame(sock, json.dumps(msg))

        # Wait briefly for execute_reply or iopub idle for our msg_id
        sock.settimeout(2.0)
        deadline = time.time() + wait_timeout
        while time.time() < deadline:
            try:
                opcode, data = _ws_recv_frame(sock)
            except socket.timeout:
                continue
            except EOFError:
                break
            if opcode == 0x8:  # close
                break
            if opcode == 0x9:  # ping -> pong
                continue
            if opcode in (0x1, 0x2):
                try:
                    txt = data.decode("utf-8", errors="ignore")
                    idx = txt.find("{")
                    if idx >= 0:
                        ev = json.loads(txt[idx:])
                        if ev.get("parent_header", {}).get("msg_id") == msg_id:
                            mtype = ev.get("msg_type") or ev.get("header", {}).get("msg_type")
                            if mtype == "execute_reply":
                                if ev.get("content", {}).get("status") == "error":
                                    raise RuntimeError(ev.get("content", {}).get("evalue", "kernel execution error"))
                                break
                            if mtype == "status" and ev.get("content", {}).get("execution_state") == "idle":
                                break
                except RuntimeError:
                    raise
                except Exception:
                    pass
    finally:
        try:
            sock.close()
        except Exception:
            pass


def _colab_unassign(access_token, endpoint):
    url = f"{COLAB_DOMAIN}/tun/m/unassign/{urllib.parse.quote(endpoint)}?authuser=0"
    _, get_res = _http_json("GET", url, headers=_colab_headers(access_token))
    xsrf = get_res.get("token", "")
    if xsrf:
        _http_json("POST", url, headers=_colab_headers(access_token, {"X-Goog-Colab-Token": xsrf}))


def _spawn_colab_idle_reaper(req, endpoint):
    """Detached watchdog: unassigns the Colab VM after COLAB_IDLE_SECONDS of inactivity."""
    code = (
        "import json, os, sys, time\n"
        f"state_file = {COLAB_STATE_FILE!r}\n"
        f"endpoint = {endpoint!r}\n"
        f"idle_sec = {COLAB_IDLE_SECONDS}\n"
        "while True:\n"
        "    time.sleep(30)\n"
        "    try:\n"
        "        with open(state_file, 'r') as f: s = json.load(f)\n"
        "        entry = s.get(endpoint)\n"
        "        if not entry: break\n"
        "        if time.time() - entry.get('last_used', 0) >= idle_sec:\n"
        "            sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))\n"
        "            break\n"
        "    except Exception: break\n"
    )
    # Run via self-invocation with --colab-reap so we reuse _colab_exchange_token & _colab_unassign
    try:
        payload = json.dumps({
            "action": "_colab_reap",
            "id": endpoint,
            "refresh_token": req.get("refresh_token", ""),
            "token": req.get("token", ""),
            "code": req.get("code", ""),
        })
        p = subprocess.Popen(
            [sys.executable, os.path.abspath(__file__)],
            stdin=subprocess.PIPE,
            stdout=subprocess.DEVNULL,
            stderr=subprocess.DEVNULL,
            start_new_session=True,
        )
        p.stdin.write(payload.encode("utf-8"))
        p.stdin.close()
    except Exception:
        pass


def _colab_reap_loop(req):
    endpoint = req.get("id", "")
    while True:
        time.sleep(30)
        sessions = _load_json(COLAB_STATE_FILE)
        entry = sessions.get(endpoint)
        if not entry:
            return
        if time.time() - entry.get("last_used", 0) >= COLAB_IDLE_SECONDS:
            try:
                tok = _colab_exchange_token(req)
                _colab_unassign(tok, endpoint)
            except Exception:
                pass
            sessions = _load_json(COLAB_STATE_FILE)
            sessions.pop(endpoint, None)
            _save_json(COLAB_STATE_FILE, sessions)
            return


def _colab_refresh_proxy(access_token, endpoint):
    sessions = _load_json(COLAB_STATE_FILE)
    sess = sessions.get(endpoint, {})
    try:
        _, res = _http_json("GET", f"{COLAB_DOMAIN}/tun/m/assignments?authuser=0",
                            headers=_colab_headers(access_token))
        for a in res.get("assignments", []):
            if a.get("endpoint") == endpoint:
                rpi = a.get("runtimeProxyInfo", {})
                sess["url"] = rpi.get("url", sess.get("url", ""))
                sess["token"] = rpi.get("token", sess.get("token", ""))
                sess["last_used"] = time.time()
                sessions[endpoint] = sess
                _save_json(COLAB_STATE_FILE, sessions)
                break
    except Exception:
        pass
    return sess


def _colab_find_warm_assignment(access_token, gpu):
    """Reuse an already-active Colab runtime with matching hardware if one is running."""
    try:
        _, res = _http_json("GET", f"{COLAB_DOMAIN}/tun/m/assignments?authuser=0",
                            headers=_colab_headers(access_token))
        want_acc = "NONE" if gpu in ("CPU", "NONE") else gpu
        for a in res.get("assignments", []):
            acc = str(a.get("accelerator", "")).upper()
            rpi = a.get("runtimeProxyInfo", {})
            if not rpi.get("url") or not rpi.get("token"):
                continue
            if want_acc == "ANY" or acc == want_acc or (want_acc == "T4" and acc in ("T4", "L4", "A100", "H100")):
                return a
    except Exception:
        pass
    return None


def _colab_get_or_start_kernel(proxy_url, proxy_token):
    """List existing kernels or start a python3 kernel, retrying while VM finishes booting."""
    last_err = None
    for attempt in range(8):
        try:
            _, kernels = _http_json(
                "GET",
                f"{proxy_url}/api/kernels?authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
                headers=_runtime_headers(proxy_token),
                timeout=15,
            )
            if isinstance(kernels, list) and len(kernels) > 0 and kernels[0].get("id"):
                return kernels[0]["id"]
            _, k_res = _http_json(
                "POST",
                f"{proxy_url}/api/kernels?authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
                headers=_runtime_headers(proxy_token),
                data={"name": "python3"},
                timeout=20,
            )
            if k_res.get("id"):
                return k_res["id"]
        except Exception as e:
            last_err = e
        time.sleep(2)
    raise RuntimeError(f"Colab Jupyter kernel did not become ready: {last_err}")


def api_colab(req, action):
    if action == "_colab_reap":
        _colab_reap_loop(req)
        return {"ok": True}

    access_token = _colab_exchange_token(req)
    job_id = req.get("id", "")

    if action == "validate":
        _, info = _http_json("GET", f"{COLAB_DOMAIN}/tun/m/ccu-info?authuser=0",
                             headers=_colab_headers(access_token))
        return {
            "ok": True,
            "api": True,
            "balance": info.get("currentBalance"),
            "assignments": info.get("assignmentsCount"),
        }

    if action == "quota":
        _, info = _http_json("GET", f"{COLAB_DOMAIN}/tun/m/ccu-info?authuser=0",
                             headers=_colab_headers(access_token))
        bal = float(info.get("currentBalance", 0) or 0)
        rate = float(info.get("consumptionRateHourly", 0) or 0)
        cnt = int(info.get("assignmentsCount", 0) or 0)
        return {
            "raw": f"Compute balance: {bal:.2f} CU | Burn rate: {rate:.2f}/hr | Active runtimes: {cnt}",
        }

    if action == "submit":
        gpu = (req.get("gpu_type") or "T4").upper()
        assign_res = _colab_find_warm_assignment(access_token, gpu)
        if not assign_res:
            params = {"nbh": _uuid_web_safe_b64(), "authuser": "0"}
            if gpu in ("T4", "L4", "A100", "H100", "G4"):
                params["variant"] = "GPU"
                params["accelerator"] = gpu
            elif gpu == "TPU":
                params["variant"] = "TPU"
                params["accelerator"] = "V5E1"
            else:
                params["variant"] = "DEFAULT"

            assign_url = f"{COLAB_DOMAIN}/tun/m/assign?" + urllib.parse.urlencode(params)
            last_err = None
            for attempt in range(4):
                try:
                    _, get_res = _http_json("GET", assign_url, headers=_colab_headers(access_token))
                    if "runtimeProxyInfo" in get_res:
                        assign_res = get_res
                        break
                    xsrf = get_res.get("token", "")
                    _, assign_res = _http_json(
                        "POST", assign_url,
                        headers=_colab_headers(access_token, {"X-Goog-Colab-Token": xsrf}),
                        timeout=60,
                    )
                    if assign_res.get("runtimeProxyInfo"):
                        break
                except Exception as e:
                    last_err = e
                    time.sleep(2 * (attempt + 1))
            if not assign_res or not assign_res.get("runtimeProxyInfo"):
                raise RuntimeError(f"Colab assign failed: {last_err}")

        endpoint = assign_res.get("endpoint")
        rpi = assign_res.get("runtimeProxyInfo", {})
        proxy_url = rpi.get("url", "").rstrip("/")
        proxy_token = rpi.get("token", "")
        if not endpoint or not proxy_url or not proxy_token:
            raise RuntimeError("Colab assign did not return runtime proxy info")

        kernel_id = _colab_get_or_start_kernel(proxy_url, proxy_token)

        jtype = req.get("type", "script")
        payload = req.get("payload", "print('gpuhub colab ok')")
        if jtype == "command":
            script_content = f"import subprocess, sys\nsys.exit(subprocess.call({payload!r}, shell=True))\n"
        else:
            script_content = payload

        run_id = uuid.uuid4().hex[:10]
        b64_script = base64.b64encode(script_content.encode("utf-8")).decode("ascii")
        launcher = (
            "import base64, os, subprocess\n"
            "os.makedirs('/content/hf_cache', exist_ok=True)\n"
            f"with open('/content/gpuhub_{run_id}.py', 'wb') as f:\n"
            f"    f.write(base64.b64decode({b64_script!r}))\n"
            f"with open('/content/gpuhub_{run_id}.sh', 'w') as f:\n"
            f"    f.write('export HF_HOME=/content/hf_cache TORCH_HOME=/content/torch_cache\\n')\n"
            f"    f.write('rm -f /content/gpuhub_{run_id}.exit /content/gpuhub_{run_id}.log\\n')\n"
            f"    f.write('python3 -u /content/gpuhub_{run_id}.py > /content/gpuhub_{run_id}.log 2>&1; echo $? > /content/gpuhub_{run_id}.exit\\n')\n"
            f"subprocess.Popen(['bash', '/content/gpuhub_{run_id}.sh'], start_new_session=True)\n"
            "print('GPUHUB_LAUNCHED')\n"
        )
        _jupyter_ws_execute(proxy_url, proxy_token, kernel_id, launcher)

        sessions = _load_json(COLAB_STATE_FILE)
        is_new_watcher = endpoint not in sessions
        sessions[endpoint] = {
            "url": proxy_url,
            "token": proxy_token,
            "kernel_id": kernel_id,
            "run_id": run_id,
            "last_used": time.time(),
        }
        _save_json(COLAB_STATE_FILE, sessions)
        if is_new_watcher:
            _spawn_colab_idle_reaper(req, endpoint)

        return {"id": f"{endpoint}|{run_id}", "api": True}

    if action in ("status", "logs"):
        endpoint, run_id = (job_id.split("|", 1) + [""])[:2] if "|" in job_id else (job_id, "")
        sess = _colab_refresh_proxy(access_token, endpoint)
        proxy_url = sess.get("url", "").rstrip("/")
        proxy_token = sess.get("token", "")
        if not run_id:
            run_id = sess.get("run_id", "")
        if not proxy_url:
            return {"state": "failed", "message": "Colab runtime session expired"} if action == "status" else {"logs": "[Colab session bulunamadi]"}

        # Touch last_used so active jobs aren't reaped
        sessions = _load_json(COLAB_STATE_FILE)
        if endpoint in sessions:
            sessions[endpoint]["last_used"] = time.time()
            _save_json(COLAB_STATE_FILE, sessions)

        log_name = f"gpuhub_{run_id}.log" if run_id else "gpuhub_job.log"
        exit_name = f"gpuhub_{run_id}.exit" if run_id else "gpuhub_job.exit"

        if action == "status":
            try:
                _, res = _http_json(
                    "GET",
                    f"{proxy_url}/api/contents/{exit_name}?content=1&authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
                    headers=_runtime_headers(proxy_token),
                )
                raw_code = str(res.get("content", "0")).strip()
                rc = int(raw_code) if raw_code.lstrip("-").isdigit() else 0
                return {"state": "succeeded" if rc == 0 else "failed", "message": f"exit code {rc}"}
            except RuntimeError as e:
                if "HTTP 404" in str(e):
                    return {"state": "running", "message": "Colab GPU runtime çalışıyor"}
                return {"state": "running", "message": str(e)[:180]}

        # logs
        try:
            _, res = _http_json(
                "GET",
                f"{proxy_url}/api/contents/{log_name}?content=1&authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
                headers=_runtime_headers(proxy_token),
            )
            return {"logs": str(res.get("content", ""))}
        except RuntimeError as e:
            if "HTTP 404" in str(e):
                return {"logs": "[Colab runtime başlatılıyor / çıktı bekleniyor...]"}
            return {"logs": f"[Colab log okuma uyarısı: {e}]"}

    if action == "cancel":
        endpoint = job_id.split("|", 1)[0]
        _colab_unassign(access_token, endpoint)
        sessions = _load_json(COLAB_STATE_FILE)
        sessions.pop(endpoint, None)
        _save_json(COLAB_STATE_FILE, sessions)
        return {"id": job_id, "state": "cancelled"}

    return None


# ---------------------------------------------------------------------------
# Modal (Warm Sandbox + Persistent HF Volume + 5-min idle auto-shutdown)
# ---------------------------------------------------------------------------

def _modal_gpu_arg(gpu_type):
    if not gpu_type:
        return "T4"
    g = gpu_type.upper()
    if g in ("CPU", "NONE"):
        return None
    if g in ("ANY",):
        return "T4"
    if g == "A10":
        return "A10G"
    return g


def _modal_get_or_create_warm_sandbox(modal, gpu):
    key = gpu or "CPU"
    warm = _load_json(MODAL_WARM_FILE)
    sb_id = warm.get(key)
    if sb_id:
        try:
            sb = modal.Sandbox.from_id(sb_id)
            if sb.poll() is None:
                return sb
        except Exception:
            pass

    app = modal.App.lookup("gpuhub-sandbox", create_if_missing=True)
    vol = modal.Volume.from_name("gpuhub-cache", create_if_missing=True)
    image = (
        modal.Image.debian_slim(python_version="3.11")
        .pip_install(
            "torch",
            "transformers",
            "accelerate",
            "diffusers",
            "huggingface_hub",
            "pillow",
            "imageio",
            "imageio-ffmpeg",
            "sentencepiece",
        )
    )
    sb = modal.Sandbox.create(
        "sleep", "infinity",
        app=app,
        image=image,
        gpu=gpu,
        volumes={"/cache": vol},
        env={"HF_HOME": "/cache/hf", "TORCH_HOME": "/cache/torch"},
        idle_timeout=MODAL_IDLE_SECONDS,
        timeout=3600,
    )
    warm[key] = sb.object_id
    _save_json(MODAL_WARM_FILE, warm)
    return sb


def api_modal(req, action):
    tid, secret = req.get("token_id", ""), req.get("secret", "")
    if not tid or not secret:
        return {"error": "Modal credential must include id (ak-...) and secret (as-...)"}
    os.environ["MODAL_TOKEN_ID"] = tid
    os.environ["MODAL_TOKEN_SECRET"] = secret

    import modal

    job_id = req.get("id", "")

    if action == "validate":
        try:
            modal.App.lookup("gpuhub-sandbox", create_if_missing=True)
            return {"ok": True, "sdk": True}
        except Exception as e:
            return {"error": f"Modal auth failed: {str(e)[:300]}"}

    if action == "quota":
        return {"raw": "$30/month recurring compute credit (Modal Starter); 5-min idle auto-shutdown enabled"}

    if action == "submit":
        gpu = _modal_gpu_arg(req.get("gpu_type", "T4"))
        sb = _modal_get_or_create_warm_sandbox(modal, gpu)
        jtype = req.get("type", "script")
        payload = req.get("payload", "print('gpuhub modal ok')")
        run_id = uuid.uuid4().hex[:10]
        b64 = base64.b64encode(payload.encode("utf-8")).decode("ascii")

        if jtype == "command":
            runner = (
                f"echo {b64} | base64 -d > /tmp/{run_id}.sh; "
                f"(bash /tmp/{run_id}.sh > /tmp/{run_id}.log 2>&1; echo $? > /tmp/{run_id}.exit) </dev/null >/dev/null 2>&1 &"
            )
        else:
            runner = (
                f"echo {b64} | base64 -d > /tmp/{run_id}.py; "
                f"(python3 -u /tmp/{run_id}.py > /tmp/{run_id}.log 2>&1; echo $? > /tmp/{run_id}.exit) </dev/null >/dev/null 2>&1 &"
            )
        p = sb.exec("bash", "-c", runner)
        p.wait()
        return {"id": f"{sb.object_id}|{run_id}", "sdk": True}

    if action == "status":
        if "|" not in job_id:
            sb = modal.Sandbox.from_id(job_id)
            rc = sb.poll()
            if rc is None:
                return {"state": "running", "message": "Modal Sandbox çalışıyor"}
            return {"state": "succeeded" if rc == 0 else "failed", "message": f"exit code {rc}"}

        sb_id, run_id = job_id.split("|", 1)
        sb = modal.Sandbox.from_id(sb_id)
        if sb.poll() is not None:
            return {"state": "failed", "message": "Modal Sandbox sonlandı"}
        p = sb.exec("bash", "-c", f"cat /tmp/{run_id}.exit 2>/dev/null || echo RUNNING")
        p.wait()
        out = (p.stdout.read() or "").strip()
        if out == "RUNNING" or out == "":
            return {"state": "running", "message": "Modal Warm Sandbox çalışıyor"}
        rc = int(out) if out.lstrip("-").isdigit() else 0
        return {"state": "succeeded" if rc == 0 else "failed", "message": f"exit code {rc}"}

    if action == "logs":
        if "|" not in job_id:
            sb = modal.Sandbox.from_id(job_id)
            return {"logs": (sb.stdout.read() or "") + (sb.stderr.read() or "")}

        sb_id, run_id = job_id.split("|", 1)
        sb = modal.Sandbox.from_id(sb_id)
        p = sb.exec("bash", "-c", f"cat /tmp/{run_id}.log 2>/dev/null || echo '[Modal Sandbox hazirlaniyor...]'")
        p.wait()
        return {"logs": p.stdout.read() or ""}

    if action == "cancel":
        sb_id = job_id.split("|", 1)[0]
        sb = modal.Sandbox.from_id(sb_id)
        sb.terminate()
        return {"id": job_id, "state": "cancelled"}

    return None


def main():
    req = json.load(sys.stdin)
    action = req.get("action", "validate")
    provider = req.get("provider")

    try:
        if provider == "colab":
            res = api_colab(req, action)
        elif provider == "modal":
            res = api_modal(req, action)
        else:
            fail(f"unsupported bridge provider: {provider}")
    except Exception as e:
        fail(f"{provider}/{action} failed: {e}")

    if not res:
        fail(f"no usable path for {provider}/{action}")
    if isinstance(res, dict) and res.get("error"):
        fail(res["error"])
    print(json.dumps(res))


if __name__ == "__main__":
    main()
