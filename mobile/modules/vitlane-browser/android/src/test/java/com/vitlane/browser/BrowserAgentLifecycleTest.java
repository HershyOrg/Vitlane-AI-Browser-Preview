package com.vitlane.browser;

import static org.junit.Assert.*;
import android.net.Uri;
import android.os.Looper;
import android.os.SystemClock;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.ValueCallback;
import android.webkit.WebResourceRequest;
import android.webkit.WebResourceResponse;
import android.webkit.WebView;
import android.widget.EditText;
import android.widget.FrameLayout;
import android.widget.TextView;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.io.ByteArrayInputStream;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.AbstractExecutorService;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.TimeUnit;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Before;
import org.junit.Ignore;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.Shadows;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28, manifest = Config.NONE)
@LooperMode(LooperMode.Mode.PAUSED)
public class BrowserAgentLifecycleTest {
    private VitlaneBrowserActivity activity;
    private ControlledWebView web;
    private ControlledExecutor worker;
    private static class ControlledExecutor extends AbstractExecutorService {
        final List<Runnable> pending = new ArrayList<>();
        private boolean stopped;
        @Override public void execute(Runnable task) { pending.add(task); }
        @Override public void shutdown() { stopped = true; }
        @Override public List<Runnable> shutdownNow() { stopped = true; return new ArrayList<>(pending); }
        @Override public boolean isShutdown() { return stopped; }
        @Override public boolean isTerminated() { return stopped; }
        @Override public boolean awaitTermination(long timeout, TimeUnit unit) { return stopped; }
    }
    private static class ControlledWebView extends WebView {
        String url = "https://example.com/a";
        final List<ValueCallback<String>> callbacks = new ArrayList<>();
        final List<Integer> touchActions = new ArrayList<>();
        final List<Float> touchXs = new ArrayList<>(), touchYs = new ArrayList<>();
        ControlledWebView(VitlaneBrowserActivity context) { super(context); }
        @Override public String getUrl() { return url; }
        @Override public void evaluateJavascript(String script, ValueCallback<String> callback) { callbacks.add(callback); }
        @Override public boolean dispatchTouchEvent(MotionEvent event) {
            touchActions.add(event.getActionMasked()); touchXs.add(event.getX()); touchYs.add(event.getY());
            return true;
        }
    }
    private Object get(String name) throws Exception {
        Field f = VitlaneBrowserActivity.class.getDeclaredField(name); f.setAccessible(true); return f.get(activity);
    }
    private void set(String name, Object value) throws Exception {
        Field f = VitlaneBrowserActivity.class.getDeclaredField(name); f.setAccessible(true); f.set(activity, value);
    }
    private Object call(String name, Class<?>[] types, Object... values) throws Exception {
        Method m = VitlaneBrowserActivity.class.getDeclaredMethod(name, types); m.setAccessible(true); return m.invoke(activity, values);
    }
    private JSONObject page(String url) throws Exception {
        return new JSONObject().put("url", url).put("documentId", "doc-a").put("title", "Public page")
                .put("text", "Red shoes cost 25 dollars").put("sensitive", false).put("elements", new JSONArray())
                .put("readiness", new JSONObject().put("readyState", "complete").put("domQuietMs", 800).put("pending", false));
    }
    private void handle(JSONObject observation, JSONObject action) throws Exception {
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                (Integer)get("mGeneration"), (Long)get("mDocumentEpoch"), web.url, observation, action);
    }
    private View findButton(View root, String text) {
        if (root instanceof TextView && text.contentEquals(((TextView)root).getText())) return root;
        if (root instanceof ViewGroup) {
            ViewGroup group = (ViewGroup)root;
            for (int i = 0; i < group.getChildCount(); i++) {
                View result = findButton(group.getChildAt(i), text);
                if (result != null) return result;
            }
        }
        return null;
    }
    private View humanButton(String text) throws Exception {
        View button = findButton((View)get("mHumanPanel"), text);
        assertNotNull("Missing inline button: " + text, button);
        return button;
    }
    private void touch(int action, float x, float y, long down, long time) throws Exception {
        MotionEvent event = MotionEvent.obtain(down, time, action, x, y, 0);
        try { assertEquals(false, call("onBrowserTouch", new Class<?>[]{MotionEvent.class}, event)); }
        finally { event.recycle(); }
    }
    @Before public void setup() throws Exception {
        activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        ((ExecutorService)get("mWorker")).shutdownNow();
        worker = new ControlledExecutor(); set("mWorker", worker);
        web = new ControlledWebView(activity);
        set("mWeb", web); set("mAddress", new EditText(activity)); set("mOrigin", new TextView(activity));
        set("mStatus", new TextView(activity));
        EditText goal = new EditText(activity); goal.setText("Compare shoes"); set("mGoalInput", goal);
        set("mGoal", "Compare shoes"); set("mHasTask", true); set("mForeground", true); set("mRunning", true);
        set("mBrowserEnabled", true);
        set("mDeadline", SystemClock.elapsedRealtime() + 100000); set("mApiKey", "sk-test-unused");
        BrowserTaskContract contract = new BrowserTaskContract("Compare shoes");
        assertTrue(contract.apply(new JSONObject().put("requirements", new JSONArray().put(new JSONObject()
                .put("id", "r1").put("quote", "Compare shoes").put("kind", "required"))), page(web.url), new JSONObject()));
        set("mContract", contract);
        call("configureBrowser", new Class<?>[]{});
    }
    @After public void tearDown() { activity.onDestroy(); }

    @Test public void navigationCancelsOldPlanAndRetainsTaskUntilFreshPageIsReady() throws Exception {
        BrowserTaskMemory memory = (BrowserTaskMemory)get("mMemory"); memory.observe(page(web.url));
        CompletableFuture<Void> task = new CompletableFuture<>(); set("mTask", task); set("mBusy", true);
        long oldSerial = (Long)get("mPlanSerial");
        web.url = "https://example.com/b";
        web.getWebViewClient().onPageStarted(web, web.url, null);
        assertTrue(task.isCancelled()); assertEquals(true, get("mRunning")); assertEquals(true, get("mLoading"));
        assertTrue((Long)get("mPlanSerial") > oldSerial);
        assertEquals("Compare shoes", get("mGoal")); assertEquals(1, memory.toJson().getJSONArray("pages").length());
        web.getWebViewClient().onPageFinished(web, web.url);
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(250));
        assertEquals(1, web.callbacks.size()); assertEquals(false, get("mLoading"));
    }
    @Test public void mainFrameAccessDenialHandsTheSameBrowserToTheUser() throws Exception {
        set("mLoading", true);
        WebResourceRequest request = new WebResourceRequest() {
            @Override public Uri getUrl() { return Uri.parse(web.url); }
            @Override public boolean isForMainFrame() { return true; }
            @Override public boolean isRedirect() { return false; }
            @Override public boolean hasGesture() { return false; }
            @Override public String getMethod() { return "GET"; }
            @Override public Map<String, String> getRequestHeaders() { return Collections.emptyMap(); }
        };
        WebResourceResponse response = new WebResourceResponse("text/html", "UTF-8", 403, "Forbidden",
                Collections.emptyMap(), new ByteArrayInputStream(new byte[0]));
        web.getWebViewClient().onReceivedHttpError(web, request, response);
        assertEquals(false, get("mRunning"));
        assertEquals(false, get("mLoading"));
        assertEquals("manual", get("mHumanMode"));
        assertNotNull(humanButton("입력 완료·계속"));
        assertTrue(((TextView)get("mHumanQuestion")).getText().toString().contains("직접 접속을 거부"));
    }
    @Test public void loadWatchdogStartsQueuedTaskWhenPageFinishedNeverArrives() throws Exception {
        set("mRunning", false);
        set("mBusy", false);
        set("mQueuedGoal", "Find public shoes");
        web.getWebViewClient().onPageStarted(web, web.url, null);
        assertEquals(true, get("mLoading"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofSeconds(31));
        assertEquals(false, get("mLoading"));
        assertEquals(true, get("mRunning"));
        assertEquals("Find public shoes", get("mGoal"));
        assertFalse(web.callbacks.isEmpty());
    }
    @Test public void spaHistoryChangeInvalidatesOldPlanWithoutStoppingTask() throws Exception {
        set("mLastObservation", page(web.url)); set("mBusy", true);
        CompletableFuture<Void> task = new CompletableFuture<>(); set("mTask", task);
        web.url = "https://example.com/a?filter=red";
        web.getWebViewClient().doUpdateVisitedHistory(web, web.url, false);
        assertTrue(task.isCancelled()); assertEquals(true, get("mRunning"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(300));
        assertEquals(1, web.callbacks.size());
    }
    @Test public void observationFromPreviousDocumentCannotReachPlanner() throws Exception {
        call("observe", new Class<?>[]{int.class}, 0);
        ValueCallback<String> stale = web.callbacks.get(0);
        web.url = "https://example.com/b";
        web.getWebViewClient().onPageStarted(web, web.url, null);
        stale.onReceiveValue(page("https://example.com/a").toString());
        assertEquals(0, get("mSteps")); assertNull(get("mTask")); assertEquals(true, get("mRunning"));
    }
    @Ignore("0.8 requires two stable complete observations") @Test public void waitsForDomQuietBeforeCountingPlannerStep() throws Exception {
        set("mSettleStarted", SystemClock.elapsedRealtime());
        call("observe", new Class<?>[]{int.class}, 0);
        JSONObject unready = page(web.url);
        unready.getJSONObject("readiness").put("domQuietMs", 10).put("pending", true);
        web.callbacks.get(0).onReceiveValue(unready.toString());
        assertEquals(0, get("mSteps")); assertNull(get("mTask")); assertEquals(false, get("mBusy"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(300));
        assertEquals(2, web.callbacks.size());
        // Stop at budget boundary to check readiness/observation without any real API traffic.
        set("mMaxSteps", 0);
        web.callbacks.get(1).onReceiveValue(page(web.url).toString());
        assertEquals(false, get("mRunning"));
        assertEquals(1, ((BrowserTaskMemory)get("mMemory")).toJson().getJSONArray("pages").length());
    }
    @Test public void dispatchSuccessRequiresSubsequentObservableChange() throws Exception {
        JSONObject before = page(web.url), action = new JSONObject().put("type", "click").put("targetId", "e1");
        before.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("role", "button").put("label", "Filters").put("expanded", false));
        call("pendingAction", new Class<?>[]{JSONObject.class, JSONObject.class}, action, before);
        assertEquals(false, call("verifyPendingAction", new Class<?>[]{JSONObject.class}, before));
        assertEquals(0, ((JSONArray)get("mHistory")).length());
        set("mActionStarted", SystemClock.elapsedRealtime() - 3000);
        assertEquals(true, call("verifyPendingAction", new Class<?>[]{JSONObject.class}, before));
        assertEquals("unknown", ((JSONArray)get("mHistory")).getJSONObject(0).getString("status"));
        assertEquals(1, get("mNoProgress"));
        call("pendingAction", new Class<?>[]{JSONObject.class, JSONObject.class}, action, before);
        JSONObject changed = page(web.url).put("text", "Filter applied: red shoes");
        changed.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("role", "button").put("label", "Filters").put("expanded", true));
        assertEquals(true, call("verifyPendingAction", new Class<?>[]{JSONObject.class}, changed));
        assertEquals("applied", ((JSONArray)get("mHistory")).getJSONObject(1).getString("status"));
        assertEquals(0, get("mNoProgress"));
    }
    @Test public void verifiedNativeFallbackDispatchesOneTouchWithoutStoppingAgent() throws Exception {
        FrameLayout host = new FrameLayout(activity);
        host.addView(web, new FrameLayout.LayoutParams(400, 800));
        host.measure(View.MeasureSpec.makeMeasureSpec(400, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(800, View.MeasureSpec.EXACTLY));
        host.layout(0, 0, 400, 800);
        assertEquals(400, web.getMeasuredWidth()); assertEquals(800, web.getMeasuredHeight());
        JSONObject before = page(web.url);
        before.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("role", "button").put("label", "Size guide").put("inViewport", true));
        BrowserTaskMemory memory = (BrowserTaskMemory)get("mMemory");
        memory.observe(before);
        memory.record(new JSONObject().put("type", "click").put("targetId", "e1"), "unknown", "DOM 클릭 후조건 없음");
        memory.feedback("NO_EFFECT");
        JSONObject fallback = new JSONObject().put("type", "click").put("targetId", "e1")
                .put("method", "native").put("message", "실제 터치로 사이즈 안내 열기");
        handle(before, fallback);
        assertEquals(1, web.callbacks.size());
        JSONObject preview = new JSONObject().put("ok", true).put("highlighted", true)
                .put("centerX", 100).put("centerY", 200).put("viewportWidth", 400).put("viewportHeight", 800);
        assertEquals(true, call("samePage", new Class<?>[]{int.class, long.class, String.class},
                (Integer)get("mGeneration"), (Long)get("mDocumentEpoch"), web.url));
        call("dispatchNativeTap", new Class<?>[]{int.class, long.class, String.class, JSONObject.class,
                        JSONObject.class, long.class, boolean.class, JSONObject.class},
                (Integer)get("mGeneration"), (Long)get("mDocumentEpoch"), web.url, before, fallback,
                (Long)get("mOperationId"), true, preview);
        assertEquals(java.util.Arrays.asList(MotionEvent.ACTION_DOWN, MotionEvent.ACTION_UP), web.touchActions);
        assertEquals(100f, web.touchXs.get(0), 0.01f); assertEquals(200f, web.touchYs.get(0), 0.01f);
        assertEquals(true, get("mRunning")); assertEquals(false, get("mNativeTapInProgress"));
        assertEquals("native", ((JSONObject)get("mPendingAction")).getString("method"));
    }
    @Ignore("0.8 foreground service keeps active work alive") @Test public void pauseKeepsPublicMemoryClearsKeyAndDoesNotAutomaticallyResume() throws Exception {
        BrowserTaskMemory memory = (BrowserTaskMemory)get("mMemory"); memory.observe(page(web.url));
        activity.onPause(); activity.onStop();
        assertEquals(false, get("mRunning")); assertEquals("", get("mApiKey"));
        assertEquals("Compare shoes", get("mGoal")); assertEquals(1, memory.toJson().getJSONArray("pages").length());
        activity.onResume(); assertEquals(false, get("mRunning"));
    }
    @Ignore("0.8 foreground service keeps active work alive") @Test public void stoppedGenerationIgnoresLatePageCallback() throws Exception {
        call("observe", new Class<?>[]{int.class}, 0);
        activity.onPause();
        web.callbacks.get(0).onReceiveValue(page(web.url).toString());
        assertEquals(0, get("mSteps")); assertEquals(false, get("mRunning")); assertNull(get("mTask"));
    }

    @Test public void resumeRetainsSourcesButResetsStallWatchdog() throws Exception {
        BrowserTaskMemory memory = (BrowserTaskMemory)get("mMemory"); memory.observe(page(web.url));
        JSONObject action = new JSONObject().put("type", "click");
        for (int i = 0; i < 3; i++) memory.record(action, "rejected", "Not actionable");
        assertTrue(memory.isStalled());
        call("stopRun", new Class<?>[]{String.class}, "Paused");
        call("beginRun", new Class<?>[]{boolean.class}, true);
        assertEquals(true, get("mRunning")); assertFalse(memory.isStalled());
        assertEquals(1, memory.toJson().getJSONArray("pages").length());
        assertEquals("Compare shoes", get("mGoal"));
    }

    @Ignore("0.8 ordinary input no longer asks approval") @Test public void confirmationShowsEntireNonSearchInputBeforeExecution() throws Exception {
        String text = new String(new char[1900]).replace('\0', 'a') + "END_OF_INPUT";
        JSONObject observation = page(web.url);
        observation.getJSONArray("elements").put(new JSONObject().put("id", "e1")
                .put("label", "Public draft").put("editable", true));
        JSONObject action = new JSONObject().put("type", "type").put("targetId", "e1").put("text", text);
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, observation, action);
        assertNull(get("mConfirmation"));
        assertEquals("approve", get("mHumanMode"));
        assertEquals(View.VISIBLE, ((View)get("mHumanPanel")).getVisibility());
        TextView message = (TextView)get("mHumanQuestion");
        assertTrue(message.getText().toString().contains(text));
        assertTrue(web.callbacks.isEmpty());
        assertFalse(action.optBoolean("approved"));
        humanButton("이 동작 실행").performClick();
        assertEquals(1, web.callbacks.size());
        assertTrue(action.getBoolean("approved"));
        assertEquals(text, ((JSONObject)get("mPendingAction")).getString("text"));
        assertEquals("", get("mHumanMode"));
    }

    private JSONObject supportedFinish() throws Exception {
        JSONObject source = new JSONObject().put("url", web.url).put("quote", "Red shoes cost 25 dollars");
        JSONObject candidate = new JSONObject().put("id", "c1").put("name", "Red shoes").put("identity", source)
                .put("checks", new JSONArray().put(new JSONObject().put("requirementId", "r1").put("status", "supported")
                        .put("evidence", new JSONArray().put(source))));
        assertTrue(((BrowserTaskContract)get("mContract")).apply(new JSONObject().put("candidates", new JSONArray().put(candidate)),
                page(web.url), new JSONObject()));
        return new JSONObject().put("type", "finish").put("message", "Compared public prices")
                .put("candidateIds", new JSONArray().put("c1")).put("evidence", new JSONArray().put(new JSONObject().put("url", web.url)
                        .put("quote", "Red shoes cost 25 dollars")));
    }
    @Test public void finalEvidenceIsVisibleEvenWithoutModelStateFacts() throws Exception {
        JSONObject action = supportedFinish();
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(false, get("mCompleted"));
        web.callbacks.get(web.callbacks.size() - 1).onReceiveValue(page(web.url).toString());
        assertEquals(false, get("mRunning")); assertEquals(true, get("mCompleted"));
        assertTrue(get("mLastResult").toString().contains(web.url));
        assertTrue(get("mLastResult").toString().contains("Red shoes cost 25 dollars"));
    }

    @Test public void verifiedImpossibleDeliveryReportsReasonAndStopsWithoutHandoffLoop() throws Exception {
        JSONObject observation = page(web.url).put("text", "This item cannot be shipped to South Korea.");
        JSONObject action = new JSONObject().put("type", "impossible").put("reason", "delivery_unavailable")
                .put("message", "이 상품은 한국으로 배송되지 않아 구매할 수 없습니다.")
                .put("evidence", new JSONArray().put(new JSONObject().put("url", web.url)
                        .put("quote", "This item cannot be shipped to South Korea.")));
        handle(observation, action);
        assertEquals(1, web.callbacks.size());
        web.callbacks.get(0).onReceiveValue(observation.toString());

        assertFalse((Boolean)get("mRunning"));
        assertTrue((Boolean)get("mCompleted"));
        assertTrue((Boolean)get("mTerminalImpossible"));
        assertEquals("", get("mHumanMode"));
        assertTrue(get("mLastResult").toString().contains("완료할 수 없어 중지"));
        assertTrue(get("mLastResult").toString().contains("배송 불가"));
        assertTrue(get("mLastResult").toString().contains("cannot be shipped"));
    }

    @Test public void unverifiedImpossibleProposalStopsRetryingButLabelsUncertainty() throws Exception {
        JSONObject action = new JSONObject().put("type", "impossible").put("reason", "no_feasible_option")
                .put("message", "조건에 맞는 선택지를 찾지 못했습니다.").put("metadataWarning", true);
        handle(page(web.url), action);

        assertFalse((Boolean)get("mRunning"));
        assertTrue((Boolean)get("mCompleted"));
        assertTrue((Boolean)get("mTerminalImpossible"));
        assertTrue(web.callbacks.isEmpty());
        assertTrue(get("mLastResult").toString().contains("완료 불가 여부를 확정하지 못했지만"));
        assertTrue(get("mLastResult").toString().contains("추가 확인 필요"));
    }

    @Test public void discardedFinishMetadataCannotUseOldSupportedContractToClaimCompletion() throws Exception {
        JSONObject action = supportedFinish().put("metadataWarning", true);
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(false, get("mCompleted")); assertEquals(false, get("mRunning"));
        assertTrue(get("mLastResult").toString().contains("부분 결과"));
        assertTrue(web.callbacks.isEmpty());
    }

    @Test public void unresolvedEarlierConditionUpdateCannotSilentlyBecomeCompleted() throws Exception {
        JSONObject action = supportedFinish();
        assertFalse(((BrowserTaskContract)get("mContract")).apply(new JSONObject().put("requirements", new JSONArray()),
                page(web.url), new JSONObject()));
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(false, get("mCompleted")); assertEquals(false, get("mRunning"));
        assertTrue(get("mLastResult").toString().contains("부분 결과"));
        assertTrue(web.callbacks.isEmpty());
    }
    @Test public void finishWithUnknownCandidateCannotBecomeCompleted() throws Exception {
        JSONObject action = new JSONObject().put("type", "finish").put("message", "Looks done")
                .put("candidateIds", new JSONArray().put("missing"));
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(false, get("mCompleted")); assertEquals(false, get("mRunning"));
        assertTrue(get("mLastResult").toString().contains("부분 결과"));
        assertTrue(get("mLastResult").toString().contains("Looks done"));
        assertTrue(web.callbacks.isEmpty());
        assertEquals(0, get("mNoProgress"));
    }

    @Test public void changingFrozenCriteriaPreservesCriteriaAndExecutesNextAction() throws Exception {
        JSONObject action = new JSONObject().put("type", "scroll").put("direction", "down")
                .put("task", new JSONObject().put("requirements", new JSONArray().put(new JSONObject()
                        .put("id", "r1").put("quote", "shoes").put("kind", "required"))));
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(1, web.callbacks.size());
        assertEquals(0, get("mNoProgress"));
        assertEquals("scroll", ((JSONObject)get("mPendingAction")).getString("type"));
        assertEquals("Compare shoes", ((BrowserTaskContract)get("mContract")).toJson().getJSONArray("requirements")
                .getJSONObject(0).getString("quote"));
    }

    @Test public void searchRunsEvenWhenConditionReferenceIsUnknown() throws Exception {
        JSONObject action = new JSONObject().put("type", "search").put("query", "shoe specifications")
                .put("purpose", "specification check").put("requirementIds", new JSONArray().put("invented"));
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals("search", ((JSONObject)get("mPendingAction")).getString("type"));
        assertEquals(1, worker.pending.size());
        assertEquals(true, get("mExpectedNavigation"));
        assertEquals(0, get("mNoProgress"));
        assertFalse(((BrowserTaskMemory)get("mMemory")).searched("shoe specifications"));
    }

    @Test public void inlineResearchKeepsCurrentDocumentWhileHostedLookupRuns() throws Exception {
        String original = web.url;
        JSONObject action = new JSONObject().put("type", "research")
                .put("query", "Loro Piana men jacket L numeric size Korea")
                .put("purpose", "L과 숫자 사이즈 대응 확인");
        handle(page(web.url), action);
        assertEquals(original, web.url);
        assertNull(get("mPendingAction"));
        assertEquals(1, worker.pending.size());
        assertFalse(((BrowserTaskMemory)get("mMemory")).searched(action.getString("query")));
        assertTrue(((TextView)get("mStatus")).getText().toString().contains("현재 상품 페이지를 유지"));
    }

    @Test public void firstSearchRunsWithoutAnyConditionTable() throws Exception {
        set("mContract", new BrowserTaskContract("Compare shoes"));
        JSONObject action = new JSONObject().put("type", "search").put("query", "shoe specifications");
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals("search", ((JSONObject)get("mPendingAction")).getString("type"));
        assertEquals(1, worker.pending.size());
        assertEquals(0, get("mNoProgress"));
        assertEquals(true, get("mRunning"));
        assertEquals("Compare shoes", get("mGoal"));
    }

    @Test public void invalidInitialConditionTableDoesNotBlockSearch() throws Exception {
        set("mContract", new BrowserTaskContract("Compare shoes"));
        JSONObject action = new JSONObject().put("type", "search").put("query", "shoe specifications")
                .put("task", new JSONObject().put("requirements", new JSONArray()));
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals("search", ((JSONObject)get("mPendingAction")).getString("type"));
        assertEquals(1, worker.pending.size());
        assertFalse(((BrowserTaskContract)get("mContract")).toJson().getBoolean("initialized"));
        assertEquals(0, get("mNoProgress"));
        assertFalse(((BrowserTaskMemory)get("mMemory")).isStalled());
    }

    @Test public void discardedOptionalMetadataDoesNotCountAsRepeatedActionFailure() throws Exception {
        JSONObject action = new JSONObject().put("type", "inspect").put("metadataWarning", true)
                .put("task", JSONObject.NULL);
        for (int i = 0; i < 3; i++) call("handleAction",
                new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(true, get("mRunning"));
        assertEquals(0, get("mNoProgress")); assertEquals(0, get("mRecoveries"));
        assertEquals(0, ((BrowserTaskMemory)get("mMemory")).toJson().getInt("consecutiveFailures"));
        assertFalse(((BrowserTaskMemory)get("mMemory")).isStalled());
    }

    @Test public void thirdIdenticalProposalTriggersDiagnosisBeforeTheRunCanStop() throws Exception {
        JSONObject observation = page(web.url);
        JSONObject action = new JSONObject().put("type", "scroll").put("direction", "down")
                .put("message", "결과를 더 찾기 위해 아래로 이동");
        set("mLastActionKey", BrowserObservationPolicy.actionKey(action, observation));
        set("mRepeatedAction", 2);
        int callbacks = web.callbacks.size();
        handle(observation, action);
        assertEquals(true, get("mRunning"));
        assertEquals(1, get("mLoopRepairs"));
        assertEquals(callbacks, web.callbacks.size());
        JSONObject feedback = ((BrowserTaskMemory)get("mMemory")).toJson().getJSONObject("feedback");
        assertEquals("NO_EFFECT", feedback.getString("code"));
        assertTrue(((TextView)get("mStatus")).getText().toString().contains("반복 원인을 분석"));
    }

    @Test public void finishWithoutConditionTableShowsUnverifiedResultWithoutRepairLoop() throws Exception {
        set("mContract", new BrowserTaskContract("Compare shoes"));
        JSONObject action = new JSONObject().put("type", "finish").put("message", "Collected public prices")
                .put("metadataWarning", true);
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(false, get("mCompleted")); assertEquals(false, get("mRunning"));
        assertTrue(get("mLastResult").toString().contains("부분 결과"));
        assertTrue(get("mLastResult").toString().contains("Collected public prices"));
        assertEquals(0, get("mRecoveries")); assertTrue(web.callbacks.isEmpty());
    }

    @Test public void failedSearchCanBeRetriedButObservedSearchIsRemembered() throws Exception {
        JSONObject action = new JSONObject().put("type", "search").put("query", "red shoe price").put("purpose", "price check");
        JSONObject before = page(web.url);
        call("pendingAction", new Class<?>[]{JSONObject.class, JSONObject.class}, action, before);
        set("mActionStarted", SystemClock.elapsedRealtime() - 3000);
        call("verifyPendingAction", new Class<?>[]{JSONObject.class}, before);
        BrowserTaskMemory memory = (BrowserTaskMemory)get("mMemory");
        assertFalse(memory.searched("red shoe price"));
        call("pendingAction", new Class<?>[]{JSONObject.class, JSONObject.class}, action, before);
        call("verifyPendingAction", new Class<?>[]{JSONObject.class}, page("https://www.google.com/search?q=red+shoe+price"));
        assertTrue(memory.searched("red shoe price"));
    }

    @Ignore("0.8 ordinary clicks no longer ask approval") @Test public void approvalIncludesProductContextForIdenticallyNamedButtons() throws Exception {
        JSONObject observation = page(web.url);
        observation.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("label", "Select")
                .put("group", "Trail Alpha").put("context", "Trail Alpha / red / 25 dollars"));
        JSONObject action = new JSONObject().put("type", "click").put("targetId", "e1");
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, observation, action);
        assertNull(get("mConfirmation"));
        assertEquals("approve", get("mHumanMode"));
        String message = ((TextView)get("mHumanQuestion")).getText().toString();
        assertTrue(message.contains("Trail Alpha")); assertTrue(message.contains("25 dollars"));
        assertTrue(web.callbacks.isEmpty());
    }

    @Test public void handoffCanReportPartialResultsEvenWhenTaskMetadataIsInvalid() throws Exception {
        JSONObject action = new JSONObject().put("type", "handoff").put("message", "Shipping cost could not be confirmed")
                .put("task", new JSONObject().put("requirements", new JSONArray()));
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        assertEquals(false, get("mRunning")); assertEquals(false, get("mCompleted"));
        assertTrue(get("mLastResult").toString().contains("Shipping cost"));
        assertEquals("manual", get("mHumanMode"));
        assertTrue(((TextView)get("mHumanQuestion")).getText().toString().contains("Shipping cost"));
        assertTrue(worker.pending.isEmpty());
    }

    @Ignore("0.8 question resume uses background notification flow") @Test public void publicQuestionWaitsForAnswerAndResumesOriginalTaskWithMemory() throws Exception {
        BrowserTaskMemory memory = (BrowserTaskMemory)get("mMemory");
        memory.observe(page(web.url));
        BrowserTaskContract contract = (BrowserTaskContract)get("mContract");
        CompletableFuture<Void> oldPlan = new CompletableFuture<>();
        set("mTask", oldPlan); set("mBusy", true);
        handle(page(web.url), new JSONObject().put("type", "ask_user").put("question", "어떤 색상을 원하시나요?"));
        assertEquals(false, get("mRunning"));
        assertTrue(oldPlan.isCancelled());
        assertEquals("answer", get("mHumanMode"));
        assertNull(get("mConfirmation"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(500));
        assertTrue(web.callbacks.isEmpty()); assertTrue(worker.pending.isEmpty());

        EditText answer = (EditText)get("mHumanAnswer");
        answer.setText("빨간색");
        humanButton("답변하고 계속").performClick();
        assertEquals(true, get("mRunning")); assertEquals("", get("mHumanMode"));
        assertEquals("", answer.getText().toString());
        assertEquals("Compare shoes", get("mGoal"));
        assertEquals("", ((EditText)get("mGoalInput")).getText().toString());
        assertSame(contract, get("mContract"));
        assertEquals(1, memory.toJson().getJSONArray("pages").length());
        JSONArray replies = (JSONArray)get("mUserReplies");
        assertEquals(1, replies.length());
        assertEquals("어떤 색상을 원하시나요?", replies.getJSONObject(0).getString("question"));
        assertEquals("빨간색", replies.getJSONObject(0).getString("answer"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(100));
        assertEquals(1, web.callbacks.size());
        web.callbacks.get(0).onReceiveValue(page(web.url).toString());
        assertEquals(1, worker.pending.size()); // Planner queued; no network operation is run in this fixture.
    }

    @Ignore("0.8 contact data uses encrypted local storage") @Test public void privateAnswerIsErasedAndKeptOutOfPlannerReplies() throws Exception {
        handle(page(web.url), new JSONObject().put("type", "ask_user").put("question", "어떤 색상을 원하시나요?"));
        EditText answer = (EditText)get("mHumanAnswer");
        answer.setText("alice@example.com");
        humanButton("답변하고 계속").performClick();
        assertEquals("", answer.getText().toString());
        assertEquals(0, ((JSONArray)get("mUserReplies")).length());
        assertEquals("manual", get("mHumanMode")); assertEquals(false, get("mRunning"));
        assertNull(get("mAnswerGrant")); assertTrue(worker.pending.isEmpty()); assertTrue(web.callbacks.isEmpty());
        assertEquals("Compare shoes", get("mGoal"));
    }

    @Test public void sensitivePageWaitsForManualInputWithoutSendingAnObservationToPlanner() throws Exception {
        call("observe", new Class<?>[]{int.class}, 0);
        web.callbacks.get(0).onReceiveValue(page(web.url).put("sensitive", true)
                .put("text", "Login verification").toString());
        assertEquals("manual", get("mHumanMode")); assertEquals(false, get("mRunning"));
        assertEquals(0, get("mSteps")); assertTrue(worker.pending.isEmpty());
        assertEquals(0, ((BrowserTaskMemory)get("mMemory")).toJson().getJSONArray("pages").length());
        assertEquals(0, ((JSONArray)get("mUserReplies")).length());
        assertNotNull(humanButton("입력 완료·계속"));
    }

    @Ignore("0.8 notification reply owns the answer lifecycle") @Test public void navigationInvalidatesPublicQuestionAndItsDetachedContinueButton() throws Exception {
        handle(page(web.url), new JSONObject().put("type", "ask_user").put("question", "어떤 색상을 원하시나요?"));
        EditText answer = (EditText)get("mHumanAnswer");
        answer.setText("빨간색");
        View staleContinue = humanButton("답변하고 계속");
        web.url = "https://example.com/b";
        web.getWebViewClient().onPageStarted(web, web.url, null);
        assertEquals("", answer.getText().toString()); assertNull(get("mHumanAnswer"));
        assertEquals("manual", get("mHumanMode"));
        staleContinue.performClick();
        assertEquals(0, ((JSONArray)get("mUserReplies")).length());
        assertNull(get("mAnswerGrant")); assertEquals(false, get("mRunning"));
        assertTrue(web.callbacks.isEmpty()); assertTrue(worker.pending.isEmpty());
    }

    @Ignore("0.8 contact data is intentionally persisted locally") @Test public void navigationErasesLocalPrivateFieldsAndInvalidatesTheirFillButton() throws Exception {
        call("requestManualInput", new Class<?>[]{String.class}, "배송 정보를 사이트에 입력하세요.");
        JSONArray fields = new JSONArray().put(new JSONObject().put("id", "e-address")
                .put("kind", "address").put("label", "배송 주소"));
        call("showHumanFields", new Class<?>[]{String.class, String.class, long.class, JSONArray.class},
                web.url, "doc-a", 0L, fields);
        List<?> inputs = (List<?>)get("mHumanFields");
        EditText address = (EditText)inputs.get(0);
        address.setText("서울시 테스트로 123");
        View staleFill = humanButton("이 사이트에 입력");
        web.url = "https://example.com/b";
        web.getWebViewClient().onPageStarted(web, web.url, null);
        assertEquals("", address.getText().toString()); assertTrue(inputs.isEmpty());
        assertEquals("manual", get("mHumanMode"));
        staleFill.performClick();
        assertTrue(web.callbacks.isEmpty()); assertTrue(worker.pending.isEmpty());
        assertEquals(0, ((JSONArray)get("mUserReplies")).length());
    }

    @Ignore("0.8 ordinary input no longer needs one-use approval") @Test public void explicitPublicAnswerGrantsOnlyOneMatchingFieldInput() throws Exception {
        JSONObject observation = page(web.url);
        JSONObject target = new JSONObject().put("id", "e1").put("tag", "input")
                .put("label", "Preferred color").put("editable", true);
        observation.getJSONArray("elements").put(target);
        handle(observation, new JSONObject().put("type", "ask_user").put("question", "어떤 색상을 원하시나요?")
                .put("targetId", "e1"));
        ((EditText)get("mHumanAnswer")).setText("빨간색");
        humanButton("답변하고 계속").performClick();
        assertNotNull(get("mAnswerGrant"));
        JSONObject wrongAnswer = new JSONObject().put("type", "type").put("targetId", "e1").put("text", "파란색");
        assertEquals(false, call("consumeAnswerGrant", new Class<?>[]{JSONObject.class, JSONObject.class, JSONObject.class},
                wrongAnswer, observation, target));
        assertNotNull(get("mAnswerGrant"));
        JSONObject input = new JSONObject().put("type", "type").put("targetId", "e1").put("text", "빨간색");
        handle(observation, input);
        assertTrue(input.getBoolean("approved")); assertNull(get("mAnswerGrant"));
        assertEquals(1, web.callbacks.size()); assertEquals("", get("mHumanMode"));
        JSONObject repeated = new JSONObject(input.toString()); repeated.remove("approved");
        handle(observation, repeated);
        assertEquals("approve", get("mHumanMode")); assertEquals(1, web.callbacks.size());
        assertFalse(repeated.optBoolean("approved"));
    }

    @Test public void answerGrantCannotApproveAChangedDocumentOrFieldLabel() throws Exception {
        JSONObject target = new JSONObject().put("id", "e1").put("tag", "input")
                .put("label", "Preferred color").put("editable", true);
        JSONObject observation = page(web.url).put("revision", 3);
        observation.getJSONArray("elements").put(target);
        handle(observation, new JSONObject().put("type", "ask_user").put("question", "어떤 색상을 원하시나요?")
                .put("targetId", "e1"));
        ((EditText)get("mHumanAnswer")).setText("red");
        humanButton("답변하고 계속").performClick();
        assertNotNull(get("mAnswerGrant"));
        JSONObject input = new JSONObject().put("text", "red");
        assertEquals(false, call("consumeAnswerGrant", new Class<?>[]{JSONObject.class, JSONObject.class, JSONObject.class},
                input, new JSONObject(observation.toString()).put("documentId", "doc-b"), target));
        assertEquals(false, call("consumeAnswerGrant", new Class<?>[]{JSONObject.class, JSONObject.class, JSONObject.class},
                input, observation, new JSONObject(target.toString()).put("label", "Public message")));
        assertEquals(false, call("consumeAnswerGrant", new Class<?>[]{JSONObject.class, JSONObject.class, JSONObject.class},
                input, new JSONObject(observation.toString()).put("revision", 4), target));
        web.url = "https://example.com/b";
        web.getWebViewClient().onPageStarted(web, web.url, null);
        assertNull(get("mAnswerGrant"));
    }

    @Ignore("0.8 ordinary input no longer needs one-use approval") @Test public void answerGrantRequiresFreshApprovalWhenFieldProductContextChanges() throws Exception {
        JSONObject target = new JSONObject().put("id", "e1").put("tag", "input")
                .put("label", "Preferred color").put("editable", true).put("context", "Trail Alpha");
        JSONObject observation = page(web.url);
        observation.getJSONArray("elements").put(target);
        handle(observation, new JSONObject().put("type", "ask_user").put("question", "어떤 색상을 원하시나요?")
                .put("targetId", "e1"));
        ((EditText)get("mHumanAnswer")).setText("red");
        humanButton("답변하고 계속").performClick();
        target.put("context", "Trail Beta");
        JSONObject input = new JSONObject().put("type", "type").put("targetId", "e1").put("text", "red");
        handle(observation, input);
        assertEquals("approve", get("mHumanMode"));
        assertTrue(((TextView)get("mHumanQuestion")).getText().toString().contains("Trail Beta"));
        assertFalse(input.optBoolean("approved")); assertTrue(web.callbacks.isEmpty());
    }

    @Ignore("0.8 ordinary clicks no longer render approval controls") @Test public void inlineApprovalCannotExecuteAfterUrlChanges() throws Exception {
        JSONObject observation = page(web.url);
        observation.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("label", "Select"));
        JSONObject action = new JSONObject().put("type", "click").put("targetId", "e1");
        handle(observation, action);
        View staleApprove = humanButton("이 동작 실행");
        assertTrue(web.callbacks.isEmpty());
        web.url = "https://example.com/b";
        web.getWebViewClient().onPageStarted(web, web.url, null);
        staleApprove.performClick();
        assertTrue(web.callbacks.isEmpty()); assertFalse(action.optBoolean("approved"));
        assertEquals(false, get("mRunning"));
    }

    @Test public void scrollingCancelsOldPlanAndRefreshesWithoutStoppingTask() throws Exception {
        BrowserTaskMemory memory = (BrowserTaskMemory)get("mMemory"); memory.observe(page(web.url));
        call("observe", new Class<?>[]{int.class}, 0);
        ValueCallback<String> staleObservation = web.callbacks.get(0);
        CompletableFuture<Void> oldPlan = new CompletableFuture<>(); set("mTask", oldPlan);
        long oldPlanSerial = (Long)get("mPlanSerial");
        long down = SystemClock.uptimeMillis();
        touch(MotionEvent.ACTION_DOWN, 10, 100, down, down);
        assertTrue(oldPlan.isCancelled()); assertTrue((Long)get("mPlanSerial") > oldPlanSerial);
        staleObservation.onReceiveValue(page(web.url).toString());
        assertTrue(worker.pending.isEmpty()); assertEquals(0, get("mSteps"));
        touch(MotionEvent.ACTION_MOVE, 10, 400, down, down + 50);
        call("observe", new Class<?>[]{int.class}, 0);
        assertEquals(1, web.callbacks.size()); // Never plan while the finger is still down.
        touch(MotionEvent.ACTION_UP, 10, 450, down, down + 100);
        assertEquals(true, get("mRunning")); assertEquals("Compare shoes", get("mGoal"));
        assertEquals(1, memory.toJson().getJSONArray("pages").length());
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400));
        assertEquals(2, web.callbacks.size());
        assertEquals(true, get("mRunning"));
    }

    @Test public void tappingStopsAutomationAndDoesNotScheduleAnotherPlan() throws Exception {
        CompletableFuture<Void> oldPlan = new CompletableFuture<>(); set("mTask", oldPlan); set("mBusy", true);
        long down = SystemClock.uptimeMillis();
        touch(MotionEvent.ACTION_DOWN, 10, 100, down, down);
        touch(MotionEvent.ACTION_UP, 10, 100, down, down + 50);
        assertTrue(oldPlan.isCancelled()); assertEquals(false, get("mRunning"));
        assertEquals("Compare shoes", get("mGoal"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(500));
        assertTrue(web.callbacks.isEmpty()); assertTrue(worker.pending.isEmpty());
    }

    @Test public void scrollingInvalidatesQueuedSearchNavigationWithoutRecordingFailure() throws Exception {
        handle(page(web.url), new JSONObject().put("type", "search").put("query", "red shoe specifications"));
        assertEquals(1, worker.pending.size());
        assertEquals("search", ((JSONObject)get("mPendingAction")).getString("type"));
        long navigation = (Long)get("mNavigationRequest");
        long down = SystemClock.uptimeMillis();
        touch(MotionEvent.ACTION_DOWN, 10, 100, down, down);
        assertTrue((Long)get("mNavigationRequest") > navigation);
        assertNull(get("mPendingAction"));
        assertEquals(false, get("mExpectedNavigation"));
        touch(MotionEvent.ACTION_MOVE, 10, 400, down, down + 50);
        touch(MotionEvent.ACTION_UP, 10, 450, down, down + 100);
        assertEquals(true, get("mRunning")); assertEquals(0, get("mNoProgress"));
        assertFalse(((BrowserTaskMemory)get("mMemory")).searched("red shoe specifications"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400));
        assertEquals(1, web.callbacks.size());
    }

    @Test public void observedDialogCloseDoesNotRequireAnotherUserConfirmation() throws Exception {
        JSONObject observation = page(web.url);
        observation.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("label", "닫기").put("dismiss", true));
        JSONObject action = new JSONObject().put("type", "click").put("targetId", "e1");
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, observation, action);
        assertEquals(1, web.callbacks.size());
        assertEquals("", get("mHumanMode"));
        assertTrue(((JSONObject)get("mPendingAction")).getBoolean("approved"));
    }

    @Ignore("0.8 native validation executes ordinary clicks automatically") @Test public void consentButtonCannotPretendToBeAnOptionalDialogClose() throws Exception {
        JSONObject observation = page(web.url);
        observation.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("label", "동의").put("dismiss", true));
        JSONObject action = new JSONObject().put("type", "click").put("targetId", "e1");
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, observation, action);
        assertTrue(web.callbacks.isEmpty());
        assertEquals("approve", get("mHumanMode"));
        assertFalse(action.optBoolean("approved"));
    }

    @Test public void changedPageDuringFinalModelResponseRequiresAnotherObservation() throws Exception {
        JSONObject action = new JSONObject().put("type", "finish").put("message", "Old price result");
        call("verifyFinish", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                0, 0L, web.url, page(web.url), action);
        web.callbacks.get(0).onReceiveValue(page(web.url).put("text", "Red shoes now cost 40 dollars").toString());
        assertEquals(false, get("mCompleted")); assertEquals(true, get("mRunning"));
        assertEquals("", get("mLastResult"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(400));
        assertEquals(2, web.callbacks.size());
    }

}
