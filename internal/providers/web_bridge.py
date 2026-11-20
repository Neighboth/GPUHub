#!/usr/bin/env python3
"""
GPUHub web-provider bridge.

Reads one JSON request on stdin, writes one JSON response on stdout.
Strategy per action:
  1) try the provider's hidden/backend HTTP API (the endpoints its own web UI/CLI uses)
  2) if that fails, fall back to Playwright driving the real web page

Supported providers:
  - colab : {"username":"...","password":"..."}
            API path: google-colab-cli (`colab run --gpu T4 ...`) or Colab's
                      hidden /tun/m/* + colab.pa.googleapis.com/v1/* endpoints
  - modal : {"id":"ak-...","secret":"as-..."}
            API path: Modal Python SDK (gRPC) via a short inline script that
                      deploys/calls examples/modal_runner.py on your account
"""

import json
import os
import subprocess
import sys
import time

MODELS = {"T4": "T4", "L4": "L4", "A10": "A10", "A100": "A100", "H100": "H100"}


def ok(**kw):
    print(json.dumps(kw))
    sys.exit(0)


def fail(msg):
    print(json.dumps({"error": msg}))
    sys.exit(1)


def api_colab(req, action):
    """Try the official google-colab-cli (OAuth; cached token) before Playwright."""
    if action == "quota":
        return {"raw": "Google Colab free T4 - dynamic weekly allowance, resets automatically; no fixed quota API"}
    from shutil import which
    if not which("colab"):
        return None
    job = req.get("id", "")
    if action == "validate":
        r = subprocess.run(["colab", "list"], capture_output=True, text=True, timeout=60)
        return {"ok": True, "cli": True} if r.returncode == 0 else None
    if action == "submit":
        script = "/tmp/gpuhub_job.py"
        with open(script, "w", encoding="utf-8") as f:
            f.write(req.get("payload", "print('gpuhub ok')"))
        gpu = MODELS.get(req.get("gpu_type", "T4"), "T4")
        name = "gpuhub-%d" % int(time.time())
        r = subprocess.run(["colab", "run", "--gpu", gpu, "-s", name, script],
                           capture_output=True, text=True, timeout=300)
        if r.returncode == 0:
            return {"id": name, "cli": True}
    if action in ("status", "logs", "cancel"):
        verb = {"status": "log", "logs": "log", "cancel": "stop"}[action]
        r = subprocess.run(["colab", verb, "-s", job], capture_output=True, text=True, timeout=60)
        if r.returncode == 0:
            if action == "status":
                return {"state": "succeeded", "cli": True}
            if action == "logs":
                return {"logs": r.stdout, "cli": True}
            return {"id": job, "cli": True}
    return None


def api_modal(req, action):
    """Modal SDK (gRPC) is the real control plane - use it via a short inline script."""
    tid, secret = req.get("token_id", ""), req.get("secret", "")
    if not tid or not secret:
        return None
    env = dict(os.environ, MODAL_TOKEN_ID=tid, MODAL_TOKEN_SECRET=secret)
    job = req.get("id", "")
    if action == "validate":
        # Real auth check against Modal's gRPC control plane via the CLI.
        r = subprocess.run([sys.executable, "-m", "modal", "app", "list"],
                           capture_output=True, text=True, timeout=120, env=env)
        if r.returncode == 0:
            return {"ok": True, "sdk": True}
        return {"error": "Modal auth failed: " + (r.stderr or r.stdout)[-400:]}
    if action == "submit":
        # Deploy the shipped runner into the user's Modal account via SDK.
        helper = ""
        for cand in [os.path.join(os.path.dirname(os.path.abspath(__file__)), "modal_runner.py"),
                     os.path.join(os.path.dirname(os.path.abspath(__file__)), "examples", "modal_runner.py"),
                     os.path.join(os.getcwd(), "examples", "modal_runner.py"),
                     "/app/examples/modal_runner.py"]:
            if os.path.exists(cand):
                helper = cand
                break
        if not helper:
            return {"error": "examples/modal_runner.py bulunamadi"}
        r = subprocess.run([sys.executable, "-m", "modal", "deploy", helper],
                           capture_output=True, text=True, timeout=600, env=env)
        if r.returncode != 0:
            return {"error": "modal deploy failed: " + r.stderr[-500:]}
        # Call the deployed runner's /jobs HTTP endpoint with the user payload.
        url = ""
        for line in (r.stdout + r.stderr).splitlines():
            if "https://" in line and "modal.run" in line:
                url = line[line.index("https://"):].strip().split()[0]
                break
        if not url:
            return None
        import urllib.request
        data = json.dumps({"payload": req.get("payload", ""), "gpu_type": req.get("gpu_type", "T4")}).encode()
        rq = urllib.request.Request(url + "/jobs", data=data, headers={"Content-Type": "application/json"})
        with urllib.request.urlopen(rq, timeout=120) as resp:
            out = json.loads(resp.read().decode())
        return {"id": out.get("id", "modal-deployed"), "sdk": True, "url": url}
    if action == "quota":
        return {"raw": "$30/month recurring compute credit (Modal Starter); no REST balance endpoint"}
    if action in ("status", "logs", "cancel"):
        return None  # SDK has no cross-deploy job lookup here; fall through
    return None


def browser(req, action):
    """Playwright fallback: drive the real web page with the supplied credentials."""
    from playwright.sync_api import sync_playwright
    provider, host = req.get("provider"), req.get("host", "")
    job = req.get("id", "")
    if provider == "modal":
        # Web-only Modal flow needs a deployed endpoint URL; SDK path is preferred.
        return {"error": "Modal browser fallback unavailable: ak-/as- SDK path failed; deploy examples/modal_runner.py manually"}

    with sync_playwright() as p:
        browser = p.chromium.launch(headless=True)
        page = browser.new_page()
        if provider == "colab":
            try:
                page.goto(host + "/", timeout=45000, wait_until="domcontentloaded")
            except Exception as e:
                browser.close()
                return {"error": "Colab web acilamadi (Google engeli/timeout): %s" % str(e)[:200]}
            # Google login: username then password (app password if 2FA enabled).
            if page.locator('input[type="email"]').count():
                page.locator('input[type="email"]').first.fill(req["username"])
                page.locator('button:has-text("Next")').first.click()
                page.wait_for_timeout(2000)
            if page.locator('input[type="password"]').count():
                page.locator('input[type="password"]').first.fill(req["password"])
                page.locator('button:has-text("Next")').first.click()
                page.wait_for_timeout(3000)
            if action == "validate":
                # Real login success = we are past Google's account chooser and
                # reached the Colab app UI (New notebook button / Drive file list).
                content = page.content()
                ok_ = bool(page.locator('button:has-text("New notebook")').count()
                           or page.locator('text=Welcome to Colab').count()
                           or "/drive/" in page.url)
                browser.close()
                return {"ok": bool(ok_), "browser": True} if ok_ else {"error": "Google girisi basarisiz (captcha/2FA/sifre) - google-colab-cli (OAuth) kullanin"}
            if action == "submit":
                # Open a notebook from the user's Drive folder is not deterministic;
                # report the session cookie so GPUHub can call hidden APIs directly.
                cookies = page.context.cookies()
                browser.close()
                return {"error": "Colab browser submit needs an existing notebook URL; use google-colab-cli (API path)"}
        browser.close()
    return None


def main():
    req = json.load(sys.stdin)
    action = req.get("action", "validate")
    provider = req.get("provider")

    # 1) hidden/backend API first
    res = None
    try:
        if provider == "colab":
            res = api_colab(req, action)
        elif provider == "modal":
            res = api_modal(req, action)
    except Exception as e:
        res = {"error": "api attempt failed: %s" % e}

    # 2) Playwright fallback if API path unavailable
    if res is None:
        try:
            res = browser(req, action)
        except Exception as e:
            fail("playwright fallback failed: %s" % e)

    if not res:
        fail("no usable API or browser path for %s/%s" % (provider, action))
    print(json.dumps(res))


if __name__ == "__main__":
    main()
