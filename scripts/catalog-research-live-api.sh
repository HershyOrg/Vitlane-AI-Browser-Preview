#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"

export APP_ENV="development"
export DATABASE_URL="${DATABASE_URL:-postgres://vitlane:vitlane@127.0.0.1:55432/vitlane_phase8_current?sslmode=disable}"
export MIGRATE_ON_START="true"
export MIGRATIONS_DIR="${repository_root}/server/migrations"
export HTTP_ADDR="${PHASE8_RESEARCH_API_ADDR:-127.0.0.1:8080}"
export PUBLIC_BASE_URL="http://${HTTP_ADDR}"
export TRUSTED_BROWSER_ORIGIN="${PHASE8_RESEARCH_REVIEW_ORIGIN:-http://127.0.0.1:4178}"
export SHOPIFY_UCP_ENABLED="true"
export CURATION_CATALOG_RESEARCH_ENABLED="true"
# Amazon review uses saved observations until the shared UI is ready for live
# verification. A missing fixture must fail instead of spending API quota.
if [[ "${AMAZON_PRODUCT_SEARCH_ENABLED:-false}" == "true" ]]; then
  export AMAZON_PRODUCT_SEARCH_MODE="${AMAZON_PRODUCT_SEARCH_MODE:-stub}"
  if [[ "${AMAZON_PRODUCT_SEARCH_MODE}" == "stub" ]]; then
    export AMAZON_PRODUCT_SEARCH_STUB_FILE="${AMAZON_PRODUCT_SEARCH_STUB_FILE:-${repository_root}/.local/amazon-review/catalog.json}"
    unset AMAZON_PRODUCT_SEARCH_API
    if [[ ! -f "${AMAZON_PRODUCT_SEARCH_STUB_FILE}" ]]; then
      echo "Amazon stub review requires a saved catalog fixture." >&2
      exit 1
    fi
  fi
fi
# A complete human review traverses search, repeated Variant pages, fresh
# workspace rendering and Prepare Agency Order. Keep the local review usable;
# the service-level 10/minute rejection contract remains covered by tests.
export CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE="${CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE:-30}"
export CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY="${CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY:-16}"
export PHASE5_SETTLEMENT_ENABLED="false"
export MANAGED_RUNNER_ENABLED="true"
export MANAGED_RUNNER_MODEL_PROVIDER="${MANAGED_RUNNER_MODEL_PROVIDER:-stub}"
case "${MANAGED_RUNNER_MODEL_PROVIDER}" in
  stub)
    managed_provider_summary="local deterministic fixture"
    ;;
  openai)
    if [[ -z "${MANAGED_OPENAI_API_SECRET:-}" ]]; then
      echo "OpenAI Managed review requires MANAGED_OPENAI_API_SECRET." >&2
      exit 1
    fi
    managed_provider_summary="live OpenAI managed model"
    ;;
  *)
    echo "Unsupported MANAGED_RUNNER_MODEL_PROVIDER: ${MANAGED_RUNNER_MODEL_PROVIDER}" >&2
    exit 1
    ;;
esac
export ALLOW_DEV_AUTH="true"
export DEV_AUTH_DEFAULT_USER_ID="e5000000-0000-4000-8000-000000000103"
export PHASE5_OPERATOR_EMAILS="${PHASE5_OPERATOR_EMAILS:-operator@example.com,local-empty-operator@example.com}"
export GOCACHE="${GOCACHE:-/tmp/vitlane-phase8-live-go-cache}"
api_binary="${PHASE8_RESEARCH_API_BINARY:-/tmp/vitlane-phase8-live-server-${HTTP_ADDR//[^[:alnum:]]/_}}"
source_revision="$(git -C "${repository_root}" rev-parse HEAD)"
if ! git -C "${repository_root}" diff-index --quiet HEAD --; then
  source_revision="${source_revision}-dirty"
fi

echo "Phase 8-1 Vitlane API + real Shopify read-only bridge"
echo "API: http://${HTTP_ADDR}"
echo "Rate limit: ${CURATION_CATALOG_RESEARCH_CALLS_PER_MINUTE}/minute; concurrency: ${CURATION_CATALOG_RESEARCH_MAX_CONCURRENCY}"
echo "Variant modal은 실제 Shopify read, CartView mutation은 provider call 0, PrepareAgencyOrder는 fresh lookup입니다."
echo "Add Target/Auto Managed provider: ${managed_provider_summary}"
echo "Shopify catalog provider: live read-only"
echo "Amazon catalog enabled: ${AMAZON_PRODUCT_SEARCH_ENABLED:-false}"
echo "Amazon catalog mode: ${AMAZON_PRODUCT_SEARCH_MODE:-live}"
echo "실제 Vitlane UI: ${TRUSTED_BROWSER_ORIGIN}/curations/e5100000-0000-4000-8000-000000000002"
echo "로컬 TEST 로그인과 Shopify live read-only bridge를 함께 활성화합니다."

cd "${repository_root}/server"
# Nested worktrees can make Go's automatic VCS discovery stamp the outer
# checkout instead of this review tree. Disable that non-authoritative stamp
# and attach the exact source revision as a stable build setting.
go build -buildvcs=false -ldflags "-X main.sourceRevision=${source_revision}" -o "${api_binary}" ./cmd/vitlane
# Keep the launcher PID equal to the serving process PID. `go run` leaves its
# compiled child behind when the review/bootstrap script terminates, which can
# make a later bootstrap mistake the stale /readyz response for the new DB.
exec "${api_binary}"
