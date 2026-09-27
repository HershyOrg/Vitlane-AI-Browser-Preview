package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

public final class BrowserResearchPlannerTest {
    private static int checks;
    private static void check(boolean condition, String message) {
        checks++;
        if (!condition) throw new AssertionError(message);
    }

    private static JSONObject plan() throws Exception {
        String url = "https://www.amazon.com/dp/B0ABC12345";
        return new JSONObject().put("completionMode", "browser").put("answer", "")
                .put("answerSourceUrls", new JSONArray()).put("summary", "상품 상세 페이지부터 확인")
                .put("entryUrl", url).put("destinationTitle", "Amazon product").put("routeRationale", "검색 결과 단계를 줄임")
                .put("task", new JSONObject().put("requirements", new JSONArray().put(new JSONObject()
                        .put("id", "r1").put("quote", "가장 싼 옵션").put("kind", "required").put("coverage", "each")))
                        .put("scope", new JSONArray()))
                .put("executionGraph", new JSONArray().put(new JSONObject().put("id", "s1").put("stage", "entry")
                        .put("expected", "상품 상세 확인").put("successSignals", new JSONArray().put("Add to Cart")).put("risk", "low")))
                .put("alternatives", new JSONArray()).put("findings", new JSONArray().put(new JSONObject()
                        .put("text", "가격은 변동 가능").put("url", url).put("volatile", true)))
                .put("dynamicRisks", new JSONArray()).put("loginLikely", false)
                .put("cost", new JSONObject().put("actionCount", 4).put("pageLoads", 2)
                        .put("failureRisk", "low").put("irreversibleRisk", "high"));
    }

    private static JSONObject response() throws Exception {
        String url = "https://www.amazon.com/dp/B0ABC12345";
        return new JSONObject().put("status", "completed").put("error", JSONObject.NULL)
                .put("output", new JSONArray().put(new JSONObject().put("type", "web_search_call")
                                .put("action", new JSONObject().put("type", "search").put("sources", new JSONArray()
                                        .put(new JSONObject().put("url", url).put("title", "Amazon product")))))
                        .put(new JSONObject().put("type", "message").put("role", "assistant").put("status", "completed")
                                .put("content", new JSONArray().put(new JSONObject().put("type", "output_text")
                                        .put("text", plan().toString()).put("annotations", new JSONArray()
                                                .put(new JSONObject().put("type", "url_citation").put("url", url).put("title", "Amazon product")))))));
    }

    private static JSONObject inlineResponse() throws Exception {
        String url = "https://kr.loropiana.com/en/help/size-guide";
        JSONObject result = new JSONObject().put("summary", "남성 재킷의 문자·숫자 사이즈 대응을 확인했습니다.")
                .put("findings", new JSONArray()
                        .put(new JSONObject().put("text", "공식 가이드는 이 상품군의 L을 숫자 50으로 안내합니다.")
                                .put("url", url).put("confidence", "high").put("applicability", "한국 남성 재킷"))
                        .put(new JSONObject().put("text", "출처 없는 변환표입니다.")
                                .put("url", "https://uncited.example.net/sizes").put("confidence", "low").put("applicability", "불명")))
                .put("conflict", false).put("needsUserChoice", false);
        return new JSONObject().put("status", "completed").put("error", JSONObject.NULL)
                .put("output", new JSONArray().put(new JSONObject().put("type", "web_search_call").put("status", "completed")
                                .put("action", new JSONObject().put("sources", new JSONArray().put(new JSONObject()
                                        .put("url", url).put("title", "Loro Piana size guide")))))
                        .put(new JSONObject().put("type", "message").put("role", "assistant").put("status", "completed")
                                .put("content", new JSONArray().put(new JSONObject().put("type", "output_text")
                                        .put("text", result.toString()).put("annotations", new JSONArray())))));
    }

    public static void main(String[] args) throws Exception {
        JSONObject request = BrowserResearchPlanner.buildRequest("gpt-6-sol", "가장 싼 옵션", new JSONArray());
        check("web_search".equals(request.getJSONArray("tools").getJSONObject(0).getString("type")), "hosted web search enabled");
        check("required".equals(request.getString("tool_choice")), "search required before browser");
        check(!request.getBoolean("store"), "research is not stored by API");
        JSONObject parsed = BrowserResearchPlanner.planFromResponse(response());
        check(parsed.getBoolean("searched"), "search call verified");
        check("https://www.amazon.com/dp/B0ABC12345".equals(parsed.getString("entryUrl")), "cited direct entry retained");
        check(parsed.getJSONArray("sources").length() == 1, "search sources retained for user-visible task log");
        JSONObject agentRequest = BrowserAgent.buildRequest("gpt-6-sol", "가장 싼 옵션", new JSONObject()
                        .put("url", parsed.getString("entryUrl")).put("title", "Amazon product").put("text", "Add to Cart")
                        .put("elements", new JSONArray()).put("sensitive", false), new JSONArray(), new JSONObject(), null, null, null, parsed);
        JSONObject agentInput = new JSONObject(agentRequest.getJSONArray("input").getJSONObject(1).getString("content"));
        check(agentInput.getJSONObject("research").getBoolean("searched"), "planner context reaches navigator");
        check("expected_stage".equals(agentInput.getJSONObject("verification").getString("state")), "live page compared with graph");
        JSONObject product = new JSONObject().put("url", "https://kr.loropiana.com/en/man/jacket")
                .put("title", "Men jacket").put("text", "Choose your size")
                .put("sensitive", false).put("elements", new JSONArray().put(new JSONObject()
                        .put("id", "size").put("tag", "select").put("role", "combobox").put("label", "Size")
                        .put("options", new JSONArray().put(new JSONObject().put("value", "48").put("label", "48")
                                .put("selected", false).put("disabled", false)))));
        JSONObject inlineRequest = BrowserResearchPlanner.buildInlineRequest("gpt-6-sol",
                "Loro Piana men jacket L numeric size Korea", "L 대응 숫자 확인", product);
        check("required".equals(inlineRequest.getString("tool_choice"))
                && "web_search".equals(inlineRequest.getJSONArray("tools").getJSONObject(0).getString("type")),
                "inline lookup requires hosted search instead of navigating the WebView");
        boolean privateRejected = false;
        try { BrowserResearchPlanner.buildInlineRequest("gpt-6-sol", "person@example.com size", "lookup", product); }
        catch (BrowserResearchPlanner.ResearchException expected) { privateRejected = "SENSITIVE".equals(expected.code); }
        check(privateRejected, "inline lookup rejects private values before any network request");
        JSONObject inline = BrowserResearchPlanner.inlineFromResponse(inlineResponse());
        check(inline.getJSONArray("findings").length() == 1
                && inline.getJSONArray("sources").length() == 1
                && inline.getJSONArray("findings").getJSONObject(0).getString("url").contains("loropiana"),
                "inline lookup discards findings not backed by tool-returned sources");
        System.out.println("BrowserResearchPlanner " + checks);
    }
}
