#!/usr/bin/env bash
set -euo pipefail

require_result() {
  local job_name="$1"
  local expected="$2"
  local actual="$3"

  if [[ "${actual}" != "${expected}" ]]; then
    printf 'CI gate rejected %s: expected %s, got %s\n' \
      "${job_name}" "${expected}" "${actual}" >&2
    exit 1
  fi
}

require_result changes success "${CHANGES_RESULT:-missing}"
require_result docs-check success "${DOCS_CHECK_RESULT:-missing}"

case "${FULL_CI:-missing}" in
  true)
    expected_heavy_result=success
    ;;
  false)
    expected_heavy_result=skipped
    ;;
  *)
    printf 'CI gate rejected unknown full_ci value: %s\n' \
      "${FULL_CI:-missing}" >&2
    exit 1
    ;;
esac

require_result verify "${expected_heavy_result}" "${VERIFY_RESULT:-missing}"
require_result browser-e2e "${expected_heavy_result}" "${BROWSER_E2E_RESULT:-missing}"
require_result managed-e2e "${expected_heavy_result}" "${MANAGED_E2E_RESULT:-missing}"
require_result phase5-e2e "${expected_heavy_result}" "${PHASE5_E2E_RESULT:-missing}"

printf 'CI gate passed (full_ci=%s)\n' "${FULL_CI}"
