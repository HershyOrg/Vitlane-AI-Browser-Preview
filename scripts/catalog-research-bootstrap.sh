#!/usr/bin/env bash
set -Eeuo pipefail

repository_root="$(cd "$(dirname "$0")/.." && pwd)"
postgres_container="${PHASE8_RESEARCH_POSTGRES_CONTAINER:-vitlane-phase8-live-pg}"
postgres_port="${PHASE8_RESEARCH_POSTGRES_PORT:-55432}"
postgres_database="vitlane_phase8_current"
bootstrap_api_addr="127.0.0.1:${PHASE8_RESEARCH_BOOTSTRAP_API_PORT:-18081}"
bootstrap_tmp="$(mktemp -d)"
api_pid=""

cleanup() {
  if [[ -n "${api_pid}" ]] && kill -0 "${api_pid}" 2>/dev/null; then
    kill "${api_pid}" 2>/dev/null || true
    wait "${api_pid}" 2>/dev/null || true
  fi
  rm -rf "${bootstrap_tmp}"
}
trap cleanup EXIT INT TERM

if curl --max-time 1 -fsS "http://${bootstrap_api_addr}/readyz" >/dev/null 2>&1; then
  echo "Phase 8 bootstrap API is already running at http://${bootstrap_api_addr}." >&2
  echo "Stop the previous review process before recreating the review database." >&2
  exit 1
fi

if docker inspect "${postgres_container}" >/dev/null 2>&1; then
  docker start "${postgres_container}" >/dev/null
else
  docker run --detach \
    --name "${postgres_container}" \
    --publish "127.0.0.1:${postgres_port}:5432" \
    --env POSTGRES_DB=postgres \
    --env POSTGRES_USER=vitlane \
    --env POSTGRES_PASSWORD=vitlane \
    postgres:16-alpine >/dev/null
fi

for _ in {1..30}; do
  if docker exec "${postgres_container}" pg_isready -U vitlane -d postgres >/dev/null 2>&1; then
    break
  fi
  sleep 1
done
docker exec "${postgres_container}" pg_isready -U vitlane -d postgres >/dev/null

# This script owns only the fixed local Phase 8 review database. Recreating it
# is intentional: the browser proof must never inherit stale
# CandidatePool/CartView state across the Phase 8 activation migrations.
docker exec "${postgres_container}" dropdb --if-exists --force -U vitlane "${postgres_database}"
docker exec "${postgres_container}" createdb -U vitlane "${postgres_database}"

DATABASE_URL="postgres://vitlane:vitlane@127.0.0.1:${postgres_port}/${postgres_database}?sslmode=disable" \
  PHASE8_RESEARCH_API_ADDR="${bootstrap_api_addr}" \
  "${repository_root}/scripts/catalog-research-live-api.sh" \
  >"${bootstrap_tmp}/api.log" 2>&1 &
api_pid=$!

for _ in {1..90}; do
  if curl -fsS "http://${bootstrap_api_addr}/readyz" >/dev/null 2>&1; then
    break
  fi
  if ! kill -0 "${api_pid}" 2>/dev/null; then
    sed -n '1,160p' "${bootstrap_tmp}/api.log" >&2
    exit 1
  fi
  sleep 1
done
if ! curl -fsS "http://${bootstrap_api_addr}/readyz" >/dev/null; then
  sed -n '1,160p' "${bootstrap_tmp}/api.log" >&2
  exit 1
fi

schema_ready="$(docker exec "${postgres_container}" \
  psql -U vitlane -d "${postgres_database}" -Atc \
  "SELECT to_regclass('public.schema_migrations') IS NOT NULL AND to_regclass('public.research_rounds') IS NOT NULL AND to_regclass('public.agency_orders') IS NOT NULL")"
if [[ "${schema_ready}" != "t" ]]; then
  echo "Phase 8 review schema is not ready after API readiness." >&2
  sed -n '1,160p' "${bootstrap_tmp}/api.log" >&2
  exit 1
fi
latest_migration="$(find "${repository_root}/server/migrations" -maxdepth 1 -type f \
  -name '*.up.sql' -printf '%f\n' | LC_ALL=C sort | tail -n 1)"
migration_ready="$(docker exec "${postgres_container}" \
  psql -U vitlane -d "${postgres_database}" -Atc \
  "SELECT EXISTS (SELECT 1 FROM schema_migrations WHERE version='${latest_migration}')")"
if [[ "${migration_ready}" != "t" ]]; then
  echo "Phase 8 latest migration ${latest_migration} is not applied after API readiness." >&2
  sed -n '1,160p' "${bootstrap_tmp}/api.log" >&2
  exit 1
fi

docker exec -i "${postgres_container}" \
  psql -U vitlane -d "${postgres_database}" -v ON_ERROR_STOP=1 \
  <"${repository_root}/web/e2e/fixtures/settlement.sql" >/dev/null

status_code="$(curl -sS -o "${bootstrap_tmp}/reset.json" -w "%{http_code}" \
  -X POST "http://${bootstrap_api_addr}/api/v1/dev/auth/profiles/multi-product/reset")"
if [[ "${status_code}" != "204" ]]; then
  echo "Phase 8 local profile reset failed: HTTP ${status_code}" >&2
  sed -n '1,40p' "${bootstrap_tmp}/reset.json" >&2
  exit 1
fi

docker exec -i "${postgres_container}" \
  psql -U vitlane -d "${postgres_database}" -v ON_ERROR_STOP=1 \
  <"${repository_root}/web/e2e/fixtures/local-review-multi-product.sql" >/dev/null

echo "Phase 8 review database ready: ${postgres_container}/${postgres_database}"
echo "Migrations, development users and the multi-product Curation fixture were recreated."
