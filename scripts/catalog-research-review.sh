#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
review_host="${PHASE8_RESEARCH_REVIEW_HOST:-127.0.0.1}"
review_port="${PHASE8_RESEARCH_REVIEW_PORT:-4178}"

if [[ ! -d "${repository_root}/web/node_modules" ]]; then
  echo "web 의존성이 없습니다. 먼저 'cd web && npm ci'를 실행해 주세요." >&2
  exit 1
fi

if curl --max-time 1 -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1 ||
  curl --max-time 1 -fsS "http://${review_host}:${review_port}/" >/dev/null 2>&1; then
  echo "Phase 8 review server is already running on :8080 or ${review_host}:${review_port}." >&2
  echo "Stop the previous review process before starting a clean review database." >&2
  exit 1
fi

if [[ "${PHASE8_RESEARCH_SKIP_BOOTSTRAP:-false}" != "true" ]]; then
  "${repository_root}/scripts/catalog-research-bootstrap.sh"
fi

api_log="${PHASE8_RESEARCH_API_LOG:-/tmp/vitlane-phase8-live-api.log}"
"${repository_root}/scripts/catalog-research-live-api.sh" >"${api_log}" 2>&1 &
api_pid=$!
cleanup() {
  if kill -0 "${api_pid}" 2>/dev/null; then
    kill "${api_pid}" 2>/dev/null || true
    wait "${api_pid}" 2>/dev/null || true
  fi
}
trap cleanup EXIT INT TERM

for _ in {1..90}; do
  if curl -fsS http://127.0.0.1:8080/readyz >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${api_pid}" 2>/dev/null; then
    sed -n '1,160p' "${api_log}" >&2
    exit 1
  fi
  sleep 1
done
curl -fsS http://127.0.0.1:8080/readyz >/dev/null

echo "Phase 8-1 Research가 통합된 실제 Vitlane UI를 시작합니다."
echo "로그인: http://${review_host}:${review_port}/login?returnTo=%2Fcurations%2Fe5100000-0000-4000-8000-000000000002"
echo "Curation: http://${review_host}:${review_port}/curations/e5100000-0000-4000-8000-000000000002"
echo "Shopify read는 로컬 Vitlane :8080을 통해서만 수행합니다."
echo "종료: Ctrl+C"
echo "API log: ${api_log}"

cd "${repository_root}/web"
npm run dev -- --host "${review_host}" --port "${review_port}" --strictPort
