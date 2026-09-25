import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';

const coordinator = await readFile(new URL(
  '../chromium/java/org/chromium/chrome/browser/lane/LaneAgentCoordinator.java', import.meta.url), 'utf8');
const pageAgent = await readFile(new URL('../agent/page-agent.js', import.meta.url), 'utf8');
const patch = await readFile(new URL('../chromium/patches/0001-lane-agent.patch', import.meta.url), 'utf8');

test('Chromium patch exposes only the Vitlane world without a caller-selected world id', () => {
  assert.match(patch, /default void evaluateVitlaneAgentJavaScript\(/);
  assert.match(patch, /constexpr int32_t kIsolatedWorldIdLaneAgent =/);
  assert.match(patch, /ISOLATED_WORLD_ID_CONTENT_END \+ 4;/);
  assert.match(patch, /kIsolatedWorldIdLaneAgent\);/);
  assert.doesNotMatch(patch, /^\+.*evaluateJavaScriptInIsolatedWorld/m);
  assert.doesNotMatch(patch, /^\+.*int32_t world_id/m);
  assert.match(coordinator, /evaluateVitlaneAgentJavaScript\(script/);
});

test('native host records cancellation uncertainty and strictly bounds signed commands', () => {
  assert.match(coordinator, /"CANCELLED_AFTER_START"/);
  assert.match(coordinator, /expiration - now > 60_000/);
  assert.doesNotMatch(coordinator, /!replay && \(expiration/);
  assert.match(coordinator, /\^hmac-sha256:\[A-Za-z0-9_\-\]\{43\}\$/);
  assert.match(coordinator, /Character\.getType\(codePoint\) == Character\.FORMAT/);
  assert.match(coordinator, /AI response · untrusted single-line text · not result evidence/);
  assert.doesNotMatch(coordinator, /action\.optString\("reason"/);
  assert.doesNotMatch(pageAgent, /code: 'FINISHED'/);
  assert.match(pageAgent, /code: 'AI_RESPONSE_RECORDED'/);
});

test('v1 never performs automatic link navigation without a native navigation policy', () => {
  assert.doesNotMatch(pageAgent, /location\.assign\(/);
  assert.match(pageAgent, /status: 'handoff', code: 'NAVIGATION_REQUIRES_USER'/);
  assert.match(pageAgent, /redirect- and DNS-aware NavigationThrottle/);
  assert.match(coordinator, /onLoadStarted\(Tab tab, boolean toDifferentDocument\)[\s\S]*?takeOver\(/);
  assert.match(coordinator, /onUrlUpdated\(Tab tab\)[\s\S]*?takeOver\(/);
  assert.match(coordinator, /!mDocumentUrl\.equals\(mTaskTab\.getUrl\(\)\.getSpec\(\)\)/);
  assert.match(coordinator, /if \(mTaskTab\.isLoading\(\)\)/);
});

test('Android owns a bounded single-offer Coupang approval and rejects broader authority', () => {
  assert.match(coordinator,
    /\^\/vp\/products\/\[1-9\]\[0-9\]\{0,19\}\/\?\$/);
  assert.match(coordinator, /uniqueNumericQueryParameter\(uri\.getRawQuery\(\), "itemId"\)/);
  assert.match(coordinator, /uniqueNumericQueryParameter\(uri\.getRawQuery\(\), "vendorItemId"\)/);
  assert.match(coordinator,
    /"https:\/\/www\.coupang\.com\/vp\/products\/" \+ productId[\s\S]*?"\?itemId=" \+ itemId \+ "&vendorItemId=" \+ vendorItemId/);

  assert.match(coordinator, /buildApprovedCoupangPreparation\(\)/);
  assert.match(coordinator,
    /"approvalDigest", "sha256:" \+ hex\(sha256\(canonicalize\(digestMaterial\)\)\)/);
  assert.match(coordinator, /verifyCoupangPlan\("single", "single_buy_now"/);
  assert.match(coordinator, /APPROVAL_DIGEST_MISMATCH/);
  assert.match(coordinator, /!seenGroups\.add\(groupName\)/);
  assert.match(coordinator,
    /request\.put\("approvedPreparation",\s*new JSONObject\(mApprovedPreparation\.toString\(\)\)\)/);

  assert.match(coordinator,
    /setOf\("select_option", "verify_options", "set_quantity", "buy_now",\s*"add_to_cart", "start_checkout"\)/);
  assert.match(coordinator,
    /!"https:\/\/cart\.coupang\.com"\.equals\(expectedOrigin\)\s*\|\|\s*!"\/cartView\.pang"\.equals\(expectedPath\)/);
  assert.doesNotMatch(coordinator, /"\/cartView\.pang\/"\.equals\(expectedPath\)/);
  assert.match(coordinator, /expectedLocalCoupangStep\(mApprovedPreparation\)/);
  assert.match(coordinator, /advanceApprovedPreparationCursor\(\)/);
  assert.match(coordinator, /mApprovedPreparation\.put\("cursor", cursor \+ 1\)/);
  assert.match(coordinator, /COUPANG_ACTIVATION_HANDOFF_STEPS/);
  assert.match(coordinator, /Final order\/payment excluded/);
  assert.match(coordinator, /mApprovedPreparation = null;[\s\S]*?mTaskTab\.removeObserver/);

  assert.doesNotMatch(coordinator, /"(?:payment|pay|place_order|confirm_order|confirm_purchase)"/);
  assert.doesNotMatch(pageAgent, /case '(?:payment|pay|place_order|confirm_order|confirm_purchase)'/);
});
