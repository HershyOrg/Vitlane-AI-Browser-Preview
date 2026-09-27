package com.vitlane.browser;

import static org.junit.Assert.*;

import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28, manifest = Config.NONE)
public class BrowserV08PolicyTest {
    private static JSONObject page() throws Exception {
        return new JSONObject().put("url", "https://shop.example.com/checkout")
                .put("documentId", "doc-1").put("title", "Checkout")
                .put("text", "Shipping details").put("sensitive", false)
                .put("elements", new JSONArray())
                .put("personalFields", new JSONArray().put(new JSONObject()
                        .put("kind", "address").put("label", "배송 주소")));
    }

    private static JSONObject response(JSONObject action) throws Exception {
        return new JSONObject().put("status", "completed").put("error", JSONObject.NULL)
                .put("output", new JSONArray().put(new JSONObject().put("type", "message")
                        .put("role", "assistant").put("status", "completed")
                        .put("content", new JSONArray().put(new JSONObject()
                                .put("type", "output_text").put("text", action.toString())))));
    }

    @Test public void routerUsesReasoningAndStrictStructuredOutput() throws Exception {
        JSONObject request = BrowserIntentRouter.buildRequest("gpt-6-sol", "내일 항공편을 찾아줘", new JSONArray());
        assertEquals("medium", request.getJSONObject("reasoning").getString("effort"));
        JSONObject format = request.getJSONObject("text").getJSONObject("format");
        assertEquals("json_schema", format.getString("type"));
        assertTrue(format.getBoolean("strict"));
        assertTrue(request.getJSONArray("input").getJSONObject(1).getString("content").contains("localTime"));
        JSONObject schema = format.getJSONObject("schema");
        assertTrue(schema.getJSONObject("properties").has("needsLocation"));
        assertTrue(schema.getJSONArray("required").toString().contains("needsLocation"));
        assertTrue(schema.getJSONObject("properties").has("clarificationQuestion"));
        assertTrue(request.getJSONArray("input").getJSONObject(0).getString("content").contains("correct search space"));
    }

    @Test public void routerAcceptsOnlyKnownModes() throws Exception {
        JSONObject answer = routeAnswer("browser");
        assertEquals("browser", BrowserIntentRouter.routeFromResponse(response(answer)));
        answer.put("needsLocation", true);
        assertTrue(BrowserIntentRouter.decisionFromResponse(response(answer)).getBoolean("needsLocation"));
        answer.put("mode", "tool");
        try { BrowserIntentRouter.routeFromResponse(response(answer)); fail(); }
        catch (BrowserIntentRouter.RouteException expected) { assertEquals("RESPONSE", expected.code); }
    }

    @Test public void routerCanAskOneBoundedQuestionBeforeSearch() throws Exception {
        JSONObject answer = routeAnswer("browser").put("clarificationNeeded", true)
                .put("clarificationQuestion", "남성용, 여성용, 공용 중 어느 쪽을 찾을까요?")
                .put("clarificationOptions", new JSONArray().put("남성용").put("여성용").put("공용"));
        JSONObject decision = BrowserIntentRouter.decisionFromResponse(response(answer));
        assertTrue(decision.getBoolean("clarificationNeeded"));
        assertEquals(3, decision.getJSONArray("clarificationOptions").length());

        answer.put("clarificationQuestion", "카드 번호를 알려주세요");
        try { BrowserIntentRouter.decisionFromResponse(response(answer)); fail(); }
        catch (BrowserIntentRouter.RouteException expected) { assertEquals("RESPONSE", expected.code); }
    }

    private static JSONObject routeAnswer(String mode) throws Exception {
        return new JSONObject().put("mode", mode).put("reason", "현재 정보 필요").put("needsLocation", false)
                .put("clarificationNeeded", false).put("clarificationQuestion", "")
                .put("clarificationOptions", new JSONArray());
    }

    @Test public void locationContextIsApproximateAndAllowlisted() throws Exception {
        JSONObject raw = new JSONObject().put("available", true).put("reason", "")
                .put("latitude", 37.56651).put("longitude", 126.97801).put("accuracyMeters", 18)
                .put("approximate", true).put("city", "서울").put("region", "서울특별시")
                .put("country", "kr").put("timezone", "Asia/Seoul");
        JSONObject safe = BrowserLocationContext.sanitize(raw);
        assertEquals(37.567, safe.getDouble("latitude"), 0.0001);
        assertEquals(126.978, safe.getDouble("longitude"), 0.0001);
        assertEquals("KR", safe.getString("country"));
        assertEquals("approximate", BrowserLocationContext.webSearchUserLocation(safe).getString("type"));
        try { BrowserLocationContext.sanitize(new JSONObject(raw.toString()).put("cookie", "secret")); fail(); }
        catch (IllegalArgumentException expected) { assertTrue(expected.getMessage().contains("Unsupported")); }
    }

    @Test public void personalFieldFailurePreventsAnUnchangedPageRetry() {
        JSONObject feedback = BrowserRecoveryPolicy.feedback("PERSONAL_FIELD");
        assertEquals("PERSONAL_FIELD", feedback.optString("code"));
        assertTrue(feedback.optString("strategy").contains("같은 화면"));
        assertTrue(feedback.optString("strategy").contains("직접 입력"));
    }

    @Test public void blockedChildNavigationGivesThePlannerANonRepeatingRecoveryPath() {
        JSONObject external = BrowserRecoveryPolicy.feedback("EXTERNAL_NAVIGATION");
        assertEquals("EXTERNAL_NAVIGATION", external.optString("code"));
        assertTrue(external.optString("strategy").contains("현재 웹페이지를 유지"));
        assertTrue(external.optString("strategy").contains("반복하지 말고"));
        JSONObject blocked = BrowserRecoveryPolicy.feedback("BLOCKED_NAVIGATION");
        assertEquals("BLOCKED_NAVIGATION", blocked.optString("code"));
        assertTrue(blocked.optString("strategy").contains("다른 공개 HTTPS"));
    }

    @Test public void checkoutPageCanAskForLocallyStoredAddress() throws Exception {
        JSONObject proposal = new JSONObject().put("type", "ask_user")
                .put("question", "배송 주소를 알려주세요")
                .put("fieldKind", "address").put("purpose", "주문 배송지 입력")
                .put("message", "배송 단계에 필요합니다");
        JSONObject action = BrowserAgent.actionFromResponse(response(proposal), page());
        assertEquals("ask_user", action.getString("type"));
        assertEquals("address", action.getString("fieldKind"));
        assertEquals("주문 배송지 입력", action.getString("purpose"));
    }

    @Test public void passwordAndCardQuestionsStillRequirePageHandoff() throws Exception {
        for (String question : new String[]{"비밀번호를 알려주세요", "카드 번호를 알려주세요", "Enter the OTP"}) {
            JSONObject proposal = new JSONObject().put("type", "ask_user").put("question", question)
                    .put("message", "필요합니다");
            assertEquals("handoff", BrowserAgent.actionFromResponse(response(proposal), page()).getString("type"));
        }
    }

    @Test public void checkoutNavigationIsAllowedButSecretQueryIsNotSerialized() throws Exception {
        JSONObject proposal = new JSONObject().put("type", "navigate")
                .put("url", "https://shop.example.com/checkout").put("message", "체크아웃으로 이동");
        assertEquals("navigate", BrowserAgent.actionFromResponse(response(proposal), page()).getString("type"));
        assertTrue(BrowserAgent.safeUrl("https://shop.example.com/checkout").contains("checkout"));
        try { BrowserAgent.safeUrl("https://shop.example.com/callback?token=secret-value"); fail(); }
        catch (BrowserAgent.PlannerException expected) { assertEquals("URL", expected.code); }
    }

    @Test public void resultCatalogIsBoundedAndSanitized() throws Exception {
        JSONObject proposal = new JSONObject().put("type", "handoff").put("message", "선택이 필요합니다")
                .put("catalog", new JSONArray().put(new JSONObject().put("title", "오전 항공편")
                        .put("subtitle", "09:10 출발 · 직항").put("price", "₩420,000")
                        .put("url", "https://travel.example.com/flights/1")
                        .put("badges", new JSONArray().put("직항").put("요청 시간 충족"))));
        JSONObject action = BrowserAgent.actionFromResponse(response(proposal), page());
        JSONObject card = action.getJSONArray("catalog").getJSONObject(0);
        assertEquals("오전 항공편", card.getString("title"));
        assertEquals(2, card.getJSONArray("badges").length());
    }

    @Test public void savedCardPaymentSignalSurvivesObservationSanitization() throws Exception {
        JSONObject payment = page().put("paymentRequired", true);
        assertTrue(BrowserAgent.sanitizeObservation(payment).getBoolean("paymentRequired"));
        try { BrowserAgent.sanitizeObservation(page().put("paymentRequired", "true")); fail(); }
        catch (BrowserAgent.PlannerException expected) { assertEquals("INPUT", expected.code); }
    }

    @Test public void localVaultAcceptsContactFieldsAndRejectsSecrets() {
        assertEquals("address", BrowserPrivateDataStore.requireKind(" ADDRESS "));
        assertEquals("user@example.com", BrowserPrivateDataStore.requireValue("email", "user@example.com"));
        for (String secret : new String[]{"password: hunter2", "인증번호 123456", "카드 번호 4111111111111111", "4111 1111 1111 1111"}) {
            try { BrowserPrivateDataStore.requireValue("address", secret); fail(); }
            catch (IllegalArgumentException expected) { assertTrue(expected.getMessage().contains("저장할 수 없습니다")); }
        }
        assertTrue(BrowserAgent.isSecretUserInput("4111 1111 1111 1111"));
        assertEquals("correct horse battery staple", BrowserPrivateDataStore.requireSecretValue("password", "correct horse battery staple"));
        assertEquals("123456", BrowserPrivateDataStore.requireSecretValue("otp", "123 456"));
        assertEquals("4111111111111111", BrowserPrivateDataStore.requireSecretValue("card_number", "4111 1111 1111 1111"));
        assertEquals("12/30", BrowserPrivateDataStore.requireSecretValue("card_expiry", "12/30"));
        assertEquals("12", BrowserPrivateDataStore.requireSecretValue("card_exp_month", "12"));
        assertEquals("2030", BrowserPrivateDataStore.requireSecretValue("card_exp_year", "2030"));
        assertEquals("123", BrowserPrivateDataStore.requireSecretValue("card_cvc", "123"));
        for (String[] invalid : new String[][]{{"otp", "abc123"}, {"card_number", "4111111111111112"},
                {"card_expiry", "13/30"}, {"card_cvc", "12"}}) {
            try { BrowserPrivateDataStore.requireSecretValue(invalid[0], invalid[1]); fail(); }
            catch (IllegalArgumentException expected) { assertFalse(expected.getMessage().isEmpty()); }
        }
    }
}
