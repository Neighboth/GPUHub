#!/usr/bin/env bash
# GPUHub smoke test - run after `docker compose up -d` (or against any reachable instance).
# Exercises: login -> create proxy -> add provider key bound to it -> submit a GPU-typed job -> read detail.
# Usage: GPUHUB_URL=http://localhost:8080 ADMIN_PASSWORD=... ./scripts/smoke.sh
# NOTE on expectations: the placeholder proxy (127.0.0.1:3128) is unreachable, so GPUHub's
# health loop will mark it unhealthy within 60s and DEACTIVATE the bound key; the job then
# ends 'unschedulable' with per-provider rejection reasons. That is the CORRECT sticky-binding
# behavior. Point GPUHUB_URL at an instance behind a real proxy to see keys survive.
set -euo pipefail

BASE="${GPUHUB_URL:-}"
if [ -z "$BASE" ]; then
  for cand in http://localhost:8080 http://127.0.0.1:8080 http://host.docker.internal:8080; do
    if curl -fsS -m 2 "$cand/api/v1/auth/status" >/dev/null 2>&1; then BASE="$cand"; break; fi
  done
fi
if [ -z "$BASE" ]; then
  echo "error: no reachable GPUHub on :8080 (set GPUHUB_URL=http://host:port)" >&2
  exit 1
fi
BASE="${BASE%/}"
ADMIN_USER="${ADMIN_USERNAME:-admin}"
ADMIN_PASS="${ADMIN_PASSWORD:-${GPUHUB_ADMIN_PASSWORD:-admin}}"
JSON_HDR=(-H 'Content-Type: application/json' -H 'X-Requested-With: XMLHttpRequest')

json_field() { sed -n "s/.*\"$1\"[[:space:]]*:[[:space:]]*\"\\{0,1\\}\\([^\",}]*\\)\"\\{0,1\\}.*/\\1/p" | head -n1; }

echo "== 1. login =="
LOGIN=$(curl -fsS -m 15 "${JSON_HDR[@]}" -X POST "$BASE/api/v1/auth/login" \
  -d "{\"username\":\"$ADMIN_USER\",\"password\":\"$ADMIN_PASS\"}")
TOKEN=$(echo "$LOGIN" | json_field access_token)
if [ -z "$TOKEN" ]; then echo "login failed: $LOGIN" >&2; exit 1; fi
echo "token acquired (${#TOKEN} chars)"
AUTH=(-H "Authorization: Bearer $TOKEN" -H 'X-Requested-With: XMLHttpRequest' -H 'Content-Type: application/json')

echo "== 2. create proxy =="
PROXY_RESP=$(curl -fsS -m 10 "${AUTH[@]}" -X POST "$BASE/api/v1/proxies" \
  -d '{"label":"smoke-proxy","url":"http://127.0.0.1:3128","country":"US"}')
echo "$PROXY_RESP"
PROXY_ID=$(echo "$PROXY_RESP" | json_field id)

echo "== 3. add kaggle key bound to proxy (sticky 1:1 binding) =="
KEY_RESP=$(curl -fsS -m 10 "${AUTH[@]}" -X POST "$BASE/api/v1/providers/kaggle/keys" \
  -d "{\"label\":\"smoke-kaggle\",\"credential\":\"{\\\"username\\\":\\\"smoke\\\",\\\"key\\\":\\\"fake-key-for-smoke\\\"}\",\"bound_proxy_id\":$PROXY_ID,\"daily_quota\":10,\"priority\":50}")
echo "$KEY_RESP"

echo "== 4. submit GPU job (T4, 16GB, x1) =="
JOB_RESP=$(curl -fsS -m 10 "${AUTH[@]}" -X POST "$BASE/api/v1/jobs" \
  -d '{"type":"script","payload":"print(\"hello from gpuhub smoke\")","gpu_type":"T4","min_vram_gb":16,"gpu_count":1,"max_duration_seconds":600}')
echo "$JOB_RESP"
JOB_ID=$(echo "$JOB_RESP" | json_field id)

echo "== 5. job detail =="
curl -fsS -m 10 "${AUTH[@]}" "$BASE/api/v1/jobs/$JOB_ID"
echo

echo "== 6. logs (WebSocket) =="
echo "logs endpoint: GET /api/v1/jobs/$JOB_ID/logs (WebSocket), e.g. wscat -c \"ws://<host>/api/v1/jobs/$JOB_ID/logs?token=<access_token>\""

echo "== 7. dashboard summary =="
curl -fsS -m 10 "${AUTH[@]}" "$BASE/api/v1/dashboard/summary"
echo

echo "SMOKE OK"
