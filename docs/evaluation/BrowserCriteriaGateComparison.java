package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

/** Deterministic gate comparison. No LLM calls, live websites, or semantic-success claims. */
public final class BrowserCriteriaGateComparison {
    private static final String GOAL = "27인치 4K USB-C 65W 이상, 총액 500000원 이하인 모니터 3개를 찾아줘";
    private static final String[] RULES = {"size", "resolution", "power", "price"};
    private static final JSONArray RESULTS = new JSONArray();

    public static void main(String[] args) throws Exception {
        runComparison("insufficient_candidates", 1, false, false, false, true, false);
        runComparison("missing_mandatory_check", 3, true, false, false, true, false);
        runComparison("duplicate_selected_candidate", 3, false, true, false, true, false);
        runComparison("all_candidates_and_checks_present", 3, false, false, false, true, true);
        runCollection(false);
        runCollection(true);
        runComparison("semantic_counterexample_over_budget_marked_supported", 3, false, false, true, true, true);
        System.out.println(new JSONObject().put("fixtureVersion", 1)
                .put("measurement", "Deterministic validation gates only; not model or live-site task accuracy")
                .put("baseline", "0.4.0 quote-presence rule, exercised through unchanged BrowserTaskMemory.validateEvidence")
                .put("candidate", "0.5.0 quote-presence plus BrowserTaskContract.canFinish")
                .put("cases", RESULTS).toString(2));
    }

    private static void runComparison(String id, int count, boolean missingPower, boolean duplicate,
            boolean overBudget, boolean expectedBaseline, boolean expectedNew) throws Exception {
        BrowserTaskContract contract = new BrowserTaskContract(GOAL);
        JSONObject observation = page(0, overBudget);
        require(contract.apply(new JSONObject().put("requirements", rules()), observation, new JSONObject()), "initialize " + id);
        for (int i = 0; i < count; i++) {
            observation = page(i, overBudget);
            JSONArray checks = new JSONArray();
            for (String rule : RULES) {
                if (missingPower && i == 1 && "power".equals(rule)) continue;
                checks.put(new JSONObject().put("requirementId", rule).put("status", "supported")
                        .put("evidence", new JSONArray().put(quote(observation))));
            }
            JSONObject candidate = new JSONObject().put("id", "c" + i).put("name", name(i))
                    .put("identity", quote(observation)).put("checks", checks);
            require(contract.apply(new JSONObject().put("candidates", new JSONArray().put(candidate)), observation, new JSONObject()), "candidate " + id);
        }
        JSONArray selected = new JSONArray();
        for (int i = 0; i < count; i++) selected.put("c" + (duplicate && i == 1 ? 0 : i));
        boolean baseline = BrowserTaskMemory.validateEvidence(new JSONArray().put(quote(observation)), observation, new JSONObject()).length() == 1;
        boolean candidate = baseline && contract.canFinish(selected);
        require(baseline == expectedBaseline && candidate == expectedNew, "gate result " + id);
        RESULTS.put(new JSONObject().put("id", id).put("quoteOnlyAccepted", baseline).put("contractAccepted", candidate)
                .put("issues", contract.completionIssues(selected)).put("expectedGateBehaviorObserved", true)
                .put("interpretation", overBudget
                        ? "Both accept misleading model assessments with real quotes: numeric/semantic truth is not independently validated."
                        : candidate ? "Necessary structural evidence conditions present; semantic success still unmeasured."
                        : "New gate rejects a structurally incomplete or duplicated result that quote presence alone accepts."));
    }

    private static void runCollection(boolean includeBeta) throws Exception {
        BrowserTaskContract contract = new BrowserTaskContract("Alpha Monitor와 Beta Monitor를 비교해줘");
        JSONObject alpha = page(0, false), beta = page(1, false);
        JSONArray requirements = new JSONArray().put(rule("alpha", "Alpha Monitor").put("coverage", "any"))
                .put(rule("beta", "Beta Monitor").put("coverage", "any"));
        require(contract.apply(new JSONObject().put("requirements", requirements), alpha, new JSONObject()), "collection initialize");
        JSONArray selected = new JSONArray();
        for (int i = 0; i < (includeBeta ? 2 : 1); i++) {
            JSONObject observation = i == 0 ? alpha : beta;
            JSONObject check = new JSONObject().put("requirementId", i == 0 ? "alpha" : "beta").put("status", "supported")
                    .put("evidence", new JSONArray().put(quote(observation)));
            JSONObject item = new JSONObject().put("id", "c" + i).put("name", name(i)).put("identity", quote(observation))
                    .put("checks", new JSONArray().put(check));
            require(contract.apply(new JSONObject().put("candidates", new JSONArray().put(item)), observation, new JSONObject()), "collection candidate");
            selected.put("c" + i);
        }
        boolean baseline = BrowserTaskMemory.validateEvidence(new JSONArray().put(quote(alpha)), alpha, new JSONObject()).length() == 1;
        boolean candidate = baseline && contract.canFinish(selected);
        require(baseline && candidate == includeBeta, "collection result");
        RESULTS.put(new JSONObject().put("id", includeBeta ? "heterogeneous_collection_complete" : "heterogeneous_collection_missing_member")
                .put("quoteOnlyAccepted", baseline).put("contractAccepted", candidate)
                .put("issues", contract.completionIssues(selected)).put("expectedGateBehaviorObserved", true)
                .put("interpretation", "Collection-wide any requirements need support across the selected results, not on every candidate."));
    }

    private static JSONArray rules() throws Exception {
        return new JSONArray().put(rule("size", "27인치")).put(rule("resolution", "4K"))
                .put(rule("power", "USB-C 65W 이상")).put(rule("price", "총액 500000원 이하"));
    }
    private static JSONObject rule(String id, String quote) throws Exception {
        return new JSONObject().put("id", id).put("quote", quote).put("kind", "required");
    }
    private static String name(int i) { return new String[] {"Alpha Monitor", "Beta Monitor", "Gamma Monitor"}[i]; }
    private static JSONObject page(int i, boolean overBudget) throws Exception {
        return new JSONObject().put("url", "https://shop.example.com/products/" + i).put("title", "공개 모니터 상품")
                .put("text", name(i) + ": 27인치 4K USB-C 65W, 배송비 포함 총액 " + (overBudget ? "700000" : "490000") + "원입니다.")
                .put("sensitive", false).put("elements", new JSONArray());
    }
    private static JSONObject quote(JSONObject page) throws Exception {
        return new JSONObject().put("url", page.getString("url")).put("quote", page.getString("text"));
    }
    private static void require(boolean condition, String label) {
        if (!condition) throw new AssertionError(label);
    }
}
