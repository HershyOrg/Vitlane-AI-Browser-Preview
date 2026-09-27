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
public final class BrowserResearchPlannerTest {
    private static final String ENTRY = "https://www.amazon.com/dp/B0ABC12345";

    private static JSONObject task() throws Exception {
        return new JSONObject().put("requirements", new JSONArray().put(new JSONObject()
                .put("id", "r1").put("quote", "가장 싼 옵션").put("kind", "required").put("coverage", "each")))
                .put("scope", new JSONArray());
    }

    private static JSONObject rawPlan() throws Exception {
        return new JSONObject().put("completionMode", "browser").put("answer", "")
                .put("answerSourceUrls", new JSONArray())
                .put("summary", "상품 상세 페이지에서 옵션과 현재 가격을 다시 확인합니다.")
                .put("entryUrl", ENTRY).put("destinationTitle", "Amazon product")
                .put("routeRationale", "검색 결과 페이지를 거치지 않는 신뢰도 높은 진입점입니다.")
                .put("task", task())
                .put("executionGraph", new JSONArray()
                        .put(new JSONObject().put("id", "s1").put("stage", "entry").put("expected", "상품 상세 확인")
                                .put("successSignals", new JSONArray().put("Add to Cart").put("옵션")) .put("risk", "low"))
                        .put(new JSONObject().put("id", "s2").put("stage", "cart").put("expected", "장바구니 확인")
                                .put("successSignals", new JSONArray().put("Cart")) .put("risk", "medium")))
                .put("alternatives", new JSONArray()
                        .put(new JSONObject().put("url", "https://www.bestbuy.com/site/item/123.p")
                                .put("title", "Uncited alternative").put("reason", "가격 비교")))
                .put("findings", new JSONArray().put(new JSONObject().put("text", "검색 시점 가격은 변동될 수 있습니다.")
                        .put("url", ENTRY).put("volatile", true)))
                .put("dynamicRisks", new JSONArray().put("지역과 로그인 상태에 따라 가격이 달라질 수 있음"))
                .put("loginLikely", true)
                .put("cost", new JSONObject().put("actionCount", 5).put("pageLoads", 3)
                        .put("failureRisk", "medium").put("irreversibleRisk", "high"));
    }

    private static JSONObject response() throws Exception {
        JSONObject source = new JSONObject().put("url", ENTRY).put("title", "Amazon product");
        JSONObject part = new JSONObject().put("type", "output_text").put("text", rawPlan().toString())
                .put("annotations", new JSONArray().put(new JSONObject().put("type", "url_citation")
                        .put("url", ENTRY).put("title", "Amazon product")));
        return new JSONObject().put("status", "completed").put("error", JSONObject.NULL)
                .put("output", new JSONArray()
                        .put(new JSONObject().put("type", "reasoning"))
                        .put(new JSONObject().put("type", "web_search_call").put("status", "completed")
                                .put("action", new JSONObject().put("type", "search")
                                        .put("sources", new JSONArray().put(source))))
                        .put(new JSONObject().put("type", "message").put("role", "assistant").put("status", "completed")
                                .put("content", new JSONArray().put(part))));
    }

    private static JSONObject observation(String text) throws Exception {
        return new JSONObject().put("url", ENTRY).put("title", "Amazon product").put("text", text)
                .put("elements", new JSONArray()).put("sensitive", false);
    }

    @Test public void requestRequiresHostedSearchBeforePlanning() throws Exception {
        JSONObject request = BrowserResearchPlanner.buildRequest("gpt-6-sol", "가장 싼 옵션을 찾아줘", new JSONArray());
        assertEquals("web_search", request.getJSONArray("tools").getJSONObject(0).getString("type"));
        assertEquals("medium", request.getJSONArray("tools").getJSONObject(0).getString("search_context_size"));
        assertTrue(request.getJSONArray("tools").getJSONObject(0).getBoolean("external_web_access"));
        assertEquals("required", request.getString("tool_choice"));
        assertEquals("web_search_call.action.sources", request.getJSONArray("include").getString(0));
        assertEquals("json_schema", request.getJSONObject("text").getJSONObject("format").getString("type"));
        assertEquals("medium", request.getJSONObject("reasoning").getString("effort"));
        assertFalse(request.getBoolean("store"));
    }

    @Test public void approximateLocationIsScopedToSearchAndRequestPayload() throws Exception {
        JSONObject location = new JSONObject().put("available", true).put("reason", "")
                .put("latitude", 37.56653).put("longitude", 126.97797).put("accuracyMeters", 25)
                .put("approximate", true).put("city", "서울").put("region", "서울특별시")
                .put("country", "KR").put("timezone", "Asia/Seoul");
        JSONObject request = BrowserResearchPlanner.buildRequest("gpt-6-sol", "내 근처 식당", new JSONArray(), location);
        JSONObject toolLocation = request.getJSONArray("tools").getJSONObject(0).getJSONObject("user_location");
        assertEquals("approximate", toolLocation.getString("type"));
        assertEquals("KR", toolLocation.getString("country"));
        JSONObject payload = new JSONObject(request.getJSONArray("input").getJSONObject(1).getString("content"));
        assertEquals(37.567, payload.getJSONObject("userLocation").getDouble("latitude"), 0.0001);
        assertFalse(request.toString().contains("37.56653"));
    }

    @Test public void responseKeepsOnlySearchSourcedDestinations() throws Exception {
        JSONObject plan = BrowserResearchPlanner.planFromResponse(response());
        assertTrue(plan.getBoolean("searched"));
        assertEquals(ENTRY, plan.getString("entryUrl"));
        assertEquals(0, plan.getJSONArray("alternatives").length());
        assertEquals(1, plan.getJSONArray("findings").length());
        assertEquals(1, plan.getJSONArray("sources").length());
        assertEquals("가장 싼 옵션", plan.getJSONObject("task").getJSONArray("requirements")
                .getJSONObject(0).getString("quote"));
    }

    @Test public void sourcedSearchAnswerFinishesWithoutBrowser() throws Exception {
        JSONObject answerPlan = rawPlan().put("completionMode", "answer")
                .put("answer", "검색 결과에 따르면 요청한 공개 정보는 다음과 같습니다.")
                .put("answerSourceUrls", new JSONArray().put(ENTRY))
                .put("executionGraph", new JSONArray());
        JSONObject answerResponse = response();
        answerResponse.getJSONArray("output").getJSONObject(2).getJSONArray("content")
                .getJSONObject(0).put("text", answerPlan.toString());
        JSONObject result = BrowserResearchPlanner.planFromResponse(answerResponse);
        assertTrue(BrowserResearchPlanner.isSearchOnly(result));
        assertTrue(BrowserResearchPlanner.answer(result).contains("공개 정보"));
        assertEquals(1, result.getJSONArray("answerSourceUrls").length());
    }

    @Test public void nativeSnapshotCannotFinishFromAnUnrelatedAnswerUrl() throws Exception {
        JSONObject answerPlan = BrowserResearchPlanner.planFromResponse(response())
                .put("completionMode", "answer").put("answer", "출처가 있는 것처럼 보이는 답변")
                .put("answerSourceUrls", new JSONArray().put("https://unrelated.example.net/result"))
                .put("executionGraph", new JSONArray());
        assertFalse(BrowserResearchPlanner.isSearchOnly(answerPlan));
        assertEquals("", BrowserResearchPlanner.answer(answerPlan));
    }

    @Test public void verifierComparesExpectedSignalsWithLivePage() throws Exception {
        JSONObject plan = BrowserResearchPlanner.planFromResponse(response());
        JSONObject state = BrowserExecutionVerifier.inspect(plan, observation("옵션을 고른 뒤 Add to Cart를 누를 수 있습니다."));
        assertEquals("expected_stage", state.getString("state"));
        assertEquals("s1", state.getString("matchedStep"));
        assertEquals("장바구니 확인", state.getString("expectedNext"));
        assertTrue(state.getBoolean("entryMatch"));
    }

    @Test public void toolOutputsOtherThanWebSearchAreRejected() throws Exception {
        JSONObject invalid = response();
        invalid.getJSONArray("output").put(new JSONObject().put("type", "function_call").put("name", "pay"));
        try {
            BrowserResearchPlanner.planFromResponse(invalid);
            fail("Expected response rejection");
        } catch (BrowserResearchPlanner.ResearchException expected) {
            assertEquals("RESPONSE", expected.code);
        }
    }
}
