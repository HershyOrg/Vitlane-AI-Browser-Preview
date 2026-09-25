#!/usr/bin/env bash
set -euo pipefail
browser_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
browser_checkout="${VITLANE_CHROMIUM_DIR:-${LANE_CHROMIUM_DIR:-$browser_root/.cache/chromium}}"
browser_depot="${VITLANE_DEPOT_TOOLS_DIR:-${LANE_DEPOT_TOOLS_DIR:-$browser_root/.cache/depot_tools}}"
browser_jobs="${VITLANE_BUILD_JOBS:-${LANE_BUILD_JOBS:-2}}"
browser_sync_jobs="${VITLANE_SYNC_JOBS:-${LANE_SYNC_JOBS:-4}}"

if [[ "$(uname -s)" != Linux || "$(uname -m)" != x86_64 ]]; then
  echo "Chromium Android requires an x86_64 Linux build host." >&2
  exit 1
fi
if ! command -v python3 >/dev/null || ! command -v git >/dev/null; then
  echo "Install Python 3 and Git first." >&2
  exit 1
fi
python3 - <<'PY'
import re, subprocess, sys
version = subprocess.check_output(['git', '--version'], text=True)
numbers = re.search(r'(\d+)\.(\d+)\.(\d+)', version)
if not numbers or tuple(map(int, numbers.groups())) < (2, 43, 1):
    sys.exit('Chromium depot_tools requires Git >= 2.43.1 for checkout --end-of-options')
PY
if [[ ! -d "$browser_depot/.git" ]]; then
  mkdir -p "$(dirname "$browser_depot")"
  git clone --depth=1 https://chromium.googlesource.com/chromium/tools/depot_tools.git "$browser_depot"
fi
export PATH="$browser_depot:$PATH"
browser_revision="$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["revision"])' "$browser_root/chromium/revision.json")"
mkdir -p "$browser_checkout"
cd "$browser_checkout"
if [[ ! -f .gclient ]]; then
  gclient config --name src --unmanaged https://chromium.googlesource.com/chromium/src.git
  printf '\ntarget_os = ["android"]\n' >> .gclient
fi
browser_state="$browser_checkout/.vitlane-build-state/$browser_revision"
mkdir -p "$browser_state"
if [[ ! -f "$browser_state/sync.complete" ]]; then
  gclient sync --revision "src@$browser_revision" --nohooks --no-history --jobs "$browser_sync_jobs"
  touch "$browser_state/sync.complete"
fi
cd src
# Never reset or overwrite work in an existing checkout.
if [[ -n "$(git status --porcelain)" ]]; then
  if python3 "$browser_root/scripts/chromium_patch.py" --check --verify-checkout "$PWD"; then
    echo "Vitlane patch already applied; preserving working tree."
  else
    echo "Checkout has other changes. Use a clean LANE_CHROMIUM_DIR." >&2
    exit 1
  fi
else
  if [[ "$(git rev-parse HEAD)" != "$browser_revision" ]]; then
    echo "Checkout revision differs from the pinned version; use a clean checkout." >&2
    exit 1
  fi
fi
if [[ ! -f "$browser_state/packages.complete" ]]; then
  build/install-build-deps.sh --android --no-prompt --no-chromeos-fonts
  touch "$browser_state/packages.complete"
fi
if [[ ! -f "$browser_state/hooks.complete" ]]; then
  gclient runhooks
  touch "$browser_state/hooks.complete"
fi
if ! git apply --reverse --check "$browser_root/chromium/patches/0001-lane-agent.patch" 2>/dev/null; then
  git apply --check "$browser_root/chromium/patches/0001-lane-agent.patch"
  git apply "$browser_root/chromium/patches/0001-lane-agent.patch"
fi
gn gen out/Vitlane --args='target_os="android" target_cpu="arm64" is_debug=false is_component_build=false is_official_build=false symbol_level=0 blink_symbol_level=0 v8_symbol_level=0 use_remoteexec=false concurrent_links=1'
# Check the modified Android/Java integration before the much longer engine build.
# Ninja reuses these outputs when assembling the APK below.
autoninja -C out/Vitlane -j "$browser_jobs" chrome/android:chrome_java
autoninja -C out/Vitlane -j "$browser_jobs" chrome_public_apk
mkdir -p "$browser_root/out"
cp out/Vitlane/apks/ChromePublic.apk "$browser_root/out/VitlaneBrowser-arm64.apk"
echo "APK: $browser_root/out/VitlaneBrowser-arm64.apk"
