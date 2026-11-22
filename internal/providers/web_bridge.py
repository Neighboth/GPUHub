#!/usr/bin/env python3
"""
GPUHub provider bridge for Modal (Sandbox SDK) and Google Colab (OAuth2 + /tun/m/*).

Reads one JSON request on stdin, writes one JSON response on stdout.
"""

import base64
import hashlib
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
COLAB_REDIRECT_URI = "https://sdk.cloud.google.com/applicationdefaultauthcode.html"
COLAB_DOMAIN = "https://colab.research.google.com"
COLAB_STATE_FILE = os.path.join(tempfile.gettempdir(), "gpuhub_colab_sessions.json")


def fail(msg):
    print(json.dumps({"error": msg}))
    sys.exit(1)


# ---------------------------------------------------------------------------
# Google Colab (OAuth2 + /tun/m/* + Jupyter Contents/Kernel WebSocket)
# ---------------------------------------------------------------------------

def _http_json(method, url, headers=None, data=None, timeout=30):
    hdrs = dict(headers or {})
    body = None
    if data is not None:
        if isinstance(data, (dict, list)):
            body = json.dumps(data).encode("utf-8")
            hdrs.setdefault("Content-Type", "application/json")
        elif isinstance(data, str):
            body = data.encode("utf-8")
        else:
            body = data
    req = urllib.request.Request(url, data=body, headers=hdrs, method=method)
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            raw = resp.read().decode("utf-8", errors="replace")
            if raw.startswith(")]}'\n"):
                raw = raw[5:]
            return resp.status, (json.loads(raw) if raw.strip() else {})
    except urllib.error.HTTPError as e:
        raw = e.read().decode("utf-8", errors="replace")
        raise RuntimeError(f"HTTP {e.code}: {raw[:300]}")


def _colab_exchange_token(req):
    """Return a fresh Google OAuth2 access_token from refresh_token, code, or access_token."""
    tok = (req.get("refresh_token") or req.get("token") or req.get("password") or "").strip()
    code = (req.get("code") or "").strip()
    if tok.startswith("ya29."):
        return tok
    if tok.startswith("4/") and not code:
        code = tok

    if code:
        form = urllib.parse.urlencode({
            "code": code,
            "client_id": COLAB_CLIENT_ID,
            "client_secret": COLAB_CLIENT_SECRET,
            "redirect_uri": COLAB_REDIRECT_URI,
            "grant_type": "authorization_code",
        }).encode("utf-8")
        _, res = _http_json("POST", "https://oauth2.googleapis.com/token",
                            headers={"Content-Type": "application/x-www-form-urlencoded"}, data=form)
        if not res.get("access_token"):
            raise RuntimeError("OAuth code exchange failed: " + json.dumps(res)[:200])
        return res["access_token"]

    if not tok:
        raise RuntimeError("Google Colab credential must be an OAuth refresh_token (1//...) or auth code (4/...)")

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


def _uuid_web_safe_b64():
    s = str(uuid.uuid4()).replace("-", "_")
    return s + "." * (44 - len(s))


def _load_colab_sessions():
    try:
        with open(COLAB_STATE_FILE, "r", encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return {}


def _save_colab_sessions(data):
    try:
        with open(COLAB_STATE_FILE, "w", encoding="utf-8") as f:
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


def _jupyter_ws_execute(runtime_url, proxy_token, kernel_id, code):
    """Execute one Jupyter execute_request over WSS without external packages."""
    parsed = urllib.parse.urlparse(runtime_url)
    host = parsed.hostname
    port = parsed.port or (443 if parsed.scheme == "https" else 80)
    base_path = parsed.path.rstrip("/")
    ws_path = f"{base_path}/api/kernels/{kernel_id}/channels?colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}"

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
                "session": uuid.uuid4().hex,
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
        time.sleep(0.5)
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


def _colab_refresh_proxy(access_token, endpoint):
    sessions = _load_colab_sessions()
    sess = sessions.get(endpoint, {})
    try:
        _, res = _http_json("GET", f"{COLAB_DOMAIN}/tun/m/assignments?authuser=0",
                            headers=_colab_headers(access_token))
        for a in res.get("assignments", []):
            if a.get("endpoint") == endpoint:
                rpi = a.get("runtimeProxyInfo", {})
                sess["url"] = rpi.get("url", sess.get("url", ""))
                sess["token"] = rpi.get("token", sess.get("token", ""))
                sessions[endpoint] = sess
                _save_colab_sessions(sessions)
                break
    except Exception:
        pass
    return sess


def api_colab(req, action):
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
        _, get_res = _http_json("GET", assign_url, headers=_colab_headers(access_token))
        if "runtimeProxyInfo" in get_res:
            assign_res = get_res
        else:
            xsrf = get_res.get("token", "")
            _, assign_res = _http_json("POST", assign_url,
                                       headers=_colab_headers(access_token, {"X-Goog-Colab-Token": xsrf}),
                                       timeout=60)

        endpoint = assign_res.get("endpoint")
        rpi = assign_res.get("runtimeProxyInfo", {})
        proxy_url = rpi.get("url", "").rstrip("/")
        proxy_token = rpi.get("token", "")
        if not endpoint or not proxy_url or not proxy_token:
            raise RuntimeError("Colab assign did not return runtime proxy info")

        jtype = req.get("type", "script")
        payload = req.get("payload", "print('gpuhub colab ok')")
        if jtype == "command":
            script_content = f"import subprocess, sys\nsys.exit(subprocess.call({payload!r}, shell=True))\n"
        else:
            script_content = payload

        # Upload script via Jupyter Contents API
        b64_script = base64.b64encode(script_content.encode("utf-8")).decode("ascii")
        _http_json(
            "PUT",
            f"{proxy_url}/api/contents/gpuhub_job.py?authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
            headers={"X-Colab-Runtime-Proxy-Token": proxy_token, "X-Colab-Client-Agent": "colab-cli"},
            data={"name": "gpuhub_job.py", "path": "gpuhub_job.py", "type": "file", "format": "base64", "content": b64_script},
        )

        # Start a Python kernel on the runtime
        _, k_res = _http_json(
            "POST",
            f"{proxy_url}/api/kernels?authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
            headers={"X-Colab-Runtime-Proxy-Token": proxy_token, "X-Colab-Client-Agent": "colab-cli"},
            data={"name": "python3"},
        )
        kernel_id = k_res.get("id")
        if not kernel_id:
            raise RuntimeError("Failed to start Colab Jupyter kernel")

        launcher = (
            "import subprocess, os\n"
            "os.makedirs('/content', exist_ok=True)\n"
            "with open('/content/gpuhub_run.sh', 'w') as f:\n"
            "    f.write('rm -f /content/gpuhub_job.exit; python3 -u /content/gpuhub_job.py > /content/gpuhub_job.log 2>&1; echo $? > /content/gpuhub_job.exit\\n')\n"
            "subprocess.Popen(['bash', '/content/gpuhub_run.sh'], start_new_session=True)\n"
        )
        _jupyter_ws_execute(proxy_url, proxy_token, kernel_id, launcher)

        sessions = _load_colab_sessions()
        sessions[endpoint] = {"url": proxy_url, "token": proxy_token, "kernel_id": kernel_id}
        _save_colab_sessions(sessions)
        return {"id": endpoint, "api": True}

    if action == "status":
        sess = _colab_refresh_proxy(access_token, job_id)
        proxy_url = sess.get("url", "").rstrip("/")
        proxy_token = sess.get("token", "")
        if not proxy_url:
            return {"state": "failed", "message": "Colab runtime session not found / expired"}
        try:
            _, res = _http_json(
                "GET",
                f"{proxy_url}/api/contents/gpuhub_job.exit?content=1&authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
                headers={"X-Colab-Runtime-Proxy-Token": proxy_token, "X-Colab-Client-Agent": "colab-cli"},
            )
            raw_code = str(res.get("content", "0")).strip()
            rc = int(raw_code) if raw_code.lstrip("-").isdigit() else 0
            try:
                _colab_unassign(access_token, job_id)
            except Exception:
                pass
            return {"state": "succeeded" if rc == 0 else "failed", "message": f"exit code {rc}"}
        except RuntimeError as e:
            if "HTTP 404" in str(e):
                return {"state": "running", "message": "Colab GPU runtime çalışıyor"}
            return {"state": "running", "message": str(e)[:180]}

    if action == "logs":
        sess = _colab_refresh_proxy(access_token, job_id)
        proxy_url = sess.get("url", "").rstrip("/")
        proxy_token = sess.get("token", "")
        if not proxy_url:
            return {"logs": "[Colab session bulunamadi]"}
        try:
            _, res = _http_json(
                "GET",
                f"{proxy_url}/api/contents/gpuhub_job.log?content=1&authuser=0&colab-runtime-proxy-token={urllib.parse.quote(proxy_token)}",
                headers={"X-Colab-Runtime-Proxy-Token": proxy_token, "X-Colab-Client-Agent": "colab-cli"},
            )
            return {"logs": str(res.get("content", ""))}
        except RuntimeError as e:
            if "HTTP 404" in str(e):
                return {"logs": "[Colab runtime başlatılıyor / çıktı bekleniyor...]"}
            return {"logs": f"[Colab log okuma uyarısı: {e}]"}

    if action == "cancel":
        _colab_unassign(access_token, job_id)
        sessions = _load_colab_sessions()
        sessions.pop(job_id, None)
        _save_colab_sessions(sessions)
        return {"id": job_id, "state": "cancelled"}

    return None


# ---------------------------------------------------------------------------
# Modal (Direct modal.Sandbox gRPC SDK - no secret or web deploy required)
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


def _read_stream_nonblocking(stream, timeout_sec=1.5):
    """Read available data from a Modal sandbox stream without hanging forever."""
    chunks = []

    def reader():
        try:
            for chunk in stream:
                chunks.append(str(chunk))
        except Exception:
            pass

    t = threading.Thread(target=reader, daemon=True)
    t.start()
    t.join(timeout=timeout_sec)
    return "".join(chunks)


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
        return {"raw": "$30/month recurring compute credit (Modal Starter); no REST balance endpoint"}

    if action == "submit":
        app = modal.App.lookup("gpuhub-sandbox", create_if_missing=True)
        image = modal.Image.debian_slim(python_version="3.11")
        gpu = _modal_gpu_arg(req.get("gpu_type", "T4"))
        jtype = req.get("type", "script")
        payload = req.get("payload", "print('gpuhub modal ok')")
        timeout = int(req.get("max_duration_seconds") or 1800)
        timeout = max(60, min(timeout, 86400))

        if jtype == "command":
            args = ("bash", "-c", payload)
        else:
            args = ("python3", "-u", "-c", payload)

        sb = modal.Sandbox.create(
            *args,
            app=app,
            image=image,
            gpu=gpu,
            timeout=timeout,
        )
        return {"id": sb.object_id, "sdk": True}

    if action == "status":
        sb = modal.Sandbox.from_id(job_id)
        rc = sb.poll()
        if rc is None:
            return {"state": "running", "message": "Modal Sandbox GPU container çalışıyor"}
        if rc == 0:
            return {"state": "succeeded", "message": "exit code 0"}
        return {"state": "failed", "message": f"exit code {rc}"}

    if action == "logs":
        sb = modal.Sandbox.from_id(job_id)
        rc = sb.poll()
        if rc is not None:
            out = sb.stdout.read() or ""
            err = sb.stderr.read() or ""
        else:
            out = _read_stream_nonblocking(sb.stdout, 1.5)
            err = _read_stream_nonblocking(sb.stderr, 0.5)
        combined = out + ("\n" + err if err else "")
        if not combined.strip() and rc is None:
            combined = "[Modal Sandbox başlatılıyor / çıktı bekleniyor...]"
        return {"logs": combined}

    if action == "cancel":
        sb = modal.Sandbox.from_id(job_id)
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
