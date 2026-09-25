#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
postgres_container="${VITLANE_LOCAL_REVIEW_POSTGRES_CONTAINER:-vitlane-local-review-postgres}"
postgres_port="${VITLANE_LOCAL_REVIEW_POSTGRES_PORT:-15433}"
server_port="${PHASE5_E2E_SERVER_PORT:-18080}"
foundry_bin_dir="${FOUNDRY_BIN_DIR:-}"

if [[ -z "${foundry_bin_dir}" ]]; then
  for candidate in \
    "${HOME}/.foundry/bin" \
    "/tmp/vitlane-foundry-v1.7.1-bin"
  do
    if [[ -x "${candidate}/forge" && -x "${candidate}/cast" && -x "${candidate}/anvil" ]]; then
      foundry_bin_dir="${candidate}"
      break
    fi
  done
fi

if [[ -z "${foundry_bin_dir}" ]]; then
  echo "Foundry 1.7.1을 찾지 못했습니다." >&2
  echo "FOUNDRY_BIN_DIR에 forge, cast, anvil이 있는 디렉터리를 지정해 주세요." >&2
  exit 1
fi

export PATH="${foundry_bin_dir}:${PATH}"

foundry_version="$(forge --version)"
if [[ "${foundry_version}" != *"forge Version: 1.7.1"* ]]; then
  echo "Vitlane 로컬 검수는 contracts/.foundry-version과 같은 Foundry 1.7.1이 필요합니다." >&2
  exit 1
fi

if docker inspect "${postgres_container}" >/dev/null 2>&1; then
  docker start "${postgres_container}" >/dev/null
else
  docker run --detach \
    --name "${postgres_container}" \
    --publish "127.0.0.1:${postgres_port}:5432" \
    --env POSTGRES_DB=vitlane \
    --env POSTGRES_USER=vitlane \
    --env POSTGRES_PASSWORD=vitlane \
    postgres:16-alpine >/dev/null
fi

for _ in {1..30}; do
  if docker exec "${postgres_container}" pg_isready -U vitlane -d vitlane >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

if ! docker exec "${postgres_container}" pg_isready -U vitlane -d vitlane >/dev/null; then
  echo "로컬 검수 PostgreSQL이 준비되지 않았습니다." >&2
  exit 1
fi

export TEST_DATABASE_URL="postgres://vitlane:vitlane@127.0.0.1:${postgres_port}/vitlane?sslmode=disable"
export PHASE5_E2E_KEEP_RUNNING=true
# PUBLIC_BASE_URL(외부 결제 복귀 redirect의 기준)은 실제로 응답하는 서버 포트를
# 가리켜야 한다 — 기본 +1000 프록시 포트는 로컬 검수에서 아무도 서빙하지 않는다.
export PHASE5_E2E_BROWSER_PROXY_PORT="${PHASE5_E2E_BROWSER_PROXY_PORT:-${server_port}}"
# 브라우저 suite 컨테이너가 접속할 호스트는 settlement-e2e.sh가 /readyz 도달성
# 실측으로 고른다(WSL 네이티브 Docker=127.0.0.1, Docker Desktop=
# host.docker.internal). 강제가 필요할 때만 이 변수로 지정한다.
if [[ -n "${VITLANE_LOCAL_REVIEW_CONTAINER_HOST:-}" ]]; then
  export PHASE5_E2E_CONTAINER_HOST="${VITLANE_LOCAL_REVIEW_CONTAINER_HOST}"
fi
export PHASE5_E2E_WINDOWS_PROXY="${VITLANE_LOCAL_REVIEW_WINDOWS_PROXY:-auto}"
export PHASE5_E2E_REUSE_BROWSER_IMAGE="${VITLANE_LOCAL_REVIEW_REUSE_BROWSER_IMAGE:-true}"

# PayPal Sandbox 풀 플로우: 리포 .env에 자격이 있으면 rail을 켠다. 값은 이
# 셸의 환경으로만 전달되고 출력하지 않는다(secrets 로그 금지).
env_file="${repository_root}/.env"
if [[ -f "${env_file}" ]]; then
  for key in PAYPAL_SANDBOX_CLIENT_ID PAYPAL_SANDBOX_CLIENT_SECRET \
             PAYPAL_SANDBOX_WEBHOOK_ID PAYPAL_SANDBOX_MERCHANT_ID \
             MANAGED_OPENAI_API_SECRET SHOPIFY_DEV_CLIENT_ID SHOPIFY_DEV_CLIENT_SECRET \
             OPEN_WEB_NINJA_API_KEY NAVER_API_HUB_CLIENT_ID NAVER_API_HUB_CLIENT_SECRET SERP_API_KEY; do
    if [[ -z "${!key:-}" ]]; then
      # 키 부재는 실패가 아니라 rail 비활성이다 — pipefail에서 grep 미일치가
      # 스크립트를 죽이지 않도록 방어한다.
      value="$(grep -E "^${key}=" "${env_file}" | head -1 | cut -d= -f2- || true)"
      if [[ -n "${value}" ]]; then
        export "${key}=${value}"
      fi
    fi
  done
fi
if [[ -n "${PAYPAL_SANDBOX_CLIENT_ID:-}" && -n "${PAYPAL_SANDBOX_MERCHANT_ID:-}" ]]; then
  echo "PayPal Sandbox rail: 활성 (tVITUSD + PayPal 전체 흐름 검수 가능)"
else
  echo "PayPal Sandbox rail: 비활성 (.env에 PAYPAL_SANDBOX_* 4종이 없으면 GIWA 전용)"
fi

# 한국 상품 조사: 기본 프로필의 조사 국가가 KR이라 한국 카탈로그가 꺼져 있으면 모든
# 조사가 KOREAN_CATALOG_UNAVAILABLE로 끝난다. 로컬 검수는 서버의 리뷰 stub(외부 호출
# 없음, 만년필·이어폰 예시 응답)을 기본으로 켠다. 끄려면
# VITLANE_LOCAL_REVIEW_KOREAN_CATALOG=false.
export KOREAN_PRODUCT_SEARCH_ENABLED="${VITLANE_LOCAL_REVIEW_KOREAN_CATALOG:-true}"
if [[ "${KOREAN_PRODUCT_SEARCH_ENABLED}" == "true" ]]; then
  # 조사 설정에 'STUB 검토' 안내(예시 검색어)를 띄운다. Web build에만 영향을 준다.
  export VITE_RESEARCH_REVIEW_STUB="${VITE_RESEARCH_REVIEW_STUB:-true}"
  echo "한국 상품 조사: stub 활성 (요청에 '만년필'·'이어폰'이 들어가면 예시 후보가 나온다)"
else
  echo "한국 상품 조사: 비활성 (KR 조사는 KOREAN_CATALOG_UNAVAILABLE로 끝난다)"
fi

echo "Vitlane 로컬 검수 환경을 빌드하고 Firefox E2E로 초기화합니다."
echo "완료 뒤 앱은 http://127.0.0.1:${server_port}, 마케팅은 http://marketing.localhost:${server_port} 입니다."
echo "종료하려면 이 터미널에서 Ctrl+C를 누르세요. 전용 PostgreSQL 데이터는 보존됩니다."

exec bash "${repository_root}/scripts/ci/settlement-e2e.sh"
