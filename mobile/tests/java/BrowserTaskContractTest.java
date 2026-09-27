package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

/** Multi-page candidate completeness, immutable goals, source retention and malformed update regressions. */
public final class BrowserTaskContractTest {
    private static int checks;
    private static final String GOAL = "27인치 4K 모니터 중 USB-C 65W 이상, 50만 원 이하 제품 세 개를 찾아줘. 구매는 하지 마.";

    public static void main(String[] args) throws Exception {
        BrowserTaskContract task = new BrowserTaskContract(GOAL);
        JSONObject first = page("https://shop.example.com/products/one", "Alpha Monitor: 27인치 4K, USB-C 65W, 총액 490000원입니다.");
        check(task.toJson().getInt("targetCount") == 3, "Korean textual requested result count is preserved");
        check(!task.canFinish(new JSONArray()), "uninitialized contract cannot finish");
        check(task.apply(initial(), first, new JSONObject()), "criteria initialize from immutable goal quotes");
        check(task.toJson().getBoolean("initialized") && task.toJson().getString("goal").equals(GOAL), "original goal retained with initialized state");
        check(task.toJson().getJSONArray("scope").getString(0).equals("구매는 하지 마"), "prohibition stored separately from per-candidate requirements");
        check(!task.apply(new JSONObject().put("requirements", new JSONArray().put(rule("size", "27인치"))), first, new JSONObject()), "criteria cannot be dropped after initialization");
        check(task.toJson().getJSONArray("requirements").length() == 4 && !task.toJson().getString("lastError").isEmpty(), "failed update is atomic and exposes bounded feedback");
        JSONObject changed = initial();
        changed.getJSONArray("requirements").getJSONObject(3).put("quote", "제품");
        check(!task.apply(changed, first, new JSONObject()), "source-present but weakened replacement criterion rejected");
        check(task.apply(new JSONObject().put("candidates", new JSONArray().put(candidate("a", "Alpha Monitor", first, false))), first, new JSONObject()), "candidate identity grounded in current page");
        check("unknown".equals(task.toJson().getJSONArray("candidates").getJSONObject(0).getJSONArray("checks").getJSONObject(0).getString("status")), "missing checks start unknown");
        check(!task.canFinish(new JSONArray().put("a")), "one candidate cannot satisfy request for three");
        JSONObject updateA = new JSONObject().put("id", "a").put("checks", allChecks(first, "supported"));
        check(task.apply(new JSONObject().put("candidates", new JSONArray().put(updateA)), first, new JSONObject()), "per-condition evidence accepted");

        JSONObject second = page("https://shop.example.com/products/two", "Beta Monitor: 27인치 4K, USB-C 65W, 총액 480000원입니다.");
        JSONObject third = page("https://shop.example.com/products/three", "Gamma Monitor: 27인치 4K, USB-C 65W, 총액 470000원입니다.");
        check(task.apply(new JSONObject().put("candidates", new JSONArray().put(candidate("b", "Beta Monitor", second, true))), second, new JSONObject()), "second candidate page");
        check(task.apply(new JSONObject().put("candidates", new JSONArray().put(candidate("c", "Gamma Monitor", third, true))), third, new JSONObject()), "third candidate page");
        JSONArray selected = new JSONArray().put("a").put("b").put("c");
        check(task.canFinish(selected), "all distinct candidates and required evidence pass necessary-condition gate");
        check(!task.canFinish(new JSONArray().put("a").put("a").put("c")), "repeated candidate IDs cannot inflate count");
        check(!task.canFinish(new JSONArray().put("a").put("b").put("invented")), "unrecorded candidate cannot finish");
        check(!task.apply(new JSONObject().put("candidates", new JSONArray().put(candidate("a2", "Alpha Monitor", first, true))), first, new JSONObject()), "same source and normalized name cannot become a second identity");
        check(!task.apply(new JSONObject().put("candidates", new JSONArray().put(new JSONObject().put("id", "a").put("name", "Gamma Monitor"))), third, new JSONObject()), "existing identity cannot be repurposed");

        JSONObject invalidEvidence = new JSONObject().put("id", "a").put("checks", new JSONArray().put(check("power", "supported",
                new JSONObject().put("url", first.getString("url")).put("quote", "이 모니터는 USB-C 100W를 제공합니다."))));
        check(!task.apply(new JSONObject().put("candidates", new JSONArray().put(invalidEvidence)), first, new JSONObject()), "invented numeric evidence is rejected");
        check(task.canFinish(selected), "invalid update did not corrupt previously grounded ledger");
        JSONObject unknown = new JSONObject().put("id", "a").put("checks", new JSONArray().put(new JSONObject()
                .put("requirementId", "power").put("status", "unknown")));
        check(task.apply(new JSONObject().put("candidates", new JSONArray().put(unknown)), third, new JSONObject())
                && !task.canFinish(selected), "explicit unknown clears prior support and blocks completion");
        // The source page has left both current observation and rolling memory, but original ledger proof remains.
        check(task.apply(new JSONObject().put("candidates", new JSONArray().put(updateA)), third, new JSONObject())
                && task.canFinish(selected), "previous validated identity quote supports updates after page memory eviction");
        check(BrowserTaskContract.validateFinishEvidence(new JSONArray().put(quote(first)), third, new JSONObject(), task.toJson()).length() == 1,
                "final source evidence survives eviction from rolling page and fact memory");
        check(BrowserTaskContract.validateFinishEvidence(new JSONArray().put(new JSONObject().put("url", first.getString("url"))
                .put("quote", "허위로 작성한 제품 사양 근거입니다.")), third, new JSONObject(), task.toJson()).length() == 0,
                "native ledger cannot support invented final quote");
        JSONObject badSource = new JSONObject(updateA.toString());
        badSource.getJSONArray("checks").getJSONObject(0).getJSONArray("evidence").getJSONObject(0).put("url", "https://other.example.com/source");
        check(!task.apply(new JSONObject().put("candidates", new JSONArray().put(badSource)), third, new JSONObject()), "retained quote cannot be attributed to a different source");
        for (String status : new String[] {"contradicted", "unavailable"}) {
            JSONObject negative = new JSONObject().put("id", "a").put("checks", new JSONArray().put(check("power", status, quote(first))));
            check(task.apply(new JSONObject().put("candidates", new JSONArray().put(negative)), first, new JSONObject()) && !task.canFinish(selected), status + " blocks required completion");
        }
        JSONObject privateObservation = new JSONObject(first.toString()).put("sensitive", true).put("text", "never-send-private");
        check(!task.apply(new JSONObject(), privateObservation, new JSONObject()) && !task.toJson().toString().contains("never-send-private"), "sensitive observation is never retained");
        JSONObject detached = task.toJson();
        detached.getJSONArray("requirements").getJSONObject(0).put("quote", "external mutation");
        check(!task.toJson().toString().contains("external mutation"), "snapshot does not mutate live contract");

        BrowserTaskContract missingNumeric = new BrowserTaskContract(GOAL);
        JSONObject incomplete = initial();
        incomplete.getJSONArray("requirements").remove(2);
        check(!missingNumeric.apply(incomplete, first, new JSONObject()) && !missingNumeric.toJson().getBoolean("initialized"), "missing original numeric condition rejects initial contract atomically");
        BrowserTaskContract inventedRule = new BrowserTaskContract(GOAL);
        JSONObject invention = initial();
        invention.getJSONArray("requirements").getJSONObject(0).put("quote", "32인치");
        check(!inventedRule.apply(invention, first, new JSONObject()), "criteria must quote actual original goal");
        BrowserTaskContract preferences = new BrowserTaskContract("27인치 모니터를 찾아줘. 가능하면 검정색을 선호해.");
        JSONObject preferenceRules = new JSONObject().put("requirements", new JSONArray().put(rule("size", "27인치"))
                .put(new JSONObject().put("id", "color").put("quote", "가능하면 검정색을 선호해").put("kind", "preference")));
        check(preferences.apply(preferenceRules, first, new JSONObject()), "explicit source preference allowed alongside required criterion");
        BrowserTaskContract allPreferences = new BrowserTaskContract("가능하면 검정색을 선호해");
        check(!allPreferences.apply(new JSONObject().put("requirements", new JSONArray().put(new JSONObject()
                .put("id", "color").put("quote", "가능하면 검정색을 선호해").put("kind", "preference"))), first, new JSONObject()), "all-preference initialization cannot bypass required checks");
        BrowserTaskContract downgraded = new BrowserTaskContract("27인치 모니터를 찾아줘");
        check(!downgraded.apply(new JSONObject().put("requirements", new JSONArray().put(rule("size", "27인치").put("kind", "preference"))), first, new JSONObject()), "required numeric condition cannot be relabeled preference without source wording");
        BrowserTaskContract fakeScope = new BrowserTaskContract("27인치 4K 모니터를 찾아줘");
        check(!fakeScope.apply(new JSONObject().put("requirements", new JSONArray().put(rule("size", "27인치")))
                .put("scope", new JSONArray().put("4K")), first, new JSONObject()), "criteria cannot be hidden in scope without permission/prohibition wording");
        check(!new BrowserTaskContract(GOAL).apply(new JSONObject().put("requirements", new JSONArray().put(rule("find", "찾아줘")))
                .put("scope", new JSONArray().put(GOAL)), first, new JSONObject()), "whole goal in scope cannot hide numeric product requirements");

        check(new BrowserTaskContract("3개의 제품을 찾아줘").toJson().getInt("targetCount") == 3, "Korean attributive result count");
        check(new BrowserTaskContract("Find three distinct monitors below 500 dollars").toJson().getInt("targetCount") == 3, "English result count does not parse monetary budget");
        check(new BrowserTaskContract("가격 500000원 이하 제품을 찾아줘").toJson().getInt("targetCount") == 1, "price is not mistaken for result count");
        check(new BrowserTaskContract("USB 포트 2개가 있는 모니터 3개를 찾아줘").toJson().getInt("targetCount") == 3
                && !new BrowserTaskContract("USB 포트 2개가 있는 모니터 3개를 찾아줘").toJson().getBoolean("unsupported"),
                "attribute count is separate from requested result count");
        check(new BrowserTaskContract("방 2개 있는 호텔 찾아줘").toJson().getInt("targetCount") == 1,
                "room attribute count does not force two hotel candidates");
        check(new BrowserTaskContract("제품 2개와 제품 3개를 비교해줘").toJson().getBoolean("unsupported"), "ambiguous multiple result counts require handoff");
        check(new BrowserTaskContract("제품 20개 찾아줘").toJson().getBoolean("unsupported"), "over-limit count cannot silently reduce to one");
        check(new BrowserTaskContract("제품 1000개 찾아줘").toJson().getBoolean("unsupported"), "four-digit Korean count cannot fall back to one");
        check(new BrowserTaskContract("Find 1000 products").toJson().getBoolean("unsupported"), "four-digit English count cannot fall back to one");
        check(!new BrowserTaskContract("").canFinish(new JSONArray().put("x")), "empty goal cannot finish");
        JSONObject unsupported = new JSONObject(initial().toString()).put("approved", true);
        check(!new BrowserTaskContract(GOAL).apply(unsupported, first, new JSONObject()), "metadata cannot carry approval flags");
        JSONObject tooLongQuote = new JSONObject().put("url", first.getString("url")).put("quote", repeat("a", 401));
        JSONObject longCandidate = candidate("long", "Alpha Monitor", first, false).put("identity", tooLongQuote);
        check(!task.apply(new JSONObject().put("candidates", new JSONArray().put(longCandidate)), first, new JSONObject()), "individual evidence quote bounded to 400 characters");
        check(task.toJson().toString().getBytes(java.nio.charset.StandardCharsets.UTF_8).length <= 122880, "task snapshot within byte budget");
        BrowserTaskContract compare = new BrowserTaskContract("Alpha Monitor와 Beta Monitor를 비교해줘.");
        JSONObject collectionRules = new JSONObject().put("requirements", new JSONArray()
                .put(rule("alpha", "Alpha Monitor").put("coverage", "any"))
                .put(rule("beta", "Beta Monitor").put("coverage", "any")));
        check(compare.apply(collectionRules, first, new JSONObject()), "heterogeneous comparison initializes collection criteria");
        JSONObject alpha = candidate("a", "Alpha Monitor", first, false).put("checks", new JSONArray().put(check("alpha", "supported", quote(first))));
        JSONObject beta = candidate("b", "Beta Monitor", second, false).put("checks", new JSONArray().put(check("beta", "supported", quote(second))));
        check(compare.apply(new JSONObject().put("candidates", new JSONArray().put(alpha)), first, new JSONObject())
                && !compare.canFinish(new JSONArray().put("a")), "one collection member cannot satisfy missing other member");
        check(compare.apply(new JSONObject().put("candidates", new JSONArray().put(beta)), second, new JSONObject())
                && compare.canFinish(new JSONArray().put("a").put("b")), "A and B evidence belongs to distinct candidates rather than requiring each to be both");
        JSONObject weakenedCoverage = initial();
        weakenedCoverage.getJSONArray("requirements").getJSONObject(0).put("coverage", "any");
        check(!task.apply(weakenedCoverage, first, new JSONObject()), "each-required criterion cannot be weakened to collection coverage after initialization");
        StringBuilder manyGoal = new StringBuilder();
        JSONArray manyRules = new JSONArray();
        for (int i = 0; i < 12; i++) {
            String condition = "조건" + (char) ('A' + i);
            manyGoal.append(condition).append(' ');
            manyRules.put(rule("r" + i, condition));
        }
        BrowserTaskContract bounded = new BrowserTaskContract(manyGoal.toString());
        String longUrl = "https://shop.example.com/" + repeat("resource", 450);
        JSONObject largePage = page(longUrl, "LargeCandidateA " + repeat("a", 370) + " 두 번째 관찰 문장 " + repeat("b", 370));
        check(bounded.apply(new JSONObject().put("requirements", manyRules), largePage, new JSONObject()), "large-ledger budget fixture initialized");
        JSONObject largeCandidate = new JSONObject().put("id", "largeA").put("name", "LargeCandidateA")
                .put("identity", new JSONObject().put("url", longUrl).put("quote", "LargeCandidateA " + repeat("a", 370)));
        JSONArray manyChecks = new JSONArray();
        for (int i = 0; i < 12; i++) manyChecks.put(new JSONObject().put("requirementId", "r" + i).put("status", "supported")
                .put("evidence", new JSONArray()
                        .put(new JSONObject().put("url", longUrl).put("quote", "LargeCandidateA " + repeat("a", 370)))
                        .put(new JSONObject().put("url", longUrl).put("quote", "두 번째 관찰 문장 " + repeat("b", 370)))));
        largeCandidate.put("checks", manyChecks);
        check(bounded.apply(new JSONObject().put("candidates", new JSONArray().put(largeCandidate)), largePage, new JSONObject()), "first large candidate remains within byte budget");
        JSONObject overflow = new JSONObject(largeCandidate.toString()).put("id", "largeB").put("name", "LargeCandidateB");
        overflow.getJSONObject("identity").put("quote", "LargeCandidateB " + repeat("a", 370));
        JSONObject overflowPage = page(longUrl, "LargeCandidateB " + repeat("a", 370) + " 두 번째 관찰 문장 " + repeat("b", 370));
        check(!bounded.apply(new JSONObject().put("candidates", new JSONArray().put(overflow)), overflowPage, new JSONObject())
                && bounded.toJson().getJSONArray("candidates").length() == 1, "oversized update rejected atomically without evicting prior candidate evidence");
        check(bounded.toJson().toString().getBytes(java.nio.charset.StandardCharsets.UTF_8).length <= 122880, "snapshot remains byte-bounded after overflow feedback");
        System.out.println("BrowserTaskContract: " + checks + " checks passed");
    }

    private static JSONObject initial() throws Exception {
        return new JSONObject().put("requirements", new JSONArray().put(rule("size", "27인치")).put(rule("resolution", "4K"))
                .put(rule("power", "USB-C 65W 이상")).put(rule("price", "50만 원 이하")))
                .put("scope", new JSONArray().put("구매는 하지 마"));
    }
    private static JSONObject rule(String id, String quote) throws Exception { return new JSONObject().put("id", id).put("quote", quote).put("kind", "required"); }
    private static JSONObject page(String url, String text) throws Exception { return new JSONObject().put("url", url).put("title", "공개 모니터 상품").put("text", text).put("sensitive", false).put("elements", new JSONArray()); }
    private static JSONObject quote(JSONObject page) throws Exception { return new JSONObject().put("url", page.getString("url")).put("quote", page.getString("text")); }
    private static JSONObject candidate(String id, String name, JSONObject page, boolean supported) throws Exception {
        JSONObject candidate = new JSONObject().put("id", id).put("name", name).put("identity", quote(page));
        if (supported) candidate.put("checks", allChecks(page, "supported"));
        return candidate;
    }
    private static JSONArray allChecks(JSONObject page, String status) throws Exception {
        JSONArray result = new JSONArray();
        for (String id : new String[] {"size", "resolution", "power", "price"}) result.put(check(id, status, quote(page)));
        return result;
    }
    private static JSONObject check(String id, String status, JSONObject quote) throws Exception { return new JSONObject().put("requirementId", id).put("status", status).put("evidence", new JSONArray().put(quote)); }
    private static String repeat(String text, int count) { StringBuilder result = new StringBuilder(); for (int i = 0; i < count; i++) result.append(text); return result.toString(); }
    private static void check(boolean condition, String message) { if (!condition) throw new AssertionError(message); checks++; }
}
