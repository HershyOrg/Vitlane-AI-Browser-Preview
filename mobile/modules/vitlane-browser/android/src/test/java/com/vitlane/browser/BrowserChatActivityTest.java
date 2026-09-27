package com.vitlane.browser;

import static org.junit.Assert.*;

import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Rect;
import android.graphics.drawable.BitmapDrawable;
import android.net.Uri;
import android.net.http.SslCertificate;
import android.net.http.SslError;
import android.os.Looper;
import android.os.SystemClock;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.ValueCallback;
import android.webkit.DownloadListener;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.SslErrorHandler;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Button;
import android.widget.EditText;
import android.widget.ImageView;
import android.widget.TextView;
import java.lang.reflect.Constructor;
import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.net.HttpURLConnection;
import java.time.Duration;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.concurrent.AbstractExecutorService;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.TimeUnit;
import java.util.function.Consumer;
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
import org.robolectric.annotation.Implementation;
import org.robolectric.annotation.Implements;
import org.robolectric.annotation.LooperMode;
import org.robolectric.shadow.api.Shadow;
import org.robolectric.util.ReflectionHelpers;

/** Native routing/lifecycle fixtures. Network clients and DNS are replaced before any queued work runs. */
@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28, manifest = Config.NONE, instrumentedPackages = {"com.vitlane.browser"},
        shadows = {BrowserChatActivityTest.ChatFixture.class, BrowserChatActivityTest.NetworkFixture.class})
@LooperMode(LooperMode.Mode.PAUSED)
public class BrowserChatActivityTest {
    private VitlaneBrowserActivity activity;
    private ControlledExecutor worker;
    private ControlledWebView web;
    private String initialUrl;

    @Implements(value = BrowserChat.class, isInAndroidSdk = false)
    public static class ChatFixture {
        static int calls;
        static JSONArray messages;
        static boolean fail;
        @Implementation protected static JSONObject reply(String key, String model, JSONArray history,
                Consumer<HttpURLConnection> connection) throws Exception {
            calls++;
            messages = new JSONArray(history.toString());
            if (fail) throw new IllegalStateException("offline fixture failure");
            return new JSONObject().put("type", "chat").put("message", "Offline fixture answer");
        }
    }

    @Implements(value = BrowserUrlPolicy.class, isInAndroidSdk = false)
    public static class NetworkFixture {
        static boolean allowed;
        @Implementation protected static boolean isPublicNetworkUrl(String value) { return allowed; }
    }

    private static class ControlledExecutor extends AbstractExecutorService {
        final List<Runnable> pending = new ArrayList<>();
        private boolean stopped;
        @Override public void execute(Runnable task) { pending.add(task); }
        void runNext() { assertFalse("Missing queued worker operation", pending.isEmpty()); pending.remove(0).run(); }
        @Override public void shutdown() { stopped = true; }
        @Override public List<Runnable> shutdownNow() { stopped = true; List<Runnable> copy = new ArrayList<>(pending); pending.clear(); return copy; }
        @Override public boolean isShutdown() { return stopped; }
        @Override public boolean isTerminated() { return stopped; }
        @Override public boolean awaitTermination(long timeout, TimeUnit unit) { return stopped; }
    }

    private static class ControlledWebView extends WebView {
        String url = "https://example.com/public";
        final List<String> scripts = new ArrayList<>();
        final List<ValueCallback<String>> callbacks = new ArrayList<>();
        final List<String> navigations = new ArrayList<>();
        int draws;
        String measureSpecs = "not measured";
        ControlledWebView(VitlaneBrowserActivity context) { super(context); }
        @Override protected void onMeasure(int widthSpec, int heightSpec) {
            measureSpecs = View.MeasureSpec.toString(widthSpec) + " / " + View.MeasureSpec.toString(heightSpec);
            // Robolectric's placeholder WebView provider does not measure a real web viewport.
            setMeasuredDimension(View.MeasureSpec.getSize(widthSpec), View.MeasureSpec.getSize(heightSpec));
        }
        void attachMeasuredViewport() {
            // ShadowWebView leaves the frame at 0x0 even after its real parent completed onLayout.
            // Supply the frame an attached Android window would assign; capture still reads getWidth/Height.
            ReflectionHelpers.setField(this, "mLeft", 0);
            ReflectionHelpers.setField(this, "mTop", 0);
            ReflectionHelpers.setField(this, "mRight", getMeasuredWidth());
            ReflectionHelpers.setField(this, "mBottom", getMeasuredHeight());
        }
        @Override public String getUrl() { return url; }
        @Override public void loadUrl(String destination) { navigations.add(destination); url = destination; }
        @Override public void evaluateJavascript(String script, ValueCallback<String> callback) {
            scripts.add(script); callbacks.add(callback);
        }
        @Override public void draw(Canvas canvas) { draws++; canvas.drawColor(Color.GREEN); }
    }

    private Object get(String name) throws Exception {
        Field field = VitlaneBrowserActivity.class.getDeclaredField(name); field.setAccessible(true); return field.get(activity);
    }
    private void set(String name, Object value) throws Exception {
        Field field = VitlaneBrowserActivity.class.getDeclaredField(name); field.setAccessible(true); field.set(activity, value);
    }
    private Object call(String name, Class<?>[] types, Object... args) throws Exception {
        Method method = VitlaneBrowserActivity.class.getDeclaredMethod(name, types); method.setAccessible(true); return method.invoke(activity, args);
    }
    private void call(String name) throws Exception { call(name, new Class<?>[]{}); }
    private void enabled(boolean value) throws Exception {
        call("setBrowserEnabled", new Class<?>[]{boolean.class}, value);
        layoutChat(360, 640);
    }
    private void visible(boolean value) throws Exception { call("setPageVisible", new Class<?>[]{boolean.class}, value); }
    private EditText composer() throws Exception { return (EditText)get("mGoalInput"); }
    private BrowserChatTimeline timeline() throws Exception { return (BrowserChatTimeline)get("mTimeline"); }
    private JSONArray history() throws Exception { return (JSONArray)get("mChatMessages"); }
    private void send(String message) throws Exception { composer().setText(message); call("sendMessage"); }
    private JSONObject page() throws Exception {
        return new JSONObject().put("url", web.url).put("documentId", "document-a").put("title", "Public product")
                .put("text", "Public product costs 25 dollars").put("sensitive", false).put("screenshotSafe", true)
                .put("localPreviewSafe", true).put("revision", 1)
                .put("elements", new JSONArray()).put("readiness", new JSONObject()
                        .put("readyState", "complete").put("domQuietMs", 800).put("pending", false));
    }
    private View pageMessage() throws Exception {
        return (View)call("addPageMessage", new Class<?>[]{String.class, JSONObject.class}, "Public product result", page());
    }
    private static <T extends View> List<T> descendants(View root, Class<T> kind) {
        List<T> result = new ArrayList<>();
        if (kind.isInstance(root)) result.add(kind.cast(root));
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup)root).getChildCount(); i++)
            result.addAll(descendants(((ViewGroup)root).getChildAt(i), kind));
        return result;
    }
    private static String text(View root) {
        StringBuilder result = new StringBuilder();
        for (TextView view : descendants(root, TextView.class)) result.append(view.getText()).append('\n');
        return result.toString();
    }
    private String geometry() throws Exception {
        StringBuilder result = new StringBuilder("density=").append(activity.getResources().getDisplayMetrics().density)
                .append(" specs=").append(web.measureSpecs);
        for (View node = web; node != null; node = node.getParent() instanceof View ? (View)node.getParent() : null) {
            result.append("\n").append(node.getClass().getSimpleName()).append(" visible=").append(node.getVisibility())
                    .append(" actual=").append(node.getWidth()).append('x').append(node.getHeight())
                    .append(" measured=").append(node.getMeasuredWidth()).append('x').append(node.getMeasuredHeight())
                    .append(" params=").append(node.getLayoutParams());
        }
        return result.toString();
    }
    private static void assertShadowInstalled(Class<?> type, Class<?> shadowType) throws Exception {
        Constructor<?> constructor = type.getDeclaredConstructor(); constructor.setAccessible(true);
        assertTrue("Offline shadow was not installed for " + type.getName(), shadowType.isInstance(Shadow.extract(constructor.newInstance())));
    }
    private static WebResourceRequest requestFor(String url) {
        return new WebResourceRequest() {
            @Override public Uri getUrl() { return Uri.parse(url); }
            @Override public boolean isForMainFrame() { return true; }
            @Override public boolean isRedirect() { return false; }
            @Override public boolean hasGesture() { return false; }
            @Override public String getMethod() { return "GET"; }
            @Override public Map<String, String> getRequestHeaders() { return Collections.emptyMap(); }
        };
    }
    private static RenderProcessGoneDetail crash() {
        return new RenderProcessGoneDetail() {
            @Override public boolean didCrash() { return true; }
            @Override public int rendererPriorityAtExit() { return WebView.RENDERER_PRIORITY_IMPORTANT; }
        };
    }
    private static void assertInside(ViewGroup root, View child) {
        Rect bounds = new Rect(); child.getDrawingRect(bounds); root.offsetDescendantRectToMyCoords(child, bounds);
        assertTrue("Control must have a visible measured area", bounds.width() > 0 && bounds.height() > 0);
        assertTrue("Control extends beyond the measured chat root: " + bounds,
                bounds.left >= 0 && bounds.top >= 0 && bounds.right <= root.getWidth() && bounds.bottom <= root.getHeight());
    }
    private ViewGroup layoutChat(int width, int height) {
        ViewGroup root = (ViewGroup)((ViewGroup)activity.findViewById(android.R.id.content)).getChildAt(0);
        // This Activity is not attached to a Robolectric window traversal. Force the same complete
        // measure/layout pass that Android performs after the browser pane changes GONE -> VISIBLE.
        forceLayoutTree(root);
        root.measure(View.MeasureSpec.makeMeasureSpec(width, View.MeasureSpec.EXACTLY),
                View.MeasureSpec.makeMeasureSpec(height, View.MeasureSpec.EXACTLY));
        root.layout(0, 0, width, height);
        if (web != null && web.getMeasuredWidth() > 0 && web.getMeasuredHeight() > 0) web.attachMeasuredViewport();
        return root;
    }

    private static void forceLayoutTree(View view) {
        view.forceLayout();
        if (view instanceof ViewGroup) for (int i = 0; i < ((ViewGroup)view).getChildCount(); i++)
            forceLayoutTree(((ViewGroup)view).getChildAt(i));
    }

    @Before public void setup() throws Exception {
        assertShadowInstalled(BrowserChat.class, ChatFixture.class);
        assertShadowInstalled(BrowserUrlPolicy.class, NetworkFixture.class);
        ChatFixture.calls = 0; ChatFixture.messages = null; ChatFixture.fail = false;
        NetworkFixture.allowed = true;
        activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        ((ExecutorService)get("mWorker")).shutdownNow();
        worker = new ControlledExecutor(); set("mWorker", worker);
        activity.onCreate(null);
        activity.onResume();
        WebView original = (WebView)get("mWeb");
        initialUrl = original.getUrl();
        ViewGroup parent = (ViewGroup)original.getParent();
        int index = parent.indexOfChild(original);
        ViewGroup.LayoutParams params = original.getLayoutParams();
        parent.removeView(original); original.destroy();
        web = new ControlledWebView(activity); parent.addView(web, index, params); set("mWeb", web);
        web.layout(0, 0, 320, 480);
        call("configureBrowser");
        set("mApiKey", "sk-offline-fixture-only");
    }

    @After public void cleanup() throws Exception {
        if (activity != null && !(Boolean)get("mDestroyed")) activity.onDestroy();
    }

    @Ignore("0.8 browser is available by default") @Test public void opensAsPlainChatWithoutStartingBrowserOrNetwork() throws Exception {
        assertFalse((Boolean)get("mBrowserEnabled")); assertFalse((Boolean)get("mPageVisible"));
        assertFalse((Boolean)get("mRunning")); assertFalse((Boolean)get("mChatBusy"));
        assertNull(initialUrl); assertTrue(worker.pending.isEmpty()); assertTrue(web.callbacks.isEmpty());
        assertEquals(View.GONE, ((View)get("mBrowserPane")).getVisibility());
        assertFalse(((View)get("mPageToggle")).isEnabled());
        assertTrue(text(timeline().getView()).contains("무엇을 도와드릴까요?"));
        visible(true);
        assertFalse((Boolean)get("mPageVisible")); assertTrue(web.callbacks.isEmpty());
    }

    @Ignore("0.8 removed browser opt-in") @Test public void enablingBrowserLoadsItsStartPageOnlyAfterExplicitOptIn() throws Exception {
        web.url = null;
        enabled(true);
        assertTrue((Boolean)get("mBrowserEnabled")); assertEquals(1, worker.pending.size());
        assertTrue(web.navigations.isEmpty());
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, web.navigations.size()); assertEquals("https://www.google.com/", web.navigations.get(0));
        assertEquals(0, ChatFixture.calls);
    }

    @Test public void sourcedSearchAnswerCompletesInChatWithoutOpeningOrRunningBrowser() throws Exception {
        long serial = 42L;
        String sourceUrl = "https://example.com/public-answer";
        JSONObject research = BrowserResearchPlanner.empty("")
                .put("searched", true).put("completionMode", "answer")
                .put("answer", "검색 결과만으로 확인한 공개 정보입니다.")
                .put("answerSourceUrls", new JSONArray().put(sourceUrl))
                .put("sources", new JSONArray().put(new JSONObject()
                        .put("url", sourceUrl).put("title", "공개 검색 출처")));
        set("mRouteSerial", serial);
        set("mRouting", true);

        call("completeBrowserResearch", new Class<?>[]{long.class, String.class, JSONObject.class, boolean.class},
                serial, "공개 정보를 찾아줘", research, true);

        assertFalse((Boolean)get("mRouting"));
        assertFalse((Boolean)get("mRunning"));
        assertEquals("", get("mQueuedGoal"));
        assertTrue(web.navigations.isEmpty());
        assertTrue(web.callbacks.isEmpty());
        assertTrue(text(timeline().getView()).contains("검색 결과만으로 확인한 공개 정보입니다."));
        assertTrue(text(timeline().getView()).contains("공개 검색 출처"));
        assertTrue(history().getJSONObject(history().length() - 1).getString("content").contains(sourceUrl));
    }

    @Test public void preSearchClarificationUsesChatAndResumesResearchWithTheAnswer() throws Exception {
        long serial = 77L;
        call("addUserMessage", new Class<?>[]{String.class}, "스파오에서 자켓 하나 사줘");
        JSONArray conversation = new JSONArray(history().toString());
        JSONObject decision = new JSONObject().put("mode", "browser").put("reason", "구매 요청")
                .put("needsLocation", false).put("clarificationNeeded", true)
                .put("clarificationQuestion", "남성용, 여성용, 공용 중 어느 쪽을 찾을까요?")
                .put("clarificationOptions", new JSONArray().put("남성용").put("여성용").put("공용"));
        set("mRouteSerial", serial);
        set("mRouting", true);
        call("requestPreSearchClarification",
                new Class<?>[]{long.class, String.class, JSONArray.class, JSONObject.class},
                serial, "스파오에서 자켓 하나 사줘", conversation, decision);

        assertEquals("answer", get("mHumanMode"));
        assertTrue(text(timeline().getView()).contains("검색 전에 한 가지만"));
        assertTrue(text(timeline().getView()).contains("여성용"));
        send("여성용");
        assertEquals("", get("mHumanMode"));
        assertTrue((Boolean)get("mRouting"));
        assertEquals(1, worker.pending.size());
        assertEquals("여성용", history().getJSONObject(history().length() - 1).getString("content"));
        assertTrue(web.navigations.isEmpty());
    }

    @Test public void manualPublicHttpAddressIsUpgradedAndOpenedAsHttps() throws Exception {
        call("navigate", new Class<?>[]{String.class, boolean.class}, "http://example.com/catalog", false);
        assertEquals(1, worker.pending.size());
        worker.runNext();
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(Collections.singletonList("https://example.com/catalog"), web.navigations);
    }

    @Test public void pageVisibilityDoesNotCancelOrRestartActiveBrowserWork() throws Exception {
        enabled(true);
        set("mRunning", true); set("mBusy", true); set("mDeadline", SystemClock.elapsedRealtime() + 100000);
        CompletableFuture<Void> plan = new CompletableFuture<>(); set("mTask", plan);
        int generation = (Integer)get("mGeneration"); long serial = (Long)get("mPlanSerial");
        visible(true);
        assertTrue((Boolean)get("mRunning")); assertFalse(plan.isCancelled());
        assertEquals(View.GONE, timeline().getView().getVisibility());
        visible(false);
        assertEquals(View.VISIBLE, timeline().getView().getVisibility());
        assertEquals(View.VISIBLE, ((View)get("mBrowserPane")).getVisibility());
        assertEquals(View.IMPORTANT_FOR_ACCESSIBILITY_NO_HIDE_DESCENDANTS,
                ((View)get("mBrowserPane")).getImportantForAccessibility());
        assertEquals(generation, get("mGeneration")); assertEquals(serial, get("mPlanSerial"));
        assertTrue((Boolean)get("mBusy")); assertFalse(plan.isCancelled()); assertTrue(web.callbacks.isEmpty());
    }

    @Test public void backFromPageReturnsToChatWithoutCancellingThePlan() throws Exception {
        enabled(true); set("mRunning", true); set("mDeadline", SystemClock.elapsedRealtime() + 100000);
        CompletableFuture<Void> plan = new CompletableFuture<>(); set("mTask", plan);
        visible(true); activity.onBackPressed();
        assertFalse((Boolean)get("mPageVisible")); assertTrue((Boolean)get("mRunning"));
        assertFalse(plan.isCancelled()); assertFalse(activity.isFinishing());
    }

    @Test public void browserOffCancelsPlanAndQueuedNavigationBeforeItCanLoad() throws Exception {
        enabled(true); set("mRunning", true); set("mDeadline", SystemClock.elapsedRealtime() + 100000);
        call("navigate", new Class<?>[]{String.class, boolean.class}, "https://example.com/queued", true);
        CompletableFuture<Void> plan = new CompletableFuture<>(); set("mTask", plan);
        long navigation = (Long)get("mNavigationRequest");
        enabled(false);
        assertFalse((Boolean)get("mBrowserEnabled")); assertFalse((Boolean)get("mRunning"));
        assertTrue(plan.isCancelled()); assertTrue((Long)get("mNavigationRequest") > navigation);
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertTrue(web.navigations.isEmpty()); assertTrue(web.callbacks.isEmpty());
        assertNull(get("mWeb")); assertNull(web.getParent());
        assertEquals(View.GONE, ((View)get("mBrowserPane")).getVisibility());
    }

    @Test public void reenablingBrowserCreatesANewEngineForTheRememberedPublicUrl() throws Exception {
        enabled(true); String remembered = web.url; enabled(false);
        assertNull(get("mWeb"));
        enabled(true);
        WebView replacement = (WebView)get("mWeb");
        assertNotNull(replacement); assertNotSame(web, replacement);
        assertEquals(1, worker.pending.size());
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(remembered, Shadows.shadowOf(replacement).getLastLoadedUrl());
        assertFalse((Boolean)get("mRunning")); assertFalse((Boolean)get("mChatBusy"));
    }

    @Ignore("0.8 reasoning router runs before chat") @Test public void plainChatSendsOnlyConversationAndNeverObservesAHiddenPage() throws Exception {
        set("mLastObservation", page().put("text", "UNRELATED_PAGE_SENTINEL"));
        set("mGoal", "UNRELATED_BROWSER_GOAL");
        send("Explain this simply");
        assertTrue((Boolean)get("mChatBusy")); assertFalse((Boolean)get("mRunning"));
        assertEquals("", composer().getText().toString()); assertEquals(1, worker.pending.size());
        assertTrue(web.callbacks.isEmpty());
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, ChatFixture.calls); assertFalse((Boolean)get("mChatBusy"));
        assertEquals(1, ChatFixture.messages.length());
        assertEquals("Explain this simply", ChatFixture.messages.getJSONObject(0).getString("content"));
        assertFalse(ChatFixture.messages.toString().contains("UNRELATED_"));
        assertEquals("Offline fixture answer", history().getJSONObject(1).getString("content"));
        assertTrue(text(timeline().getView()).contains("Offline fixture answer")); assertTrue(web.callbacks.isEmpty());
    }

    @Ignore("0.8 reasoning router runs before chat") @Test public void ordinaryChatFailureRestoresControlsAndExplicitRetryCanSucceed() throws Exception {
        ChatFixture.fail = true; send("Explain this simply");
        assertFalse(((View)get("mStart")).isEnabled()); assertFalse(composer().isEnabled());
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertFalse((Boolean)get("mChatBusy")); assertTrue(((View)get("mStart")).isEnabled()); assertTrue(composer().isEnabled());
        assertFalse(((View)get("mStop")).isEnabled()); assertTrue(worker.pending.isEmpty());
        assertTrue(text(timeline().getView()).contains("재시도"));
        ChatFixture.fail = false; send("Explain this simply"); worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(2, ChatFixture.calls); assertFalse((Boolean)get("mChatBusy"));
        assertTrue(text(timeline().getView()).contains("Offline fixture answer")); assertTrue(web.callbacks.isEmpty());
    }

    @Ignore("0.8 removed browser-off mode") @Test public void lateErrorsFromDisabledOldWebViewCannotCancelANewPlainChat() throws Exception {
        enabled(true); WebViewClient oldClient = web.getWebViewClient(); enabled(false);
        send("Continue plain chat"); long serial = (Long)get("mChatSerial");
        oldClient.onReceivedError(web, requestFor(web.url), null);
        SslErrorHandler sslHandler = Shadow.newInstanceOf(SslErrorHandler.class);
        // No certificate parsing/provider dependencies are needed: this callback uses only its URL.
        SslCertificate certificate = Shadow.newInstanceOf(SslCertificate.class);
        oldClient.onReceivedSslError(web, sslHandler, new SslError(SslError.SSL_UNTRUSTED, certificate, web.url));
        assertTrue(Shadows.shadowOf(sslHandler).wasCancelCalled()); assertFalse(Shadows.shadowOf(sslHandler).wasProceedCalled());
        assertTrue(oldClient.onRenderProcessGone(web, crash()));
        assertTrue((Boolean)get("mChatBusy")); assertEquals(serial, get("mChatSerial")); assertFalse(activity.isFinishing());
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, ChatFixture.calls); assertTrue(text(timeline().getView()).contains("Offline fixture answer"));
    }

    @Ignore("0.8 removed browser-off mode") @Test public void inactiveBrowserRendererCrashLeavesPlainChatAlive() throws Exception {
        send("Keep this conversation"); long serial = (Long)get("mChatSerial");
        assertTrue(web.getWebViewClient().onRenderProcessGone(web, crash()));
        assertNull(get("mWeb")); assertFalse((Boolean)get("mBrowserEnabled"));
        assertTrue((Boolean)get("mChatBusy")); assertEquals(serial, get("mChatSerial")); assertFalse(activity.isFinishing());
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, ChatFixture.calls); assertTrue(text(timeline().getView()).contains("Offline fixture answer"));
    }

    @Ignore("0.8 removed browser-off mode") @Test public void oldDownloadNotificationCannotCancelPlainChatAfterBrowserOff() throws Exception {
        enabled(true); DownloadListener oldDownload = Shadows.shadowOf(web).getDownloadListener();
        assertNotNull(oldDownload); enabled(false); send("Continue plain chat");
        long serial = (Long)get("mChatSerial");
        oldDownload.onDownloadStart("https://example.com/file", "fixture", "attachment", "application/octet-stream", 100);
        assertTrue((Boolean)get("mChatBusy")); assertEquals(serial, get("mChatSerial"));
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertTrue(text(timeline().getView()).contains("Offline fixture answer"));
    }

    @Ignore("0.8 reasoning router owns dispatch") @Test public void queuedBlockedNetworkNoticeCannotCancelANewerPlainChat() throws Exception {
        enabled(true); WebViewClient oldClient = web.getWebViewClient();
        NetworkFixture.allowed = false;
        assertNotNull(oldClient.shouldInterceptRequest(web, requestFor(web.url)));
        enabled(false); send("Continue after browser closes");
        long serial = (Long)get("mChatSerial");
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertTrue((Boolean)get("mChatBusy")); assertEquals(serial, get("mChatSerial"));
        assertTrue(oldClient.shouldOverrideUrlLoading(web, requestFor("https://example.com/stale")));
        worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, ChatFixture.calls); assertTrue(text(timeline().getView()).contains("Offline fixture answer"));
    }

    @Ignore("0.8 reasoning router owns dispatch") @Test public void browserOnRoutesTheSameComposerToAgentAndPreservesTheTaskGoal() throws Exception {
        call("addUserMessage", new Class<?>[]{String.class}, "I prefer red shoes");
        call("addAssistantMessage", new Class<?>[]{String.class}, "We can compare red public options");
        enabled(true); send("Compare public shoes");
        assertTrue((Boolean)get("mRunning")); assertFalse((Boolean)get("mChatBusy"));
        assertEquals("Compare public shoes", get("mGoal")); assertEquals("", composer().getText().toString());
        assertTrue(text(timeline().getView()).contains("Compare public shoes"));
        JSONArray context = (JSONArray)get("mBrowserConversation");
        assertEquals(2, context.length());
        assertEquals("I prefer red shoes", context.getJSONObject(0).getString("content"));
        assertEquals("We can compare red public options", context.getJSONObject(1).getString("content"));
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(Duration.ofMillis(120));
        assertEquals(1, web.callbacks.size()); assertTrue(worker.pending.isEmpty()); assertEquals(0, ChatFixture.calls);
    }

    @Test public void preSearchAnswerComposerStaysEditableWhileRouterIsPending() throws Exception {
        set("mRunning", false);
        set("mRouting", true);
        set("mHumanMode", "answer");
        call("updateControls");
        assertTrue(composer().isEnabled());
        assertTrue(((Button)get("mStart")).isEnabled());
        assertEquals("답변 보내기", ((Button)get("mStart")).getText().toString());
    }

    @Ignore("0.8 notification reply owns clarification lifecycle") @Test public void clarificationUsesTheExistingComposerAndKeepsTheOriginalBrowserGoal() throws Exception {
        enabled(true); send("Compare public shoes");
        EditText input = composer();
        call("requestUserAnswer", new Class<?>[]{JSONObject.class, JSONObject.class},
                new JSONObject().put("type", "ask_user").put("question", "어떤 색상을 원하시나요?"), page());
        assertSame(input, get("mHumanAnswer")); assertEquals("answer", get("mHumanMode"));
        assertEquals(View.VISIBLE, input.getVisibility()); assertTrue(input.isEnabled());
        assertTrue(descendants(timeline().getView(), EditText.class).isEmpty());
        assertTrue(text(timeline().getView()).contains("어떤 색상을 원하시나요?"));
        send("빨간색");
        assertEquals("Compare public shoes", get("mGoal")); assertTrue((Boolean)get("mRunning"));
        assertFalse((Boolean)get("mChatBusy")); assertEquals("", get("mHumanMode"));
        assertEquals("", composer().getText().toString());
        assertEquals("빨간색", ((JSONArray)get("mUserReplies")).getJSONObject(0).getString("answer"));
        assertTrue(text(timeline().getView()).contains("빨간색")); assertEquals(0, ChatFixture.calls);
    }

    @Ignore("0.8 private contact data uses local vault") @Test public void privateClarificationAnswerStaysOutOfConversationAndModelReplies() throws Exception {
        enabled(true); send("Compare public shoes");
        call("requestUserAnswer", new Class<?>[]{JSONObject.class, JSONObject.class},
                new JSONObject().put("question", "선호하는 조건이 있나요?"), page());
        send("person@example.com");
        assertEquals(0, ((JSONArray)get("mUserReplies")).length());
        assertFalse(history().toString().contains("person@example.com"));
        assertFalse(text(timeline().getView()).contains("person@example.com"));
        assertEquals("manual", get("mHumanMode")); assertFalse((Boolean)get("mRunning"));
        assertTrue(worker.pending.isEmpty());
    }

    @Test public void compactAndKeyboardSizedLayoutsKeepTheSharedComposerReachable() throws Exception {
        enabled(true); send("Compare public shoes");
        String question = "선호 조건을 알려 주세요. " + new String(new char[350]).replace('\0', '가');
        call("requestUserAnswer", new Class<?>[]{JSONObject.class, JSONObject.class},
                new JSONObject().put("question", question), page());
        for (int height : new int[] {640, 400}) {
            ViewGroup root = layoutChat(360, height);
            assertInside(root, composer()); assertInside(root, (View)get("mStart"));
            assertTrue("Chat must retain a positive scrolling viewport", timeline().getView().getHeight() > 0);
            assertEquals(View.VISIBLE, composer().getVisibility()); assertTrue(composer().isEnabled());
            assertTrue(descendants(timeline().getView(), View.class).contains((View)get("mHumanPanel")));
            assertTrue("Question and inline actions must be scrollable above the composer",
                    timeline().getView().getChildAt(0).getHeight() >= timeline().getView().getHeight());
        }
    }

    @Test public void screenshotRequiresASecondFreshObservationAndStaysLocalToTheTimeline() throws Exception {
        enabled(true); View bubble = pageMessage();
        assertTrue("Fixture WebView must have a measured viewport: " + geometry(), web.getWidth() > 1 && web.getHeight() > 1);
        assertEquals(0, web.draws); assertTrue(descendants(bubble, ImageView.class).isEmpty());
        assertEquals(1, web.callbacks.size()); web.callbacks.get(0).onReceiveValue(page().toString());
        assertEquals("Fresh page must be drawn", 1, web.draws);
        assertEquals("Drawn image must attach to its message", 1, descendants(bubble, ImageView.class).size());
        assertEquals("Public product result", history().getJSONObject(0).getString("content"));
        assertEquals(2, history().getJSONObject(0).length());
        assertTrue(text(bubble).contains("현재 페이지는 이 화면과 다를 수 있어요."));
        descendants(bubble, ImageView.class).get(0).performClick();
        assertFalse((Boolean)get("mPageVisible")); assertTrue(web.navigations.isEmpty());
    }

    @Test public void plannerVisionCaptureRequiresTheStrictPixelPrivacyFlagAndIsNeverPersisted() throws Exception {
        enabled(true);
        String image = (String)call("capturePlannerScreenshot", new Class<?>[]{JSONObject.class}, page());
        assertNotNull(image);
        assertTrue(image.startsWith("data:image/jpeg;base64,"));
        assertEquals(1, web.draws);
        assertNull(call("capturePlannerScreenshot", new Class<?>[]{JSONObject.class}, page().put("screenshotSafe", false)));
        assertEquals(1, web.draws);
        assertFalse(history().toString().contains("data:image"));
    }

    @Test public void completingASecretStepDoesNotPermanentlyHideLaterPublicScreenshots() throws Exception {
        enabled(true);
        call("rememberSecretRedactions", new Class<?>[]{JSONArray.class},
                new JSONArray().put(new JSONObject().put("value", "local-secret-sentinel")));
        View bubble = pageMessage();
        assertEquals(1, web.callbacks.size());
        web.callbacks.get(0).onReceiveValue(page().toString());
        assertEquals(1, web.draws);
        assertEquals(1, descendants(bubble, ImageView.class).size());
        assertFalse(history().toString().contains("local-secret-sentinel"));
    }

    @Test public void slowSourceImageDownloadCannotDelayTheVisibleScreenshot() throws Exception {
        enabled(true);
        JSONObject observed = page().put("images", new JSONArray().put(new JSONObject()
                .put("id", "image-a").put("src", "https://images.example.com/product.png")
                .put("alt", "Public product")));
        View bubble = (View)call("addPageMessage", new Class<?>[]{String.class, JSONObject.class},
                "Public product result", observed);
        assertEquals(1, web.callbacks.size());
        web.callbacks.get(0).onReceiveValue(observed.toString());
        assertEquals(1, web.draws);
        assertEquals(1, descendants(bubble, ImageView.class).size());
        assertEquals("Source download should still be queued", 1, worker.pending.size());
    }

    @Test public void changedOrSensitivePageCannotBeAttachedAsAPublicResultScreenshot() throws Exception {
        enabled(true); View changed = pageMessage();
        web.callbacks.get(0).onReceiveValue(page().put("text", "Different page content").toString());
        assertEquals(0, web.draws); assertTrue(descendants(changed, ImageView.class).isEmpty());
        View sensitive = pageMessage();
        web.callbacks.get(1).onReceiveValue(page().put("sensitive", true).toString());
        assertEquals(0, web.draws); assertTrue(descendants(sensitive, ImageView.class).isEmpty());
        int callbacks = web.callbacks.size();
        call("attachPageSnapshot", new Class<?>[]{View.class, JSONObject.class}, sensitive, page().put("sensitive", true));
        assertEquals(callbacks, web.callbacks.size());
    }

    @Test public void localPreviewDoesNotDependOnTheStrictPixelExportFlagButRequiresExplicitPermission() throws Exception {
        enabled(true); View bubble = pageMessage();
        web.callbacks.get(0).onReceiveValue(page().put("screenshotSafe", false).toString());
        assertEquals(1, web.draws); assertEquals(1, descendants(bubble, ImageView.class).size());
        JSONObject missingPermission = page(); missingPermission.remove("localPreviewSafe");
        call("attachPageSnapshot", new Class<?>[]{View.class, JSONObject.class}, bubble, missingPermission);
        assertEquals(1, web.callbacks.size());
    }

    @Ignore("0.8 screenshot harness replaced") @Test public void navigationAndNewConversationDiscardLateScreenshotCallbacks() throws Exception {
        enabled(true); View old = pageMessage(); JSONObject oldPage = page();
        web.url = "https://example.com/next"; web.getWebViewClient().onPageStarted(web, web.url, null);
        web.callbacks.get(0).onReceiveValue(oldPage.toString());
        assertEquals(0, web.draws); assertTrue(descendants(old, ImageView.class).isEmpty());
        web.getWebViewClient().onPageFinished(web, web.url);
        pageMessage(); ValueCallback<String> pending = web.callbacks.get(1); JSONObject current = page();
        call("newConversation"); pending.onReceiveValue(current.toString());
        assertEquals(0, web.draws); assertTrue(descendants(timeline().getView(), ImageView.class).isEmpty());
        assertEquals(0, history().length()); assertFalse(text(timeline().getView()).contains("Public product result"));
    }

    @Ignore("0.8 removed browser-off mode") @Test public void browserDisablePreventsLateScreenshotCapture() throws Exception {
        enabled(true); pageMessage(); JSONObject expected = page();
        enabled(false); web.callbacks.get(0).onReceiveValue(expected.toString()); assertEquals(0, web.draws);
        assertNull(get("mWeb"));
    }

    @Ignore("0.8 screenshot harness replaced") @Test public void privateInputPreventsLateScreenshotCapture() throws Exception {
        enabled(true); pageMessage();
        @SuppressWarnings("unchecked") List<EditText> fields = (List<EditText>)get("mHumanFields");
        EditText privateField = new EditText(activity); privateField.setText("private address"); fields.add(privateField);
        web.callbacks.get(0).onReceiveValue(page().toString());
        assertEquals(0, web.draws); assertFalse(history().toString().contains("private address"));
    }

    @Ignore("0.8 screenshot harness replaced") @Test public void sharingSensitivePageAddsOnlyAnExplanationAndNoImage() throws Exception {
        enabled(true); visible(true); call("shareCurrentPage");
        web.callbacks.get(0).onReceiveValue(page().put("sensitive", true).toString());
        assertEquals(0, web.draws); assertEquals(1, web.callbacks.size());
        assertTrue(descendants(timeline().getView(), ImageView.class).isEmpty());
        assertTrue(text(timeline().getView()).contains("이미지를 남기지 않았어요")); assertFalse((Boolean)get("mPageVisible"));
    }

    @Ignore("0.8 page-open control removed") @Test public void explicitPageShareReadsThenRevalidatesBeforeCapturing() throws Exception {
        enabled(true); call("shareCurrentPage");
        web.callbacks.get(0).onReceiveValue(page().toString());
        assertEquals(2, web.callbacks.size()); assertEquals(0, web.draws);
        web.callbacks.get(1).onReceiveValue(page().toString());
        assertEquals(1, web.draws); assertEquals(1, descendants(timeline().getView(), ImageView.class).size());
    }

    @Ignore("0.8 foreground service keeps chat running") @Test public void pauseDiscardsAnAlreadyQueuedChatSuccessAndNeverAutomaticallyResumes() throws Exception {
        send("Late answer test"); worker.runNext(); // API fixture completed; its UI callback has not run.
        long serial = (Long)get("mChatSerial"); activity.onPause();
        assertTrue((Long)get("mChatSerial") > serial); assertFalse((Boolean)get("mChatBusy"));
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, history().length()); assertFalse(text(timeline().getView()).contains("Offline fixture answer"));
        activity.onResume(); assertFalse((Boolean)get("mChatBusy")); assertEquals(1, ChatFixture.calls);
        assertTrue(worker.pending.isEmpty());
    }

    @Test public void newConversationDiscardsAnAlreadyQueuedChatError() throws Exception {
        ChatFixture.fail = true; send("Late error test"); worker.runNext();
        call("newConversation"); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(0, history().length()); assertFalse((Boolean)get("mChatBusy"));
        assertFalse(text(timeline().getView()).contains("다시 보내면 재시도합니다"));
        assertTrue(text(timeline().getView()).contains("새 대화를 시작했어요"));
    }

    @Ignore("0.8 removed manual mode switching") @Test public void switchingModesCancelsAQueuedPlainChatBeforeCallingItsClient() throws Exception {
        send("Queued plain chat"); long serial = (Long)get("mChatSerial");
        enabled(true); worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertTrue((Long)get("mChatSerial") > serial); assertFalse((Boolean)get("mChatBusy"));
        assertEquals(0, ChatFixture.calls); assertTrue(web.callbacks.isEmpty());
    }

    @Ignore("0.8 foreground service keeps active work running") @Test public void pauseDiscardsLateScreenshotAndKeepsOnlyExistingPublicConversation() throws Exception {
        enabled(true); pageMessage(); activity.onPause();
        web.callbacks.get(0).onReceiveValue(page().toString());
        assertEquals(0, web.draws); assertEquals(1, history().length());
        assertTrue(descendants(timeline().getView(), ImageView.class).isEmpty());
    }

    @Ignore("0.8 screenshot harness replaced") @Test public void newConversationClearsTaskAndRecyclesOwnedScreenshots() throws Exception {
        enabled(true); View bubble = pageMessage(); web.callbacks.get(0).onReceiveValue(page().toString());
        Bitmap retained = ((BitmapDrawable)descendants(bubble, ImageView.class).get(0).getDrawable()).getBitmap();
        set("mGoal", "Old goal"); set("mHasTask", true); set("mLastObservation", page());
        ((BrowserTaskMemory)get("mMemory")).observe(page());
        set("mUserReplies", new JSONArray().put(new JSONObject().put("question", "Color?").put("answer", "Red")));
        call("newConversation");
        assertTrue(retained.isRecycled()); assertEquals(0, history().length()); assertEquals("", get("mGoal"));
        assertFalse((Boolean)get("mHasTask")); assertNull(get("mLastObservation"));
        assertEquals(0, ((JSONArray)get("mUserReplies")).length());
        assertEquals(0, ((BrowserTaskMemory)get("mMemory")).toJson().getJSONArray("pages").length());
        assertTrue(descendants(timeline().getView(), ImageView.class).isEmpty());
    }

    @Ignore("0.8 reasoning router owns dispatch") @Test public void longBrowserResultCannotPoisonTheNextPlainChatRequest() throws Exception {
        String result = new String(new char[15000]).replace('\0', 'x');
        call("addAssistantMessage", new Class<?>[]{String.class}, result);
        assertEquals(12000, history().getJSONObject(0).getString("content").length());
        send("Summarize that"); worker.runNext(); Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals(1, ChatFixture.calls); assertEquals(2, ChatFixture.messages.length());
        assertEquals("Summarize that", ChatFixture.messages.getJSONObject(1).getString("content"));
        assertEquals("Offline fixture answer", history().getJSONObject(2).getString("content"));
    }

    @Ignore("0.8 screenshot harness replaced") @Test public void destroyClearsConversationAndReleasesTheTimeline() throws Exception {
        enabled(true); View bubble = pageMessage(); web.callbacks.get(0).onReceiveValue(page().toString());
        BrowserChatTimeline timeline = timeline();
        Bitmap retained = ((BitmapDrawable)descendants(bubble, ImageView.class).get(0).getDrawable()).getBitmap();
        activity.onDestroy();
        assertEquals(0, history().length()); assertEquals("", get("mApiKey")); assertTrue(retained.isRecycled());
        assertTrue(descendants(timeline.getView(), ImageView.class).isEmpty());
        timeline.addAssistant("Late message after destroy");
        assertFalse(text(timeline.getView()).contains("Late message after destroy"));
    }
}
