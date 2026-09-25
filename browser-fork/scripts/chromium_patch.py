#!/usr/bin/env python3
"""Build and optionally verify a reproducible patch against one Chromium revision."""
import argparse
import base64
import concurrent.futures
import difflib
import hashlib
import json
import pathlib
import subprocess
import tempfile
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[1]
PIN = json.loads((ROOT / "chromium/revision.json").read_text())
CACHE = ROOT / ".cache/upstream"
ACTIVITY = "chrome/android/java/src/org/chromium/chrome/browser/ChromeTabbedActivity.java"
SOURCES = "chrome/android/chrome_java_sources.gni"
INTERFACE = "content/public/android/java/src/org/chromium/content_public/browser/WebContents.java"
IMPL = "content/public/android/java/src/org/chromium/content/browser/webcontents/WebContentsImpl.java"
HEADER = "content/browser/web_contents/web_contents_android.h"
NATIVE = "content/browser/web_contents/web_contents_android.cc"
WORLD_CPP = "chrome/common/chrome_isolated_world_ids.h"
WORLD_JAVA = "chrome/android/java/src/org/chromium/chrome/browser/common/ChromeIsolatedWorldIds.java"
PATHS = [ACTIVITY, SOURCES, INTERFACE, IMPL, HEADER, NATIVE, WORLD_CPP, WORLD_JAVA]
MANIFEST = ROOT / "chromium/upstream-sha256.json"
COORDINATOR = ROOT / "chromium/java/org/chromium/chrome/browser/lane/LaneAgentCoordinator.java"
PAGE_AGENT = ROOT / "agent/page-agent.js"


def checked_source(path, markers):
    source = path.read_text()
    missing = [marker for marker in markers if marker not in source]
    if missing:
        raise RuntimeError(f"Protocol source {path} is stale; missing: {', '.join(missing)}")
    return source


def download(path):
    target = CACHE / path
    if not target.exists():
        url = f"https://chromium.googlesource.com/chromium/src/+/{PIN['revision']}/{path}?format=TEXT"
        with urllib.request.urlopen(url, timeout=60) as response:
            data = base64.b64decode(response.read())
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(data)
    data = target.read_bytes()
    return path, data.decode(), hashlib.sha256(data).hexdigest()


def replace_once(source, before, after):
    if source.count(before) != 1:
        raise RuntimeError(f"Expected one patch anchor, got {source.count(before)}: {before[:100]}")
    return source.replace(before, after, 1)


def make_changes(original):
    changed = dict(original)

    def edit(path, before, after):
        changed[path] = replace_once(changed[path], before, after)

    edit(ACTIVITY, "import org.chromium.chrome.browser.notifications.tips.TipsPromoCoordinator;",
         "import org.chromium.chrome.browser.lane.LaneAgentCoordinator;\n"
         "import org.chromium.chrome.browser.notifications.tips.TipsPromoCoordinator;")
    edit(ACTIVITY, "    private TipsPromoCoordinator mTipsPromoCoordinator;",
         "    private TipsPromoCoordinator mTipsPromoCoordinator;\n\n"
         "    private @Nullable LaneAgentCoordinator mLaneAgent;")
    edit(ACTIVITY, "            super.finishNativeInitialization();",
         "            super.finishNativeInitialization();\n"
         "            mLaneAgent = new LaneAgentCoordinator(\n"
         "                    this, this::getActivityTab);")
    edit(ACTIVITY, "    public void onStopWithNative() {\n",
         "    public void onStopWithNative() {\n"
         "        if (mLaneAgent != null) mLaneAgent.onBackgrounded();\n")
    edit(ACTIVITY, "    public void onDestroyInternal() {\n",
         "    public void onDestroyInternal() {\n"
         "        if (mLaneAgent != null) {\n"
         "            mLaneAgent.destroy();\n"
         "            mLaneAgent = null;\n"
         "        }\n")
    edit(SOURCES, "chrome_java_sources = [\n", "chrome_java_sources = [\n"
         '  "java/src/org/chromium/chrome/browser/lane/LaneAgentCoordinator.java",\n'
         '  "java/src/org/chromium/chrome/browser/lane/LanePageScript.java",\n')

    # Reserve ID +4 for Lane on every platform; leave Apple's slot consistent in Java.
    edit(WORLD_CPP, "#if BUILDFLAG(IS_MAC)\n", "  // Lane's browser-owned agent scripts.\n"
         "  ISOLATED_WORLD_ID_LANE_AGENT,\n\n#if BUILDFLAG(IS_MAC)\n")
    edit(WORLD_JAVA, "    ChromeIsolatedWorldIds.ISOLATED_WORLD_UNUSED_MAC,",
         "    ChromeIsolatedWorldIds.ISOLATED_WORLD_ID_LANE_AGENT,\n"
         "    ChromeIsolatedWorldIds.ISOLATED_WORLD_UNUSED_MAC,")
    edit(WORLD_JAVA,
         "    int ISOLATED_WORLD_UNUSED_MAC = IsolatedWorldIds.ISOLATED_WORLD_ID_CONTENT_END + 4;\n"
         "    int ISOLATED_WORLD_ID_UNUSED_EXTENSIONS = IsolatedWorldIds.ISOLATED_WORLD_ID_CONTENT_END + 5;",
         "    int ISOLATED_WORLD_ID_LANE_AGENT = IsolatedWorldIds.ISOLATED_WORLD_ID_CONTENT_END + 4;\n"
         "    int ISOLATED_WORLD_UNUSED_MAC = IsolatedWorldIds.ISOLATED_WORLD_ID_CONTENT_END + 5;\n"
         "    int ISOLATED_WORLD_ID_UNUSED_EXTENSIONS = IsolatedWorldIds.ISOLATED_WORLD_ID_CONTENT_END + 6;")

    edit(INTERFACE, "    void evaluateJavaScript(String script, @Nullable JavaScriptCallback callback);",
         "    void evaluateJavaScript(String script, @Nullable JavaScriptCallback callback);\n\n"
         "    /** Runs Vitlane-owned code only in Vitlane's dedicated isolated world. */\n"
         "    default void evaluateVitlaneAgentJavaScript(\n"
         "            String script, @Nullable JavaScriptCallback callback) {\n"
         "        throw new UnsupportedOperationException(\n"
         "                \"Vitlane agent execution requires a live WebContentsImpl\");\n"
         "    }")
    edit(IMPL, "    @Override\n    public void evaluateJavaScriptForTests(",
         "    @Override\n"
         "    public void evaluateVitlaneAgentJavaScript(\n"
         "            String script, @Nullable JavaScriptCallback callback) {\n"
         "        ThreadUtils.assertOnUiThread();\n"
         "        if (isDestroyed() || script == null) return;\n"
         "        WebContentsImplJni.get().evaluateVitlaneAgentJavaScript(\n"
         "                mNativeWebContentsAndroid, script, callback);\n"
         "    }\n\n    @Override\n    public void evaluateJavaScriptForTests(")
    edit(IMPL, "        void evaluateJavaScriptForTests(\n",
         "        void evaluateVitlaneAgentJavaScript(\n"
         "                long nativeWebContentsAndroid,\n"
         "                String script,\n"
         "                @Nullable JavaScriptCallback callback);\n\n"
         "        void evaluateJavaScriptForTests(\n")
    edit(HEADER, "  void EvaluateJavaScriptForTests(\n",
         "  void EvaluateVitlaneAgentJavaScript(\n"
         "      JNIEnv* env,\n"
         "      const base::android::JavaRef<jstring>& script,\n"
         "      const base::android::JavaRef<jobject>& callback);\n"
         "  void EvaluateJavaScriptForTests(\n")
    edit(NATIVE, "void WebContentsAndroid::EvaluateJavaScriptForTests(\n", """void WebContentsAndroid::EvaluateVitlaneAgentJavaScript(
    JNIEnv* env,
    const JavaRef<jstring>& script,
    const JavaRef<jobject>& callback) {
  // This browser-owned API has no caller-selected world ID. Keep the value in
  // sync with ISOLATED_WORLD_ID_LANE_AGENT in chrome_isolated_world_ids.h
  // without adding a //content -> //chrome dependency.
  constexpr int32_t kIsolatedWorldIdLaneAgent =
      ISOLATED_WORLD_ID_CONTENT_END + 4;
  static_assert(kIsolatedWorldIdLaneAgent < ISOLATED_WORLD_ID_MAX);
  auto* frame = web_contents_->GetPrimaryMainFrame();
  if (!frame || !frame->IsRenderFrameLive() ||
      !frame->GetLastCommittedURL().SchemeIsHTTPOrHTTPS()) {
    if (callback) {
      ScopedJavaGlobalRef<jobject> retained(env, callback);
      JavaScriptResultCallback(retained, base::Value());
    }
    return;
  }
  if (!callback) {
    frame->ExecuteJavaScriptInIsolatedWorld(
        ConvertJavaStringToUTF16(env, script), base::NullCallback(),
        kIsolatedWorldIdLaneAgent);
    return;
  }
  ScopedJavaGlobalRef<jobject> retained(env, callback);
  frame->ExecuteJavaScriptInIsolatedWorld(
      ConvertJavaStringToUTF16(env, script),
      base::BindOnce(&JavaScriptResultCallback, retained),
      kIsolatedWorldIdLaneAgent);
}

void WebContentsAndroid::EvaluateJavaScriptForTests(
""")
    destination = "chrome/android/java/src/org/chromium/chrome/browser/lane/"
    coordinator = checked_source(COORDINATOR, [
        '"protocolVersion"', '"authorizedCommand"', '"serverPermit"', '"HmacSHA256"',
        '"controlGeneration"', '"RECEIVED"', '"VALIDATED"', '"STARTED"',
        '"APPLIED"', '"OUTCOME_UNKNOWN"', '__laneAgent.snapshot(', '__laneAgent.inspect(',
        '__laneAgent.execute(', 'builtin.coupang.purchase-preparation',
        'buildApprovedCoupangPreparation()', 'request.put("approvedPreparation"',
        'advanceApprovedPreparationCursor()', 'APPROVAL_DIGEST_MISMATCH',
    ])
    page_agent = checked_source(PAGE_AGENT, [
        'snapshot(rawMetadata)', 'inspect(command)', 'execute(command)',
        "case 'open_candidate'", "case 'run_preparation_step'", "case 'request_human'",
    ])
    changed[destination + "LaneAgentCoordinator.java"] = coordinator
    encoded = base64.b64encode(page_agent.encode()).decode()
    chunks = [encoded[i:i + 100] for i in range(0, len(encoded), 100)]
    literal = '\n            + '.join(json.dumps(c) for c in chunks)
    changed[destination + "LanePageScript.java"] = """// Generated by scripts/chromium_patch.py. Do not edit.
package org.chromium.chrome.browser.lane;

import android.util.Base64;
import java.nio.charset.StandardCharsets;

final class LanePageScript {
    static final String SOURCE = new String(Base64.decode(
            """ + literal + """, Base64.DEFAULT), StandardCharsets.UTF_8);
    private LanePageScript() {}
}
"""
    return changed


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--check", action="store_true")
    parser.add_argument("--verify-checkout", type=pathlib.Path)
    args = parser.parse_args()
    with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
        fetched = list(executor.map(download, PATHS))
    original = {p: content for p, content, sha in fetched}
    hashes = {p: sha for p, content, sha in fetched}
    if MANIFEST.exists():
        if json.loads(MANIFEST.read_text()) != hashes:
            raise RuntimeError("Pinned upstream file hashes do not match; do not apply patch")
    else:
        if args.check:
            raise RuntimeError("Missing upstream hash manifest")
        MANIFEST.write_text(json.dumps(hashes, indent=2) + "\n")
    changed = make_changes(original)
    if args.verify_checkout:
        checkout = args.verify_checkout.resolve()
        head = subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=checkout, text=True).strip()
        if head != PIN["revision"]:
            raise RuntimeError("Checkout HEAD does not match the pinned revision")
        status = subprocess.check_output(["git", "status", "--porcelain=v1", "-z", "--untracked-files=all"], cwd=checkout).decode()
        paths = {entry[3:] for entry in status.split("\0") if entry}
        if paths != set(changed):
            raise RuntimeError("Checkout has missing patches or unrelated changes")
        for path, expected in changed.items():
            if (checkout / path).read_text() != expected:
                raise RuntimeError(f"Unexpected checkout contents: {path}")
        print("Existing checkout contains exactly the expected Vitlane browser-agent changes")
    patch = ""
    for path, after in changed.items():
        before = original.get(path, "")
        if before == after:
            continue
        patch += f"diff --git a/{path} b/{path}\n"
        if path not in original:
            patch += "new file mode 100644\n"
        patch += "".join(difflib.unified_diff(before.splitlines(True), after.splitlines(True),
            fromfile=f"a/{path}" if path in original else "/dev/null", tofile=f"b/{path}"))
    output = ROOT / "chromium/patches/0001-lane-agent.patch"
    if args.check:
        if not output.exists() or output.read_text() != patch:
            raise RuntimeError("Generated patch is out of date; run npm run patch")
    else:
        output.parent.mkdir(parents=True, exist_ok=True)
        output.write_text(patch)
    with tempfile.TemporaryDirectory() as directory:
        work = pathlib.Path(directory)
        for path, content in original.items():
            target = work / path
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_text(content)
        subprocess.run(["git", "init", "-q", directory], check=True)
        subprocess.run(["git", "apply", "--check", str(output)], cwd=work, check=True)
        subprocess.run(["git", "apply", str(output)], cwd=work, check=True)
        for path, expected in changed.items():
            if (work / path).read_text() != expected:
                raise RuntimeError(f"Patch result mismatch: {path}")
    print(f"Verified {len(changed)} patched files against Chromium {PIN['version']} ({PIN['revision']})")


if __name__ == "__main__":
    main()
