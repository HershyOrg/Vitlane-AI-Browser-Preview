package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

/** Cross-page evidence retention, failure recovery and bounded/redacted request context. */
public final class BrowserTaskMemoryTest {
    private static int checks;
    public static void main(String[] args) throws Exception {
        BrowserTaskMemory memory = new BrowserTaskMemory();
        JSONObject first = page("https://shop.example.com/product?id=one", "첫 상품은 파란색이고 가격은 39,000원입니다.");
        memory.observe(first);
        JSONObject note = new JSONObject().put("type", "navigate").put("state", new JSONObject()
                .put("plan", new JSONArray().put("두 번째 상품을 확인하고 가격 비교"))
                .put("facts", new JSONArray().put(new JSONObject().put("text", "첫 상품 가격 39,000원")
                        .put("url", first.getString("url")).put("evidence", first.getString("text")))));
        memory.record(note, "unknown", "다음 페이지로 이동 예정");
        memory.observe(page("https://shop.example.com/product?id=two", "두 번째 상품은 45,000원이며 무료 배송입니다."));
        JSONObject snapshot = memory.toJson();
        check(snapshot.getJSONArray("pages").length() == 2, "public product IDs preserve distinct page identity");
        check(snapshot.getJSONArray("facts").length() == 1 && snapshot.getJSONArray("plan").length() == 1, "plan and grounded facts survive navigation");
        JSONArray evidence = new JSONArray().put(new JSONObject().put("url", first.getString("url")).put("quote", first.getString("text")));
        check(BrowserTaskMemory.validateEvidence(evidence, page("https://shop.example.com/other", "다른 페이지 내용입니다."), snapshot).length() == 1, "previous source can support final comparison");
        JSONArray wrong = new JSONArray().put(new JSONObject().put("url", "https://other.example.com/product").put("quote", first.getString("text")));
        check(BrowserTaskMemory.validateEvidence(wrong, first, snapshot).length() == 0, "a quote cannot be attached to an invented URL");
        JSONObject fabricated = new JSONObject(note.toString());
        fabricated.getJSONObject("state").getJSONArray("facts").getJSONObject(0).put("evidence", "공식 할인이 100퍼센트 적용됩니다.");
        memory.record(fabricated, "rejected", "요소가 변경됨");
        check(memory.toJson().getJSONArray("facts").length() == 1, "unsupported fact is discarded");
        memory.record(new JSONObject().put("type", "click"), "unknown", "계획");
        memory.record(new JSONObject().put("type", "click"), "rejected", "실패");
        memory.record(new JSONObject().put("type", "click"), "unknown", "계획");
        memory.record(new JSONObject().put("type", "click"), "rejected", "실패");
        check(memory.isStalled(), "three rejected actions remain visible despite planning records");
        memory.resetProgressWatchdog();
        check(!memory.isStalled() && memory.toJson().getJSONArray("facts").length() == 1
                && memory.toJson().getJSONArray("pages").length() == 2 && memory.toJson().getJSONArray("plan").length() == 1,
                "manual resume resets progress guards while preserving task context");
        memory.record(new JSONObject().put("type", "wait"), "applied", "대기 완료");
        check(!memory.isStalled(), "successful recovery resets rejection counter");
        BrowserTaskMemory clickMemory = new BrowserTaskMemory();
        clickMemory.observe(page("https://shop.example.com/product", "사이즈 안내"));
        clickMemory.record(new JSONObject().put("type", "click").put("targetId", "e_size").put("method", "native"),
                "unknown", "터치 결과 확인 중");
        JSONObject clickOutcome = BrowserTaskMemory.sanitize(clickMemory.toJson()).getJSONObject("lastOutcome");
        check("e_size".equals(clickOutcome.getString("targetId")) && "native".equals(clickOutcome.getString("method")),
                "click target and bounded transport survive sanitized recovery memory");

        JSONObject copy = memory.toJson();
        copy.getJSONArray("facts").getJSONObject(0).put("text", "external mutation");
        check(!memory.toJson().toString().contains("external mutation"), "snapshot is detached from live memory");
        for (int i = 0; i < 20; i++) memory.observe(page("https://shop.example.com/item?id=" + i, repeat("상품 설명 ", 500)));
        check(memory.toJson().getJSONArray("pages").length() == 8, "old page excerpts evicted at bound");
        check(memory.toJson().getJSONArray("pages").getJSONObject(0).getString("text").length() <= 1600, "page excerpts bounded");
        check(memory.toJson().getJSONArray("facts").length() == 1, "grounded extracted facts survive page eviction");

        JSONObject privatePage = page("https://shop.example.com/product?id=private", "never-send-secret").put("sensitive", true);
        memory.observe(privatePage);
        check(!memory.toJson().toString().contains("never-send-secret"), "sensitive page cannot enter memory");
        JSONObject publicPage = page("https://shop.example.com/search?q=shoes&tracking=ignored", "person@example.com sk-example_private_token ０１０１２３４５６７８ password: never-send-secret");
        memory.observe(publicPage);
        String serialized = memory.toJson().toString();
        check(!serialized.contains("person@example.com") && !serialized.contains("sk-example") && !serialized.contains("０１０") && !serialized.contains("never-send-secret"), "retained excerpts redact common private values");
        check("https://shop.example.com/search?q=shoes&page=2".equals(BrowserAgent.safeUrl("https://shop.example.com/search?q=shoes&page=2&utm_source=tracking#secret")), "useful query values retained and trackers removed");
        check("https://shop.example.com/search".equals(BrowserAgent.safeUrl("https://shop.example.com/search?q=person%40example.com&unknown=secret")), "URL query PII and unknown keys removed");
        String numericProduct = "https://shop.example.com/products/123456789012?itemId=456789012345";
        check(numericProduct.equals(BrowserAgent.safeUrl(numericProduct)), "public numeric resource IDs are not mistaken for phone numbers");
        BrowserTaskMemory numericMemory = new BrowserTaskMemory();
        numericMemory.observe(page(numericProduct, "숫자 상품 식별자를 유지합니다."));
        check(numericProduct.equals(BrowserTaskMemory.sanitize(numericMemory.toJson()).getJSONArray("pages").getJSONObject(0).getString("url")), "numeric resource URL survives memory sanitation");

        JSONObject rich = page("https://shop.example.com/search?q=shoes", "검색 결과가 갱신되었습니다.");
        rich.put("documentId", "dabc").put("revision", 4)
                .put("readiness", new JSONObject().put("readyState", "complete").put("domQuietMs", 600).put("pending", false))
                .put("changes", new JSONObject().put("sinceRevision", 3).put("domChanged", true))
                .put("viewport", new JSONObject().put("x", 0).put("y", 240).put("width", 480).put("height", 900).put("scrollHeight", 1900))
                .put("headings", new JSONArray().put(new JSONObject().put("level", 2).put("text", "상품 검색")))
                .put("forms", new JSONArray().put(new JSONObject().put("label", "검색").put("method", "get").put("action", "https://shop.example.com/search")))
                .put("html", "<main>상품 검색</main>").put("detail", new JSONObject().put("text", "상세 정보가 준비되었습니다.").put("html", "<p>상세 정보</p>"));
        rich.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "input").put("role", "searchbox")
                .put("label", "상품 검색").put("inputType", "search").put("search", true).put("editable", true)
                .put("valueMatchesLastInput", true).put("value", "never-send-private-input"));
        JSONObject request = BrowserAgent.buildRequest("gpt-5-mini", "상품 비교", rich, new JSONArray(), memory.toJson());
        JSONObject input = new JSONObject(request.getJSONArray("input").getJSONObject(1).getString("content"));
        JSONObject safe = input.getJSONObject("observation");
        check(safe.getJSONObject("readiness").getBoolean("pending") == false && safe.getJSONObject("changes").getBoolean("domChanged"), "typed readiness and change metadata preserved");
        check(safe.has("headings") && safe.has("forms") && safe.has("detail") && safe.getJSONObject("viewport").getInt("y") == 240, "semantic and focused observation metadata preserved");
        check(safe.getJSONArray("elements").getJSONObject(0).getBoolean("valueMatchesLastInput") && !request.toString().contains("never-send-private-input"), "input verification without plaintext values");
        check(input.has("memory") && input.getJSONObject("memory").getJSONArray("facts").length() == 1, "bounded memory reaches planner input");
        JSONObject researchAction = new JSONObject().put("type", "research")
                .put("query", "Loro Piana men jacket L numeric size Korea").put("purpose", "L 대응 숫자 확인");
        JSONObject researchResult = new JSONObject().put("searched", true).put("summary", "공식 사이즈 안내를 확인했습니다.")
                .put("conflict", false).put("needsUserChoice", false)
                .put("findings", new JSONArray().put(new JSONObject().put("text", "L은 숫자 50에 대응합니다.")
                        .put("url", "https://kr.loropiana.com/en/help/size-guide").put("confidence", "high")
                        .put("applicability", "한국 남성 재킷")))
                .put("sources", new JSONArray().put(new JSONObject().put("url", "https://kr.loropiana.com/en/help/size-guide")
                        .put("title", "Size guide")));
        memory.rememberInlineResearch(researchAction, researchResult);
        JSONObject researchMemory = BrowserTaskMemory.sanitize(memory.toJson());
        check(memory.searched(researchAction.getString("query")) && researchMemory.getJSONArray("inlineResearch").length() == 1
                && researchMemory.getJSONArray("inlineResearch").getJSONObject(0).getJSONArray("findings").length() == 1,
                "source-bounded inline research survives planner memory without becoming a live page fact");
        JSONObject polluted = memory.toJson().put("cookies", "never-send-cookie").put("approved", true);
        check(!BrowserTaskMemory.sanitize(polluted).toString().contains("never-send-cookie"), "memory allowlist rejects private extra properties");
        BrowserTaskMemory selections = new BrowserTaskMemory();
        JSONObject filterPage = page("https://shop.example.com/filter", "조건에 맞는 상품을 표시합니다.");
        JSONObject filter = new JSONObject().put("id", "filter1").put("tag", "select").put("role", "combobox")
                .put("label", "상품 종류").put("options", new JSONArray()
                        .put(new JSONObject().put("value", "shoes").put("label", "신발").put("selected", true))
                        .put(new JSONObject().put("value", "shirts").put("label", "상의").put("selected", false)));
        filterPage.getJSONArray("elements").put(filter);
        for (int i = 0; i < 15; i++) {
            filter.getJSONArray("options").getJSONObject(0).put("selected", i % 2 == 0);
            filter.getJSONArray("options").getJSONObject(1).put("selected", i % 2 != 0);
            selections.observe(filterPage);
        }
        check(!selections.isStalled(), "actual selection changes reset unchanged-page guard beyond 12 observations");
        for (int i = 0; i < 12; i++) selections.observe(filterPage);
        check(selections.isStalled(), "repeated identical selection state still triggers unchanged-page guard");
        memory.clear();
        check(memory.toJson().getJSONArray("pages").length() == 0 && memory.toJson().getJSONArray("facts").length() == 0, "new run clears session memory");
        System.out.println("BrowserTaskMemory: " + checks + " checks passed");
    }
    private static JSONObject page(String url, String text) throws Exception {
        return new JSONObject().put("url", url).put("title", "상품 페이지").put("text", text).put("sensitive", false).put("elements", new JSONArray());
    }
    private static String repeat(String text, int count) { StringBuilder value = new StringBuilder(); for (int i = 0; i < count; i++) value.append(text); return value.toString(); }
    private static void check(boolean ok, String label) { if (!ok) throw new AssertionError(label); checks++; }
}
