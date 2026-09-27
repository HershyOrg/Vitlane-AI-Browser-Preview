package com.vitlane.browser;

import static org.junit.Assert.*;

import android.net.Uri;
import android.os.Looper;
import android.os.SystemClock;
import android.view.View;
import android.view.ViewGroup;
import android.webkit.ValueCallback;
import android.webkit.WebResourceRequest;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.EditText;
import android.widget.Button;
import android.widget.LinearLayout;
import android.widget.TextView;

import java.lang.reflect.Field;
import java.lang.reflect.Method;
import java.util.ArrayList;
import java.util.Collections;
import java.util.List;
import java.util.Map;
import java.util.concurrent.TimeUnit;

import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.Shadows;
import org.robolectric.android.controller.ActivityController;
import org.robolectric.annotation.Config;
import org.robolectric.annotation.LooperMode;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 34, manifest = Config.NONE)
@LooperMode(LooperMode.Mode.PAUSED)
public class BrowserV08ActivityTest {
    private VitlaneBrowserActivity activity;
    private ControlledWebView web;

    private static final class ControlledWebView extends WebView {
        String url = "https://shop.example.com/product";
        final List<String> scripts = new ArrayList<>();
        final List<ValueCallback<String>> callbacks = new ArrayList<>();
        ControlledWebView(VitlaneBrowserActivity context) { super(context); }
        @Override public String getUrl() { return url; }
        @Override public void evaluateJavascript(String script, ValueCallback<String> callback) {
            scripts.add(script); callbacks.add(callback);
        }
    }

    private Object get(String name) throws Exception {
        Field field = VitlaneBrowserActivity.class.getDeclaredField(name);
        field.setAccessible(true);
        return field.get(activity);
    }

    private void set(String name, Object value) throws Exception {
        Field field = VitlaneBrowserActivity.class.getDeclaredField(name);
        field.setAccessible(true);
        field.set(activity, value);
    }

    private Object call(String name, Class<?>[] types, Object... args) throws Exception {
        Method method = VitlaneBrowserActivity.class.getDeclaredMethod(name, types);
        method.setAccessible(true);
        return method.invoke(activity, args);
    }

    private JSONObject page(String label) throws Exception {
        return new JSONObject().put("url", web.url).put("documentId", "doc-1")
                .put("title", "Product").put("text", "Product details").put("sensitive", false)
                .put("elements", new JSONArray().put(new JSONObject().put("id", "e1")
                        .put("tag", "button").put("role", "button").put("label", label)));
    }

    private void handle(JSONObject page, JSONObject action) throws Exception {
        call("handleAction", new Class<?>[]{int.class, long.class, String.class, JSONObject.class, JSONObject.class},
                (Integer)get("mGeneration"), (Long)get("mDocumentEpoch"), web.url, page, action);
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

    @Before public void setup() throws Exception {
        activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        web = new ControlledWebView(activity);
        set("mWeb", web);
        set("mAddress", new EditText(activity));
        set("mOrigin", new TextView(activity));
        set("mStatus", new TextView(activity));
        set("mGoalInput", new EditText(activity));
        set("mBrowserEnabled", true);
        set("mForeground", true);
        set("mRunning", true);
        set("mHasTask", true);
        set("mGoal", "상품을 장바구니에 담아줘");
        set("mApiKey", "sk-test-unused");
        set("mTimeoutSeconds", 60);
        set("mDeadline", SystemClock.elapsedRealtime() + 100000);
    }

    @After public void tearDown() { activity.onDestroy(); }

    @Test public void ordinaryCartClickExecutesWithoutApprovalPrompt() throws Exception {
        handle(page("장바구니에 담기"), new JSONObject().put("type", "click")
                .put("targetId", "e1").put("message", "장바구니에 담습니다"));
        assertEquals("", get("mHumanMode"));
        assertEquals(1, web.scripts.size());
        assertTrue(web.scripts.get(0).contains("\"approved\":true"));
    }

    @Test public void visibleBrowserHighlightsTheGroundedTargetBeforeExecutingIt() throws Exception {
        set("mPageVisible", true);
        handle(page("장바구니에 담기"), new JSONObject().put("type", "click")
                .put("targetId", "e1").put("message", "장바구니에 담습니다"));
        assertEquals(1, web.scripts.size());
        assertTrue(web.scripts.get(0).contains(").preview("));
        web.callbacks.get(0).onReceiveValue(new JSONObject().put("ok", true).put("highlighted", true).toString());
        Shadows.shadowOf(Looper.getMainLooper()).idleFor(321, TimeUnit.MILLISECONDS);
        assertEquals(2, web.scripts.size());
        assertTrue(web.scripts.get(1).contains(").action("));
    }

    @Test public void paymentControlShowsBrowserAndNeverDispatchesTheClick() throws Exception {
        handle(page("Pay now"), new JSONObject().put("type", "click")
                .put("targetId", "e1").put("message", "결제합니다"));
        assertEquals("manual", get("mHumanMode"));
        assertEquals(true, get("mPageVisible"));
        assertTrue(web.scripts.get(0).endsWith(").manualState()"));
    }

    @Test public void sensitiveHandoffOffersTheNativeVaultWithoutStartingAPlannerRequest() throws Exception {
        set("mPrivateData", new BrowserPrivateDataStore(activity));
        web.url = "https://shop.example.com/login";
        call("requestSecretInput", new Class<?>[]{String.class}, "비밀번호가 필요합니다");
        assertEquals("manual", get("mHumanMode"));
        assertEquals(true, get("mPageVisible"));
        assertTrue(web.scripts.stream().anyMatch(script -> script.endsWith(").secretFields()")));
        assertFalse((Boolean)get("mRouting"));
        assertFalse((Boolean)get("mChatBusy"));
    }

    @Test public void missingCompatiblePersonalFieldHandsOffInsteadOfRestartingTheAgent() throws Exception {
        set("mRunning", false);
        set("mPrivateData", new BrowserPrivateDataStore(activity));
        BrowserPrivateDataStore.SavedValue saved = new BrowserPrivateDataStore.SavedValue(7, "address", "local-only-address");
        call("fillPersonalData", new Class<?>[]{BrowserPrivateDataStore.SavedValue.class, String.class},
                saved, "배송지 입력");
        assertEquals(1, web.callbacks.size());
        web.callbacks.get(0).onReceiveValue(new JSONObject().put("url", web.url).put("documentId", "doc-1")
                .put("blocked", false).put("fields", new JSONArray().put(new JSONObject()
                        .put("id", "h1").put("kind", "phone").put("label", "전화번호"))).toString());

        assertEquals("manual", get("mHumanMode"));
        assertFalse((Boolean)get("mRunning"));
        assertEquals(2, web.callbacks.size()); // field discovery followed only by manual-state polling
        JSONObject last = ((JSONArray)get("mHistory")).getJSONObject(((JSONArray)get("mHistory")).length() - 1);
        assertEquals("rejected", last.getString("status"));
        assertTrue(last.getString("message").contains("호환되는 입력란"));
        JSONObject feedback = ((BrowserTaskMemory)get("mMemory")).toJson().getJSONObject("feedback");
        assertEquals("PERSONAL_FIELD", feedback.getString("code"));
        assertFalse(((BrowserTaskMemory)get("mMemory")).toJson().toString().contains("local-only-address"));

        LinearLayout panel = (LinearLayout)get("mHumanPanel");
        LinearLayout actions = (LinearLayout)panel.getChildAt(1);
        ((Button)actions.getChildAt(0)).performClick();
        assertEquals("", get("mHumanMode"));
        assertTrue((Boolean)get("mRunning"));
        call("requestPersonalData", new Class<?>[]{String.class, String.class, String.class},
                "address", "배송지 입력", "주소를 알려주세요");
        assertEquals("", get("mHumanMode"));
        assertEquals(1, web.scripts.stream().filter(script -> script.endsWith(").humanFields()")).count());
        JSONObject appliedFeedback = ((BrowserTaskMemory)get("mMemory")).toJson().getJSONObject("feedback");
        assertEquals("PERSONAL_FIELD_APPLIED", appliedFeedback.getString("code"));
    }

    @Test public void successfulPersonalFieldFillIsAppliedOnlyOnceOnTheSamePage() throws Exception {
        set("mRunning", false);
        set("mPrivateData", new BrowserPrivateDataStore(activity));
        BrowserPrivateDataStore.SavedValue saved = new BrowserPrivateDataStore.SavedValue(9, "email", "local-only@example.com");
        call("fillPersonalData", new Class<?>[]{BrowserPrivateDataStore.SavedValue.class, String.class},
                saved, "연락 이메일 입력");
        web.callbacks.get(0).onReceiveValue(new JSONObject().put("url", web.url).put("documentId", "doc-1")
                .put("blocked", false).put("fields", new JSONArray().put(new JSONObject()
                        .put("id", "h1").put("kind", "email").put("label", "이메일"))).toString());
        web.callbacks.get(1).onReceiveValue(new JSONObject().put("ok", true).put("filled", 1)
                .put("failed", 0).toString());

        assertTrue((Boolean)get("mRunning"));
        assertFalse(((BrowserTaskMemory)get("mMemory")).toJson().toString().contains("local-only@example.com"));
        call("requestPersonalData", new Class<?>[]{String.class, String.class, String.class},
                "email", "연락 이메일 입력", "이메일을 알려주세요");

        assertEquals(1, web.scripts.stream().filter(script -> script.endsWith(").humanFields()")).count());
        assertEquals("", get("mHumanMode"));
        assertEquals("PERSONAL_FIELD_APPLIED",
                ((BrowserTaskMemory)get("mMemory")).toJson().getJSONObject("feedback").getString("code"));
    }

    @Test public void rejectedPersonalFieldFillHandsOffAndDoesNotRetryAutomatically() throws Exception {
        set("mRunning", false);
        BrowserPrivateDataStore.SavedValue saved = new BrowserPrivateDataStore.SavedValue(8, "address", "local-only-address");
        call("fillPersonalData", new Class<?>[]{BrowserPrivateDataStore.SavedValue.class, String.class},
                saved, "배송지 입력");
        web.callbacks.get(0).onReceiveValue(new JSONObject().put("url", web.url).put("documentId", "doc-1")
                .put("blocked", false).put("fields", new JSONArray().put(new JSONObject()
                        .put("id", "h1").put("kind", "address").put("label", "주소"))).toString());
        assertEquals(2, web.callbacks.size());
        web.callbacks.get(1).onReceiveValue(new JSONObject().put("ok", false).put("filled", 0)
                .put("failed", 1).put("code", "NO_EFFECT").toString());

        assertEquals("manual", get("mHumanMode"));
        assertFalse((Boolean)get("mRunning"));
        assertEquals(3, web.callbacks.size());
        assertTrue(((JSONArray)get("mHistory")).toString().contains("입력값 반영을 거절"));
        assertFalse(((JSONArray)get("mHistory")).toString().contains("local-only-address"));
    }

    @Test public void scrollDoesNotStopAnActiveTaskButTapDoes() throws Exception {
        call("configureBrowser", new Class<?>[]{});
        long down = SystemClock.uptimeMillis();
        call("onBrowserTouch", new Class<?>[]{android.view.MotionEvent.class},
                android.view.MotionEvent.obtain(down, down, android.view.MotionEvent.ACTION_DOWN, 10, 10, 0));
        call("onBrowserTouch", new Class<?>[]{android.view.MotionEvent.class},
                android.view.MotionEvent.obtain(down, down + 20, android.view.MotionEvent.ACTION_MOVE, 10, 80, 0));
        call("onBrowserTouch", new Class<?>[]{android.view.MotionEvent.class},
                android.view.MotionEvent.obtain(down, down + 40, android.view.MotionEvent.ACTION_UP, 10, 100, 0));
        assertTrue("status=" + ((TextView)get("mStatus")).getText(), (Boolean)get("mRunning"));
    }

    @Test public void externalAppNavigationRejectsOnlyTheClickAndKeepsTheAgentRunning() throws Exception {
        call("configureBrowser", new Class<?>[]{});
        JSONObject action = new JSONObject().put("type", "click").put("targetId", "e1")
                .put("message", "상품 상세를 엽니다");
        set("mPendingAction", action);
        set("mBeforeAction", page("상품 상세"));
        set("mBusy", true);
        set("mExpectedNavigation", true);

        WebViewClient client = web.getWebViewClient();
        assertTrue(client.shouldOverrideUrlLoading(web,
                requestFor("intent://product/123#Intent;scheme=coupang;package=com.coupang.mobile;end")));

        assertTrue((Boolean)get("mRunning"));
        assertNull(get("mPendingAction"));
        assertFalse((Boolean)get("mBusy"));
        assertFalse((Boolean)get("mExpectedNavigation"));
        assertEquals("EXTERNAL_NAVIGATION",
                ((BrowserTaskMemory)get("mMemory")).toJson().getJSONObject("feedback").getString("code"));
        assertTrue(((JSONArray)get("mHistory")).toString().contains("현재 페이지를 유지"));
    }

    @Test public void httpLinkCanBeClickedBecauseNavigationWillUpgradeItToHttps() throws Exception {
        JSONObject linked = page("상품 상세");
        linked.getJSONArray("elements").getJSONObject(0)
                .put("tag", "a").put("role", "link").put("href", "http://shop.example.com/item/7");
        handle(linked, new JSONObject().put("type", "click").put("targetId", "e1")
                .put("message", "상품 상세를 엽니다"));
        assertTrue((Boolean)get("mRunning"));
        assertEquals(1, web.scripts.size());
        assertTrue(web.scripts.get(0).contains(").action("));
    }

    @Test public void notificationReplyFeedsTheVisibleChatAnswerComposer() throws Exception {
        EditText composer = (EditText)get("mGoalInput");
        final boolean[] submitted = {false};
        set("mHumanMode", "answer");
        set("mSubmitHumanAnswer", (Runnable)() -> submitted[0] = true);
        activity.onNotificationReply("창가 좌석");
        Shadows.shadowOf(Looper.getMainLooper()).idle();
        assertEquals("창가 좌석", composer.getText().toString());
        assertTrue(submitted[0]);
    }

    @Test public void chatComposerDoesNotSendRecognizableSecretsToTheRouter() throws Exception {
        set("mRunning", false);
        EditText composer = (EditText)get("mGoalInput");
        composer.setText("카드 번호 4111 1111 1111 1111");
        call("sendMessage", new Class<?>[]{});
        assertEquals("", composer.getText().toString());
        assertFalse((Boolean)get("mRouting"));
        assertTrue(((TextView)get("mStatus")).getText().toString().contains("AI에 보내지 않았어요"));
        assertEquals(0, ((JSONArray)get("mChatMessages")).length());
    }

    @Test public void usedVaultValuesAreRedactedAgainBeforeAnyLaterModelObservation() throws Exception {
        JSONArray entries = new JSONArray().put(new JSONObject().put("value", "local-password-sentinel"))
                .put(new JSONObject().put("value", "123"));
        call("rememberSecretRedactions", new Class<?>[]{JSONArray.class}, entries);
        JSONObject observation = new JSONObject().put("url", "https://shop.example.com/account/123")
                .put("documentId", "doc-123").put("revision", "123")
                .put("title", "Account").put("text", "Welcome local-password-sentinel")
                .put("sensitive", false)
                .put("elements", new JSONArray().put(new JSONObject().put("id", "e123")
                        .put("tag", "a").put("role", "link")
                        .put("label", "Order 123")
                        .put("href", "https://shop.example.com/order?token=local-password-sentinel")));
        assertTrue((Boolean)call("redactRememberedSecrets", new Class<?>[]{JSONObject.class}, observation));
        assertFalse(observation.getString("text").contains("local-password-sentinel"));
        assertTrue(observation.getString("text").contains("보안정보 숨김"));
        assertEquals("https://shop.example.com/account/123", observation.getString("url"));
        assertEquals("doc-123", observation.getString("documentId"));
        assertEquals("123", observation.getString("revision"));
        assertEquals("e123", observation.getJSONArray("elements").getJSONObject(0).getString("id"));
        assertFalse(observation.getJSONArray("elements").getJSONObject(0).has("href"));
        BrowserAgent.sanitizeObservation(observation);
        assertFalse(observation.optBoolean("localPreviewSafe", true));
        assertFalse(observation.optBoolean("screenshotSafe", true));
    }

    @Test public void leavingTheActivityKeepsAnActiveTaskAndItsApiSessionAlive() throws Exception {
        set("mApiKey", "sk-test-background");
        activity.onPause();
        activity.onStop();
        assertEquals(true, get("mRunning"));
        assertEquals("sk-test-background", get("mApiKey"));
    }

    @Test public void publicQuestionCanResumeTheSameTaskFromTheComposer() throws Exception {
        JSONObject action = new JSONObject().put("type", "ask_user")
                .put("question", "선호 색상은 무엇인가요?").put("message", "선택에 필요합니다");
        call("requestUserAnswer", new Class<?>[]{JSONObject.class, JSONObject.class}, action, page("옵션"));
        assertEquals("answer", get("mHumanMode"));
        ((EditText)get("mHumanAnswer")).setText("파란색");
        ((Runnable)get("mSubmitHumanAnswer")).run();
        assertTrue("status=" + ((TextView)get("mStatus")).getText(), (Boolean)get("mRunning"));
        assertEquals("파란색", ((JSONArray)get("mUserReplies")).getJSONObject(0).getString("answer"));
    }

    @Test public void polishedChatStartsWithBrowserReadyAndVisibleChatBrowserTabs() throws Exception {
        ActivityController<VitlaneBrowserActivity> controller = Robolectric.buildActivity(VitlaneBrowserActivity.class).create().start().resume();
        VitlaneBrowserActivity ui = controller.get();
        try {
            Field enabled = VitlaneBrowserActivity.class.getDeclaredField("mBrowserEnabled");
            enabled.setAccessible(true);
            assertTrue((Boolean)enabled.get(ui));
            String rendered = allText(ui.findViewById(android.R.id.content));
            assertFalse(rendered.contains("브라우저 켜기"));
            assertFalse(rendered.contains("페이지 보기"));
            assertFalse(rendered.contains("현재 브라우저 열기"));
            assertTrue(rendered.contains("채팅"));
            assertTrue(rendered.contains("브라우저"));
            Field pageToggle = VitlaneBrowserActivity.class.getDeclaredField("mPageToggle");
            pageToggle.setAccessible(true);
            ((View)pageToggle.get(ui)).performClick();
            Field pageVisible = VitlaneBrowserActivity.class.getDeclaredField("mPageVisible");
            pageVisible.setAccessible(true);
            assertTrue((Boolean)pageVisible.get(ui));
        } finally { controller.pause().stop().destroy(); }
    }

    @Test public void onlyMoneyChargingLabelsRequireApproval() throws Exception {
        Method method = VitlaneBrowserActivity.class.getDeclaredMethod("finalCommitment", String.class);
        method.setAccessible(true);
        assertTrue((Boolean)method.invoke(null, "결제하기"));
        assertTrue((Boolean)method.invoke(null, "Pay now"));
        assertFalse((Boolean)method.invoke(null, "예약 확정"));
        assertFalse((Boolean)method.invoke(null, "장바구니에 담기"));
    }

    private static String allText(View root) {
        StringBuilder result = new StringBuilder();
        if (root instanceof TextView) result.append(((TextView)root).getText()).append('\n');
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup)root).getChildCount(); i++)
            result.append(allText(((ViewGroup)root).getChildAt(i)));
        return result.toString();
    }
}
