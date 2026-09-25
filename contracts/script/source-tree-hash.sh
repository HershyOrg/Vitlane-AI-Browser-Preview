#!/usr/bin/env bash
set -euo pipefail

contracts_root="$(cd "$(dirname "$0")/.." && pwd)"

# Stream NUL-framed relative paths and contents directly into sha256sum.
digest="$(
  cd "${contracts_root}"
  while IFS= read -r -d '' source_file; do
    printf '%s\0' "${source_file}"
    cat "${source_file}"
    printf '\0'
  done < <(find src -type f -name '*.sol' -print0 | sort -z) |
    sha256sum |
    cut -d ' ' -f 1
)"
actual="sha256:${digest}"

if [[ "${1:-}" == "--check" ]]; then
  if [[ -n "${2:-}" ]]; then
    manifest="$2"
  else
    active_version="$(
      jq -er '.activeVersion' "${contracts_root}/deployments/91342/index.json"
    )"
    manifest="${contracts_root}/deployments/91342/${active_version}.json"
  fi
  expected="$(jq -er '.sourceTreeHash' "${manifest}")"
  if [[ "${actual}" != "${expected}" ]]; then
    echo "source tree hash mismatch: expected=${expected} actual=${actual}" >&2
    exit 1
  fi
  echo "source tree hash verified: ${actual}"
  exit 0
fi

printf '%s\n' "${actual}"
