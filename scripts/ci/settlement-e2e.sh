#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "$0")/../.." && pwd)"
settlement_tmp="$(mktemp -d)"
rpc_port="${PHASE5_E2E_RPC_PORT:-8545}"
server_port="${PHASE5_E2E_SERVER_PORT:-18080}"
container_host="${PHASE5_E2E_CONTAINER_HOST:-127.0.0.1}"
browser_proxy_port="${PHASE5_E2E_BROWSER_PROXY_PORT:-$((server_port + 1000))}"
screenshot_dir="${PHASE5_E2E_SCREENSHOT_DIR:-/tmp/vitlane-phase5-e2e}"
keep_running="${PHASE5_E2E_KEEP_RUNNING:-false}"
skip_browser_suites="${PHASE5_E2E_SKIP_BROWSER_SUITES:-false}"
windows_proxy_mode="${PHASE5_E2E_WINDOWS_PROXY:-false}"
reuse_browser_image="${PHASE5_E2E_REUSE_BROWSER_IMAGE:-false}"
local_review_user_id="e5000000-0000-4000-8000-000000000001"
rpc_url="http://127.0.0.1:${rpc_port}"
base_url="http://127.0.0.1:${server_port}"
container_rpc_url="http://${container_host}:${rpc_port}"
container_base_url="http://${container_host}:${server_port}"
# 외부 결제 복귀(redirect)의 기준 호스트다. PayPal은 이 절대 URL로 브라우저를
# 되돌리고 복귀 SPA 라우트는 세션 쿠키가 필요하므로, 검수 브라우저가 실제로
# 여는 호스트와 반드시 일치해야 한다. WSL에서 interop(curl.exe)까지 죽어 있으면
# Windows 브라우저는 127.0.0.1(localhost 전달)에도 Windows proxy에도 닿을 수
# 없으므로, Windows·WSL 양쪽에서 항상 닿는 WSL eth0 IP를 기본값으로 쓴다.
public_host="${PHASE5_E2E_PUBLIC_HOST:-}"
if [[ -z "${public_host}" ]]; then
  public_host="127.0.0.1"
  if grep -qi microsoft /proc/version 2>/dev/null &&
    ! curl.exe --version >/dev/null 2>&1; then
    wsl_eth0_ip="$(ip -4 addr show eth0 2>/dev/null |
      grep -oP 'inet \K[0-9.]+' | head -1 || true)"
    if [[ -n "${wsl_eth0_ip}" ]]; then
      public_host="${wsl_eth0_ip}"
      echo "WSL interop 불가: 결제 복귀 공개 호스트를 ${public_host}로 설정합니다." >&2
      echo "검수 브라우저(Windows 포함)는 http://${public_host}:${browser_proxy_port} 로 접속하세요." >&2
    fi
  fi
fi
trusted_browser_base_url="http://${public_host}:${browser_proxy_port}"
server_pid=""
anvil_pid=""
browser_container=""
windows_proxy_pid=""

cleanup() {
  local exit_code=$?
  trap - EXIT
  if [[ "${exit_code}" -ne 0 ]]; then
    if [[ -f "${settlement_tmp}/server.log" ]]; then
      echo "Phase 5 E2E server diagnostics:" >&2
      tail -200 "${settlement_tmp}/server.log" >&2
    fi
    if [[ -f "${settlement_tmp}/anvil.log" ]]; then
      echo "Phase 5 E2E Anvil diagnostics:" >&2
      tail -100 "${settlement_tmp}/anvil.log" >&2
    fi
  fi
  if [[ -n "${browser_container}" ]]; then
    docker rm -f "${browser_container}" >/dev/null 2>&1 || true
  fi
  if [[ -n "${windows_proxy_pid}" ]]; then
    kill "${windows_proxy_pid}" 2>/dev/null || true
  fi
  if [[ -n "${server_pid}" ]]; then
    kill "${server_pid}" 2>/dev/null || true
  fi
  if [[ -n "${anvil_pid}" ]]; then
    kill "${anvil_pid}" 2>/dev/null || true
  fi
  rm -rf "${settlement_tmp}"
  exit "${exit_code}"
}
trap cleanup EXIT

for command in anvil cast forge jq openssl go docker curl; do
  command -v "${command}" >/dev/null || {
    echo "missing Phase 5 E2E command: ${command}" >&2
    exit 1
  }
done

if cast chain-id --rpc-url "${rpc_url}" >/dev/null 2>&1; then
  echo "Phase 5 E2E RPC port ${rpc_port} is already serving another chain." >&2
  echo "Stop the existing local-review process before starting a new one." >&2
  exit 1
fi
if curl -fsS --max-time 1 "${base_url}/readyz" >/dev/null 2>&1; then
  echo "Phase 5 E2E server port ${server_port} is already serving another app." >&2
  echo "Stop the existing local-review process before starting a new one." >&2
  exit 1
fi

new_key() {
  local key address
  while true; do
    key="$(openssl rand -hex 32)"
    if address="$(cast wallet address --private-key "${key}" 2>/dev/null)"; then
      printf '%s %s\n' "${key}" "${address}"
      return
    fi
  done
}

read -r deployer_key deployer_address < <(new_key)
read -r quote_key quote_address < <(new_key)
read -r finalizer_key finalizer_address < <(new_key)
read -r refunder_key refunder_address < <(new_key)
pii_encryption_key="$(openssl rand -base64 32)"

anvil --silent --host 0.0.0.0 --port "${rpc_port}" --chain-id 91342 --block-time 1 \
  --prune-history --cache-path "${settlement_tmp}/anvil-cache" \
  >"${settlement_tmp}/anvil.log" 2>&1 &
anvil_pid="$!"
for _ in {1..30}; do
  if ! kill -0 "${anvil_pid}" 2>/dev/null; then
    echo "Phase 5 Anvil exited before readiness" >&2
    exit 1
  fi
  if cast chain-id --rpc-url "${rpc_url}" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! kill -0 "${anvil_pid}" 2>/dev/null; then
  echo "Phase 5 Anvil exited before owning RPC port ${rpc_port}" >&2
  exit 1
fi
[[ "$(cast chain-id --rpc-url "${rpc_url}")" == "91342" ]]

for funded_address in \
  "${deployer_address}" "${finalizer_address}" "${refunder_address}"
do
  cast rpc --rpc-url "${rpc_url}" anvil_setBalance \
    "${funded_address}" 0x3635c9adc5dea00000 >/dev/null
done

export DEPLOYER="${deployer_address}"
export PAUSER="${deployer_address}"
export QUOTE_SIGNER="${quote_address}"
export FINALIZER="${finalizer_address}"
export REFUNDER="${refunder_address}"
export TEST_FEE="${deployer_address}"
export FAUCET_CLAIM_AMOUNT=1000000000
export FAUCET_COOLDOWN_SECONDS=3600
export FAUCET_DAILY_CAP=1000000000000
export MINIMUM_ESCROW_WINDOW_SECONDS=3600
export SETTLEMENT_ADMIN="${deployer_address}"
export AMAZON_TEST_PRINCIPAL="${deployer_address}"
export WALMART_TEST_PRINCIPAL="${deployer_address}"
export SHOPIFY_TEST_PRINCIPAL="${deployer_address}"
export GENERIC_WEB_TEST_PRINCIPAL="${deployer_address}"
export AMAZON_TEST_REGISTRY_VERSION=1
export WALMART_TEST_REGISTRY_VERSION=1
export SHOPIFY_TEST_REGISTRY_VERSION=1
export GENERIC_WEB_TEST_REGISTRY_VERSION=1

pushd "${repository_root}/contracts" >/dev/null
forge script script/DeployGiwaSepolia.s.sol:DeployGiwaSepolia \
  --rpc-url "${rpc_url}" --private-key "${deployer_key}" --broadcast
deployment_broadcast="broadcast/DeployGiwaSepolia.s.sol/91342/run-latest.json"
export TVITUSD_ADDRESS="$(
  jq -er '.transactions[] | select(.contractName=="VitlaneTestUSD") | .contractAddress' \
    "${deployment_broadcast}" | head -1
)"
export FAUCET_ADDRESS="$(
  jq -er '.transactions[] | select(.contractName=="VitlaneFaucet") | .contractAddress' \
    "${deployment_broadcast}" | head -1
)"
export SETTLEMENT_ADDRESS="$(
  jq -er '.transactions[] | select(.contractName=="VitlaneSettlement") | .contractAddress' \
    "${deployment_broadcast}" | head -1
)"
forge script script/ConfigureGiwaSepolia.s.sol:ConfigureGiwaSepolia \
  --rpc-url "${rpc_url}" --private-key "${deployer_key}" --broadcast
forge script script/VerifyDeployment.s.sol:VerifyDeployment --rpc-url "${rpc_url}"
popd >/dev/null

# v2 환불 자금원은 수취 계좌의 allowance-pull이다(giwa-contract-deploy §5.5):
# fee 반환은 TEST_FEE, 수납 종결 뒤 pass-through 반환은 TestPhase principal이
# Settlement에 approve해야 한다. 로컬 검수에서는 둘 다 deployer 계좌이므로 한
# 번 승인해 whole-MO 취소·환불 보상(REFUND_PARTIAL)이 revert하지 않게 한다.
cast send "${TVITUSD_ADDRESS}" "approve(address,uint256)" "${SETTLEMENT_ADDRESS}" \
  115792089237316195423570985008687907853269984665640564039457584007913129639935 \
  --rpc-url "${rpc_url}" --private-key "${deployer_key}" >/dev/null

token_code="$(cast code "${TVITUSD_ADDRESS}" --rpc-url "${rpc_url}")"
faucet_code="$(cast code "${FAUCET_ADDRESS}" --rpc-url "${rpc_url}")"
settlement_code="$(cast code "${SETTLEMENT_ADDRESS}" --rpc-url "${rpc_url}")"
token_hash="$(cast keccak "${token_code}")"
faucet_hash="$(cast keccak "${faucet_code}")"
settlement_hash="$(cast keccak "${settlement_code}")"
jq -n \
  --arg token_address "${TVITUSD_ADDRESS}" \
  --arg token_hash "${token_hash}" \
  --arg faucet_address "${FAUCET_ADDRESS}" \
  --arg faucet_hash "${faucet_hash}" \
  --arg settlement_address "${SETTLEMENT_ADDRESS}" \
  --arg settlement_hash "${settlement_hash}" \
  '{
    schemaVersion:"vitlane.onchain-deployment.v1",
    version:"phase5-ci",
    environment:"TEST",
    chainId:91342,
    status:"ACTIVE",
    contracts:{
      VitlaneTestUSD:{address:$token_address,runtimeBytecodeHash:$token_hash},
      VitlaneFaucet:{address:$faucet_address,runtimeBytecodeHash:$faucet_hash},
      VitlaneSettlement:{address:$settlement_address,runtimeBytecodeHash:$settlement_hash}
    }
  }' >"${settlement_tmp}/manifest.json"
printf '%s\n' "${quote_key}" >"${settlement_tmp}/quote-signer.key"
printf '%s\n' "${finalizer_key}" >"${settlement_tmp}/finalizer.key"
printf '%s\n' "${refunder_key}" >"${settlement_tmp}/refunder.key"
chmod 0600 "${settlement_tmp}"/*.key

pushd "${repository_root}/web" >/dev/null
npm ci
node --test "${repository_root}/scripts/ci/wallet-identity-kyc-openapi.test.mjs"
VITE_ALLOW_DEV_AUTH_UI=true npm run build
popd >/dev/null
pushd "${repository_root}/server" >/dev/null
go build -buildvcs=false -o "${settlement_tmp}/vitlane" ./cmd/vitlane
popd >/dev/null

docker run --rm --network host -i \
  postgres:16-alpine \
  psql "${TEST_DATABASE_URL}" -v ON_ERROR_STOP=1 \
  <"${repository_root}/web/e2e/fixtures/settlement-preflight.sql"

# PayPal Sandbox 풀 플로우(선택): 호출자 환경에 자격 4종이 전부 있으면 binding을
# 등록하고 서버에 rail을 켠다. CI에는 secrets가 없어 기존 GIWA 전용 경로 그대로다.
paypal_env=()
export E2E_PAYPAL_ENABLED=false
if [[ -n "${PAYPAL_SANDBOX_CLIENT_ID:-}" && -n "${PAYPAL_SANDBOX_CLIENT_SECRET:-}" \
   && -n "${PAYPAL_SANDBOX_WEBHOOK_ID:-}" && -n "${PAYPAL_SANDBOX_MERCHANT_ID:-}" ]]; then
  echo "PayPal Sandbox rail을 로컬 검수에 활성화합니다 (merchant ${PAYPAL_SANDBOX_MERCHANT_ID})."
  env \
    DATABASE_URL="${TEST_DATABASE_URL}" \
    MIGRATIONS_DIR="${repository_root}/server/migrations" \
    PAYPAL_SANDBOX_CLIENT_ID="${PAYPAL_SANDBOX_CLIENT_ID}" \
    PAYPAL_SANDBOX_WEBHOOK_ID="${PAYPAL_SANDBOX_WEBHOOK_ID}" \
    "${settlement_tmp}/vitlane" paypal-binding register \
    --environment SANDBOX \
    --merchant-id "${PAYPAL_SANDBOX_MERCHANT_ID}" \
    --verified-by "${PAYPAL_BINDING_VERIFIED_BY:-local-review}" \
    --evidence "${PAYPAL_BINDING_EVIDENCE:-sandbox order payee readback; owner dashboard 확인 대기}"
  paypal_env=(
    PAYPAL_SANDBOX_ENABLED=true
    PAYPAL_SANDBOX_CLIENT_ID="${PAYPAL_SANDBOX_CLIENT_ID}"
    PAYPAL_SANDBOX_CLIENT_SECRET="${PAYPAL_SANDBOX_CLIENT_SECRET}"
    PAYPAL_SANDBOX_WEBHOOK_ID="${PAYPAL_SANDBOX_WEBHOOK_ID}"
  )
  export E2E_PAYPAL_ENABLED=true
fi

env \
  "${paypal_env[@]}" \
  APP_ENV=test \
  HTTP_ADDR="0.0.0.0:${server_port}" \
  PUBLIC_BASE_URL="${trusted_browser_base_url}" \
  DATABASE_URL="${TEST_DATABASE_URL:?set TEST_DATABASE_URL}" \
  MIGRATIONS_DIR="${repository_root}/server/migrations" \
  WEB_DIR="${repository_root}/web/dist" \
  MARKETING_WEB_DIR="${repository_root}/marketing" \
  MARKETING_BASE_URL="http://marketing.localhost:${server_port}" \
  MARKETING_ADMIN_EMAILS=operator@example.com,local-empty-operator@example.com \
  RESEARCH_CATALOG_PROVIDER="${PHASE5_E2E_CATALOG_PROVIDER:-stub}" \
  SHOPIFY_UCP_ENABLED="${PHASE5_E2E_SHOPIFY_UCP_ENABLED:-false}" \
  CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE="${CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE:-10}" \
  CURATION_CATALOG_RESEARCH_ENABLED=true \
  AGENCY_ORDER_ENABLED=true \
  AGENCY_ORDER_CHECKOUT_PROVIDER="${PHASE5_E2E_CHECKOUT_PROVIDER:-stub}" \
  MANAGED_RUNNER_MODEL_PROVIDER="${PHASE5_E2E_MODEL_PROVIDER:-stub}" \
  ALLOW_DEV_AUTH=true \
  DEV_AUTH_DEFAULT_USER_ID="${local_review_user_id}" \
  PHASE5_SETTLEMENT_ENABLED=true \
  PHASE5_OPERATOR_EMAILS=operator@example.com,local-empty-operator@example.com \
  SETTLEMENT_ENV=LOCAL \
  GIWA_CHAIN_ID=91342 \
  GIWA_RPC_URL="${rpc_url}" \
  GIWA_PUBLIC_RPC_URL="${container_rpc_url}" \
  GIWA_EXPLORER_URL=http://127.0.0.1:8545 \
  ONCHAIN_MANIFEST_PATH="${settlement_tmp}/manifest.json" \
  KYC_ASSURANCE_MODE=MOCK_DOJANG_VERIFIED \
  PII_ENCRYPTION_KEY="${pii_encryption_key}" \
  PII_KEY_VERSION=e2e-v1 \
  QUOTE_SIGNER_KEY_FILE="${settlement_tmp}/quote-signer.key" \
  FINALIZER_KEY_FILE="${settlement_tmp}/finalizer.key" \
  REFUNDER_KEY_FILE="${settlement_tmp}/refunder.key" \
  TVITUSD_ADDRESS="${TVITUSD_ADDRESS}" \
  FAUCET_ADDRESS="${FAUCET_ADDRESS}" \
  SETTLEMENT_ADDRESS="${SETTLEMENT_ADDRESS}" \
  TEST_FEE="${TEST_FEE}" \
  REFUNDER_ADDRESS="${refunder_address}" \
  PAUSER_ADDRESS="${PAUSER}" \
  AMAZON_TEST_PRINCIPAL="${AMAZON_TEST_PRINCIPAL}" \
  WALMART_TEST_PRINCIPAL="${WALMART_TEST_PRINCIPAL}" \
  SHOPIFY_TEST_PRINCIPAL="${SHOPIFY_TEST_PRINCIPAL}" \
  GENERIC_WEB_TEST_PRINCIPAL="${GENERIC_WEB_TEST_PRINCIPAL}" \
  AMAZON_TEST_REGISTRY_VERSION="${AMAZON_TEST_REGISTRY_VERSION}" \
  WALMART_TEST_REGISTRY_VERSION="${WALMART_TEST_REGISTRY_VERSION}" \
  SHOPIFY_TEST_REGISTRY_VERSION="${SHOPIFY_TEST_REGISTRY_VERSION}" \
  GENERIC_WEB_TEST_REGISTRY_VERSION="${GENERIC_WEB_TEST_REGISTRY_VERSION}" \
  FAUCET_CLAIM_AMOUNT="${FAUCET_CLAIM_AMOUNT}" \
  CHAIN_START_BLOCK=0 \
  "${settlement_tmp}/vitlane" >"${settlement_tmp}/server.log" 2>&1 &
server_pid="$!"

for _ in {1..60}; do
  if ! kill -0 "${server_pid}" 2>/dev/null; then
    echo "Phase 5 server exited before owning HTTP port ${server_port}" >&2
    tail -200 "${settlement_tmp}/server.log" >&2
    exit 1
  fi
  if curl -fsS "${base_url}/readyz" >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
if ! curl -fsS "${base_url}/readyz" >/dev/null; then
  tail -200 "${settlement_tmp}/server.log" >&2
  exit 1
fi

# 브라우저 suite 컨테이너(--network host)가 서버에 실제로 닿는 호스트는 Docker
# 배포마다 다르다(WSL 네이티브 Docker Engine=127.0.0.1, Docker Desktop=
# host.docker.internal). 가정 대신 /readyz 도달성을 실측해 확정하고, 설정값이
# 안 닿으면 알려진 후보로 폴백한다 — suite 깊숙한 곳의 타임아웃 대신 여기서
# 명확히 실패한다.
probe_container_host() {
  docker run --rm --network host postgres:16-alpine \
    wget -q -T 3 -O /dev/null "http://$1:${server_port}/readyz" >/dev/null 2>&1
}
if [[ "${skip_browser_suites}" != "true" ]]; then
  bridge_gateway="$(docker network inspect bridge \
    --format '{{(index .IPAM.Config 0).Gateway}}' 2>/dev/null || true)"
  resolved_container_host=""
  probed_hosts=""
  for candidate in "${container_host}" 127.0.0.1 host.docker.internal \
    "${bridge_gateway}"; do
    if [[ -z "${candidate}" || " ${probed_hosts} " == *" ${candidate} "* ]]; then
      continue
    fi
    probed_hosts="${probed_hosts} ${candidate}"
    if probe_container_host "${candidate}"; then
      resolved_container_host="${candidate}"
      break
    fi
  done
  if [[ -z "${resolved_container_host}" ]]; then
    echo "브라우저 suite 컨테이너에서 검수 서버 :${server_port}에 닿는 호스트가 없습니다." >&2
    echo "시도한 후보:${probed_hosts}" >&2
    if [[ "${keep_running}" == "true" ]]; then
      echo "브라우저 suite를 건너뛰고 검수 서버만 유지합니다." >&2
      skip_browser_suites="true"
    else
      exit 1
    fi
  elif [[ "${resolved_container_host}" != "${container_host}" ]]; then
    echo "container host 실측 조정: ${container_host} -> ${resolved_container_host}"
    container_host="${resolved_container_host}"
    container_rpc_url="http://${container_host}:${rpc_port}"
    container_base_url="http://${container_host}:${server_port}"
  fi
fi

if [[ "${windows_proxy_mode}" != "false" ]] && command -v curl.exe >/dev/null 2>&1; then
  if ! curl.exe -fsS --max-time 3 "${base_url}/readyz" >/dev/null 2>&1; then
    # command -v는 PATH 존재만 본다 — WSL interop이 꺼진 상태에서는 node.exe가
    # PATH에 있어도 Exec format error로 죽으므로 실제 실행 가능성을 검사한다.
    if node.exe --version >/dev/null 2>&1 &&
      command -v wslpath >/dev/null 2>&1 &&
      command -v hostname >/dev/null 2>&1
    then
      read -r wsl_host _ < <(hostname -I)
      windows_proxy_script="$(wslpath -w "${repository_root}/scripts/local-review-proxy.cjs")"
      node.exe "${windows_proxy_script}" "${wsl_host}" "${server_port}" "${server_port}" \
        >"${settlement_tmp}/windows-proxy.log" 2>&1 &
      windows_proxy_pid="$!"
      for _ in {1..20}; do
        if curl.exe -fsS --max-time 2 "${base_url}/readyz" >/dev/null 2>&1; then
          break
        fi
        if ! kill -0 "${windows_proxy_pid}" 2>/dev/null; then
          break
        fi
        sleep 1
      done
    fi
  fi
  if ! curl.exe -fsS --max-time 3 "${base_url}/readyz" >/dev/null 2>&1; then
    if [[ -f "${settlement_tmp}/windows-proxy.log" ]]; then
      tail -50 "${settlement_tmp}/windows-proxy.log" >&2
    fi
    if [[ "${windows_proxy_mode}" == "true" ]]; then
      echo "Windows localhost에서 Vitlane 로컬 검수 서버에 연결할 수 없습니다." >&2
      exit 1
    fi
    echo "Windows localhost proxy를 시작하지 못했습니다. WSL 주소로만 검수할 수 있습니다." >&2
  else
    echo "Windows localhost review route: ${base_url}"
  fi
fi
mkdir -p "${screenshot_dir}"

docker run --rm --network host -i \
  postgres:16-alpine \
  psql "${TEST_DATABASE_URL}" -v ON_ERROR_STOP=1 \
  <"${repository_root}/web/e2e/fixtures/settlement.sql"

reset_review_profile() {
  local profile_key="$1"
  local response_file="${settlement_tmp}/reset-${profile_key}.json"
  local status_code
  status_code="$(
    curl -sS -o "${response_file}" -w "%{http_code}" -X POST \
      "${base_url}/api/v1/dev/auth/profiles/${profile_key}/reset"
  )"
  if [[ "${status_code}" != "204" ]]; then
    echo "Local review profile reset failed: ${profile_key} HTTP ${status_code}" >&2
    sed -n '1,20p' "${response_file}" >&2
    exit 1
  fi
}
reset_review_profile "empty-user"
reset_review_profile "empty-operator"
reset_review_profile "multi-product"
docker run --rm --network host -i \
  postgres:16-alpine \
  psql "${TEST_DATABASE_URL}" -v ON_ERROR_STOP=1 \
  <"${repository_root}/web/e2e/fixtures/local-review-multi-product.sql"
# Persisted preview databases must be restartable: prove that the new
# Curation/Cart/Candidate graph can be removed and rebuilt without a fresh DB.
reset_review_profile "multi-product"
docker run --rm --network host -i \
  postgres:16-alpine \
  psql "${TEST_DATABASE_URL}" -v ON_ERROR_STOP=1 \
  <"${repository_root}/web/e2e/fixtures/local-review-multi-product.sql"

# The human multi-product profile must be able to enter OrderSheet immediately.
# Seed its encrypted default address through the production-shaped Account API
# rather than writing PII/ciphertext directly from SQL.
multi_product_cookies="${settlement_tmp}/multi-product-shipping.cookies"
multi_product_session_response="${settlement_tmp}/multi-product-session.json"
multi_product_session_status="$(
  curl -sS -c "${multi_product_cookies}" -o "${multi_product_session_response}" -w "%{http_code}" \
    -H "Content-Type: application/json" \
    --data '{"profileKey":"multi-product"}' \
    "${base_url}/api/v1/dev/auth/session"
)"
if [[ "${multi_product_session_status}" != "201" ]]; then
  echo "Multi-product review session failed: HTTP ${multi_product_session_status}" >&2
  exit 1
fi
multi_product_shipping_response="${settlement_tmp}/multi-product-shipping.json"
multi_product_shipping_status="$(
  curl -sS -b "${multi_product_cookies}" -o "${multi_product_shipping_response}" -w "%{http_code}" \
    -X PUT -H "Content-Type: application/json" \
    --data '{"label":"AgencyOrder Review","recipientName":"Test Buyer","addressLine1":"123 Test Street","addressLine2":"","city":"Seattle","region":"WA","postalCode":"98101","country":"US","phone":"+12065550100"}' \
    "${base_url}/api/v1/account/shipping-profiles/default"
)"
if [[ "${multi_product_shipping_status}" != "201" ]]; then
  echo "Multi-product review shipping seed failed: HTTP ${multi_product_shipping_status}" >&2
  exit 1
fi

if [[ "${reuse_browser_image}" == "true" ]] &&
  docker image inspect vitlane-phase5-playwright >/dev/null 2>&1
then
  echo "Reusing the installed Playwright runtime image for local review."
else
  docker build --progress plain -f "${repository_root}/web/e2e/Dockerfile" \
    -t vitlane-phase5-playwright "${repository_root}"
fi
if ! curl -fsS "${base_url}/readyz" >/dev/null; then
  echo "Phase 5 server lost readiness before browser E2E" >&2
  exit 1
fi
# /readyz is core-only (GAP-007), so also wait for the settlement runtime
# through the authenticated operator ops health before the suites start. The
# empty-operator review profile was reset above and carries an allowlisted
# operator email in this stack.
ops_health_cookies="${settlement_tmp}/ops-health.cookies"
curl -fsS -c "${ops_health_cookies}" -H 'Content-Type: application/json' \
  -d '{"profileKey":"empty-operator"}' \
  "${base_url}/api/v1/dev/auth/session" >/dev/null
settlement_ready=""
for _ in {1..60}; do
  if [[ "$(curl -fsS -b "${ops_health_cookies}" \
    "${base_url}/api/v1/admin/ops/health" | jq -r '.status' 2>/dev/null)" == \
    "ready" ]]; then
    settlement_ready="true"
    break
  fi
  sleep 1
done
if [[ -z "${settlement_ready}" ]]; then
  echo "Phase 5 settlement runtime did not become ready behind core readiness" >&2
  curl -fsS -b "${ops_health_cookies}" "${base_url}/api/v1/admin/ops/health" >&2 ||
    true
  tail -200 "${settlement_tmp}/server.log" >&2
  exit 1
fi
# 브라우저 suite 하나를 컨테이너로 실행한다. 실패 시 CI(keep_running=false)는
# 즉시 종료하지만, 로컬 검수(keep_running=true)는 실패를 기록만 하고 서버를
# 유지한다 — 검수 서버가 suite 실패에 같이 죽으면 수동 검수 자체가 불가능하다.
browser_suite_failures=""
run_browser_suite() {
  local suite_name="$1"
  shift
  browser_container="vitlane-${suite_name}-playwright-run-$$"
  docker create --name "${browser_container}" --network host \
    --volume "${repository_root}/web/e2e:/e2e/e2e:ro" \
    "$@" >/dev/null
  if docker start -a "${browser_container}"; then
    docker cp "${browser_container}:/screenshots/." "${screenshot_dir}" ||
      echo "Phase 5 E2E screenshot copy failed: ${suite_name}" >&2
    docker rm "${browser_container}" >/dev/null 2>&1 || true
    browser_container=""
    return 0
  fi
  docker cp "${browser_container}:/screenshots/." "${screenshot_dir}" >/dev/null 2>&1 || true
  docker rm "${browser_container}" >/dev/null 2>&1 || true
  browser_container=""
  if [[ "${keep_running}" != "true" ]]; then
    exit 1
  fi
  browser_suite_failures="${browser_suite_failures} ${suite_name}"
  echo "Phase 5 E2E suite FAILED: ${suite_name} (keep-running 검수 서버는 유지)" >&2
  return 1
}

if [[ "${skip_browser_suites}" == "true" ]]; then
  echo "Phase 5 browser suites skipped; Server, fixture and local chain are ready for manual review."
else
# Step 2 terminal path: a single intent drives Managed planning and automatic
# Candidate research, then the browser issues an AgencyOrder and consumes the
# existing tVITUSD rail through an actual Anvil receipt and FINALIZED readback.
reset_review_profile "empty-user"
if run_browser_suite agency-order \
  -e E2E_BASE_URL="${container_base_url}" \
  -e E2E_RPC_URL="${container_rpc_url}" \
  -e E2E_SCREENSHOT_DIR=/screenshots \
  -e E2E_AGENCY_ORDER_TIMEOUT_MS=180000 \
  -e E2E_PAYPAL_ENABLED="${E2E_PAYPAL_ENABLED}" \
  vitlane-phase5-playwright node e2e/catalog-agency-order.cjs; then
  if ! docker run --rm --network host -i \
    postgres:16-alpine \
    psql "${TEST_DATABASE_URL}" -v ON_ERROR_STOP=1 \
    <"${repository_root}/web/e2e/fixtures/agency-order-terminal-assertions.sql"; then
    if [[ "${keep_running}" != "true" ]]; then
      exit 1
    fi
    browser_suite_failures="${browser_suite_failures} agency-order-assertions"
    echo "Phase 5 E2E suite FAILED: agency-order-assertions (keep-running 검수 서버는 유지)" >&2
  fi
fi

# The wallet suite owns an empty-user lifecycle and asserts the first mutation
# from zero Wallets. The AgencyOrder suite leaves one current Wallet with a
# dependent order graph for the review profile, so isolate the browser suites.
reset_review_profile "empty-user"
run_browser_suite wallet-identity \
  -e E2E_BASE_URL="${container_base_url}" \
  -e E2E_SCREENSHOT_DIR=/screenshots \
  vitlane-phase5-playwright npm run test:e2e:wallet-identity-kyc || true

run_browser_suite operator-api-usage \
  -e E2E_BASE_URL="${container_base_url}" \
  -e E2E_SCREENSHOT_DIR=/screenshots \
  vitlane-phase5-playwright node e2e/operator-api-usage.cjs || true

reset_review_profile "empty-operator"
run_browser_suite locale-matrix \
  -e E2E_BASE_URL="${container_base_url}" \
  -e MARKETING_BASE_URL="${container_base_url}" \
  -e MARKETING_HOST_HEADER="marketing.localhost:${server_port}" \
  vitlane-phase5-playwright node e2e/locale-matrix.cjs || true

run_browser_suite auth-session-expiry \
  -e E2E_BASE_URL="${container_base_url}" \
  vitlane-phase5-playwright node e2e/auth-session-expiry.cjs || true

run_browser_suite marketing \
  -e MARKETING_BASE_URL="${container_base_url}" \
  -e APP_BASE_URL="${container_base_url}" \
  -e EXPECTED_APP_BASE_URL="${container_base_url}" \
  -e MARKETING_HOST_HEADER="marketing.localhost:${server_port}" \
  -e MARKETING_ORIGIN_HEADER="http://marketing.localhost:${server_port}" \
  -e MARKETING_ADMIN_USER_ID="${local_review_user_id}" \
  -e MARKETING_SCREENSHOT_DIR=/screenshots \
  vitlane-phase5-playwright npm run test:e2e:marketing || true

run_browser_suite product-analytics \
  -e E2E_BASE_URL="${container_base_url}" \
  -e ANALYTICS_EVIDENCE_DIR=/screenshots \
  vitlane-phase5-playwright node e2e/product-analytics.cjs || true

if [[ -z "${browser_suite_failures}" ]]; then
  echo "Phase 5 production-core/simulated-adapter E2E: PASS"
else
  echo "Phase 5 E2E FAILED suites:${browser_suite_failures}" >&2
fi
fi
if [[ "${keep_running}" == "true" ]]; then
  echo "Phase 5 review app remains available at ${base_url}"
  echo "Phase 5 review marketing remains available at http://marketing.localhost:${server_port}"
  if [[ "${public_host}" != "127.0.0.1" ]]; then
    echo "검수 브라우저 접속 주소(Windows·WSL 공통): ${trusted_browser_base_url}"
    echo "PayPal 결제 복귀가 이 호스트로 돌아오므로 반드시 이 주소로 검수하세요."
  fi
  while kill -0 "${server_pid}" 2>/dev/null && kill -0 "${anvil_pid}" 2>/dev/null; do
    sleep 30
  done
  echo "Phase 5 review process stopped unexpectedly" >&2
  exit 1
fi
