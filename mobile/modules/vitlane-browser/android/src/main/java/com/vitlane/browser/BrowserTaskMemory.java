package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

import java.text.Normalizer;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.Map;

/** Per-browser-task RAM context, retained while paused. No credentials, input values or disk persistence. */
public final class BrowserTaskMemory {
    private static final int MAX_PAGES = 8;
    private static final int MAX_FACTS = 16;
    private final LinkedHashMap<String, JSONObject> pages = new LinkedHashMap<>();
    private final LinkedHashMap<String, JSONObject> facts = new LinkedHashMap<>();
    private JSONArray plan = new JSONArray();
    private JSONObject latest;
    private JSONObject lastOutcome;
    private JSONObject feedback;
    private JSONArray searches = new JSONArray();
    private JSONArray inlineResearch = new JSONArray();
    private String previousFingerprint = "";
    private int observations;
    private int unchanged;
    private int failures;

    public void clear() {
        pages.clear(); facts.clear(); plan = new JSONArray(); latest = null; lastOutcome = null; feedback = null;
        searches = new JSONArray(); inlineResearch = new JSONArray();
        previousFingerprint = ""; observations = 0; unchanged = 0; failures = 0;
    }

    /** A user-triggered resume starts a fresh recovery budget while retaining task evidence. */
    public void resetProgressWatchdog() {
        previousFingerprint = "";
        unchanged = 0;
        failures = 0;
    }

    /** Call after each fresh native observation; sensitive contents are never retained. */
    public void observe(JSONObject raw) throws Exception {
        JSONObject safe = BrowserAgent.sanitizeObservation(raw);
        if (safe.optBoolean("sensitive")) { latest = null; return; }
        latest = safe;
        observations++;
        String fingerprint = fingerprint(safe);
        unchanged = fingerprint.equals(previousFingerprint) ? unchanged + 1 : 0;
        previousFingerprint = fingerprint;
        String url = safe.getString("url");
        JSONObject page = new JSONObject().put("url", url).put("title", safe.getString("title"))
                .put("text", clip(observedText(safe), 1600)).put("observation", observations);
        pages.remove(url); pages.put(url, page); trim(pages, MAX_PAGES);
    }

    /** Records outcome, not a claim of semantic task completion. Apply state even after a rejected action. */
    public void feedback(String code) { feedback = BrowserRecoveryPolicy.feedback(code); }

    public boolean searched(String query) {
        String key = normalized(query).toLowerCase(java.util.Locale.ROOT);
        for (int i = 0; i < searches.length(); i++) {
            JSONObject search = searches.optJSONObject(i);
            if (search != null && key.equals(normalized(search.optString("query")).toLowerCase(java.util.Locale.ROOT))) return true;
        }
        return false;
    }

    public void rememberSearch(JSONObject action) {
        try {
            JSONObject search = new JSONObject().put("query", clean(action, "query", 300))
                    .put("purpose", clean(action, "purpose", 200));
            searches.put(search);
            if (searches.length() > 12) searches.remove(0);
        } catch (Exception ignored) { }
    }

    /** Retains source-bounded advisory lookup results without promoting them to live page facts. */
    public void rememberInlineResearch(JSONObject action, JSONObject rawResult) {
        try {
            JSONObject result = BrowserResearchPlanner.sanitizeInline(rawResult);
            JSONObject entry = new JSONObject().put("query", clean(action, "query", 300))
                    .put("purpose", clean(action, "purpose", 200))
                    .put("summary", result.getString("summary"))
                    .put("conflict", result.getBoolean("conflict"))
                    .put("needsUserChoice", result.getBoolean("needsUserChoice"))
                    .put("findings", new JSONArray(result.getJSONArray("findings").toString()))
                    .put("sources", new JSONArray(result.getJSONArray("sources").toString()));
            inlineResearch.put(entry);
            if (inlineResearch.length() > 6) inlineResearch.remove(0);
            rememberSearch(action);
        } catch (Exception ignored) { }
    }

    public void record(JSONObject action, String status, String message) {
        try { recordInternal(action, status, message); }
        catch (Exception ignored) { /* Never let malformed model metadata interrupt the native loop. */ }
    }

    private void recordInternal(JSONObject action, String status, String message) throws Exception {
        if (action == null) return;
        if (!("applied".equals(status) || "rejected".equals(status) || "handoff".equals(status) || "unknown".equals(status)))
            throw new IllegalArgumentException("Invalid outcome");
        JSONObject state = action.optJSONObject("state");
        try { if (state != null && latest != null) {
            JSONObject safe = validateState(state, latest, toJson());
            if (safe.has("plan")) plan = safe.getJSONArray("plan");
            JSONArray updates = safe.optJSONArray("facts");
            if (updates != null) for (int i = 0; i < updates.length(); i++) {
                JSONObject fact = updates.getJSONObject(i);
                String key = fact.getString("url") + "\n" + fact.getString("text");
                facts.remove(key); facts.put(key, fact); trim(facts, MAX_FACTS);
            }
        } } catch (Exception ignored) { /* Invalid state is discarded, but the native outcome is retained. */ }
        if ("applied".equals(status)) feedback = null;
        if (!"unknown".equals(status)) failures = "rejected".equals(status) ? failures + 1 : 0;
        lastOutcome = new JSONObject().put("type", clean(action, "type", 20))
                .put("status", status).put("message", BrowserAgent.publicText(clip(message == null ? "" : message, 500)));
        if (action.has("targetId")) {
            String targetId = clean(action, "targetId", 80);
            if (targetId.matches("[A-Za-z0-9][A-Za-z0-9_-]{0,79}")) lastOutcome.put("targetId", targetId);
        }
        if ("click".equals(action.optString("type"))) {
            String method = action.optString("method", "dom");
            if ("dom".equals(method) || "direct".equals(method) || "native".equals(method)) lastOutcome.put("method", method);
        }
        if (latest != null) lastOutcome.put("url", latest.getString("url"));
    }

    /** Advisory only. A quiet page or elapsed wait is not proof of failure or completion. */
    public boolean isStalled() { return unchanged >= 12 || failures >= 3; }

    public JSONObject toJson() {
        try { return snapshot(); }
        catch (Exception ignored) { return new JSONObject(); }
    }

    private JSONObject snapshot() throws Exception {
        JSONObject result = new JSONObject().put("plan", new JSONArray(plan.toString()))
                .put("pages", array(pages)).put("facts", array(facts))
                .put("observations", observations).put("unchangedObservations", unchanged).put("consecutiveFailures", failures);
        result.put("searches", new JSONArray(searches.toString()));
        result.put("inlineResearch", new JSONArray(inlineResearch.toString()));
        if (feedback != null) result.put("feedback", new JSONObject(feedback.toString()));
        if (lastOutcome != null) result.put("lastOutcome", new JSONObject(lastOutcome.toString()));
        return result;
    }

    /** Reapply bounds/allowlists at the network boundary, including caller-provided context. */
    static JSONObject sanitize(JSONObject raw) throws Exception {
        JSONObject safe = new JSONObject();
        if (raw == null) return safe;
        if (raw.has("feedback")) safe.put("feedback", BrowserRecoveryPolicy.feedback(raw.getJSONObject("feedback").optString("code")));
        if (raw.has("searches")) {
            JSONArray source = raw.getJSONArray("searches"), searches = new JSONArray();
            if (source.length() > 12) throw new IllegalArgumentException("Too many searches");
            for (int i = 0; i < source.length(); i++) {
                JSONObject item = source.getJSONObject(i);
                searches.put(new JSONObject().put("query", clean(item, "query", 300)).put("purpose", clean(item, "purpose", 200)));
            }
            safe.put("searches", searches);
        }
        if (raw.has("inlineResearch")) {
            JSONArray source = raw.getJSONArray("inlineResearch"), research = new JSONArray();
            if (source.length() > 6) throw new IllegalArgumentException("Too many inline research entries");
            for (int i = 0; i < source.length(); i++) {
                JSONObject item = source.getJSONObject(i);
                allowed(item, "query", "purpose", "summary", "conflict", "needsUserChoice", "findings", "sources");
                JSONObject result = new JSONObject().put("searched", true)
                        .put("summary", clean(item, "summary", 700))
                        .put("conflict", item.getBoolean("conflict"))
                        .put("needsUserChoice", item.getBoolean("needsUserChoice"))
                        .put("findings", item.getJSONArray("findings"))
                        .put("sources", item.getJSONArray("sources"));
                JSONObject bounded = BrowserResearchPlanner.sanitizeInline(result);
                research.put(new JSONObject().put("query", clean(item, "query", 300))
                        .put("purpose", clean(item, "purpose", 200))
                        .put("summary", bounded.getString("summary"))
                        .put("conflict", bounded.getBoolean("conflict"))
                        .put("needsUserChoice", bounded.getBoolean("needsUserChoice"))
                        .put("findings", bounded.getJSONArray("findings"))
                        .put("sources", bounded.getJSONArray("sources")));
            }
            safe.put("inlineResearch", research);
        }
        if (raw.has("plan")) safe.put("plan", cleanPlan(raw.getJSONArray("plan")));
        if (raw.has("pages")) {
            JSONArray source = raw.getJSONArray("pages"), pages = new JSONArray();
            if (source.length() > MAX_PAGES) throw new IllegalArgumentException("Memory pages exceed limit");
            for (int i = 0; i < source.length(); i++) {
                JSONObject page = source.getJSONObject(i);
                pages.put(new JSONObject().put("url", BrowserAgent.safeUrl(clean(page, "url", 4096)))
                        .put("title", clean(page, "title", 300)).put("text", clean(page, "text", 1600)));
            }
            safe.put("pages", pages);
        }
        if (raw.has("facts")) {
            JSONArray source = raw.getJSONArray("facts"), facts = new JSONArray();
            if (source.length() > MAX_FACTS) throw new IllegalArgumentException("Memory facts exceed limit");
            for (int i = 0; i < source.length(); i++) facts.put(cleanFact(source.getJSONObject(i)));
            safe.put("facts", facts);
        }
        for (String key : new String[] {"observations", "unchangedObservations", "consecutiveFailures"}) {
            if (!raw.has(key)) continue;
            Object value = raw.get(key);
            if (!(value instanceof Number) || ((Number) value).longValue() < 0 || ((Number) value).longValue() > 10000)
                throw new IllegalArgumentException("Invalid memory counter");
            safe.put(key, ((Number) value).intValue());
        }
        if (raw.has("lastOutcome")) {
            JSONObject result = raw.getJSONObject("lastOutcome");
            JSONObject clean = new JSONObject().put("type", clean(result, "type", 20))
                    .put("status", clean(result, "status", 20)).put("message", clean(result, "message", 500));
            if (result.has("url")) clean.put("url", BrowserAgent.safeUrl(clean(result, "url", 4096)));
            if (result.has("targetId")) {
                String targetId = clean(result, "targetId", 80);
                if (!targetId.matches("[A-Za-z0-9][A-Za-z0-9_-]{0,79}")) throw new IllegalArgumentException("Invalid outcome target");
                clean.put("targetId", targetId);
            }
            if (result.has("method")) {
                String method = clean(result, "method", 12);
                if (!("dom".equals(method) || "direct".equals(method) || "native".equals(method)))
                    throw new IllegalArgumentException("Invalid outcome method");
                clean.put("method", method);
            }
            safe.put("lastOutcome", clean);
        }
        return safe;
    }

    static JSONObject validateState(JSONObject raw, JSONObject observation, JSONObject memory) throws Exception {
        allowed(raw, "plan", "facts");
        JSONObject safe = new JSONObject();
        if (raw.has("plan")) safe.put("plan", cleanPlan(raw.getJSONArray("plan")));
        if (raw.has("facts")) {
            JSONArray source = raw.getJSONArray("facts"), facts = new JSONArray();
            if (source.length() > 8) throw new IllegalArgumentException("Too many fact updates");
            JSONObject context = sanitize(memory);
            for (int i = 0; i < source.length(); i++) {
                JSONObject fact = cleanFact(source.getJSONObject(i));
                if (grounded(fact.getString("url"), fact.getString("evidence"), observation, context)) facts.put(fact);
            }
            safe.put("facts", facts);
        }
        return safe;
    }

    /** Quote presence is a provenance check, NOT a correctness or task-success oracle. */
    static JSONArray validateEvidence(JSONArray source, JSONObject observation, JSONObject memory) throws Exception {
        JSONArray clean = new JSONArray();
        if (source == null || source.length() < 1 || source.length() > 6) return clean;
        JSONObject context = sanitize(memory);
        for (int i = 0; i < source.length(); i++) {
            JSONObject evidence = source.getJSONObject(i);
            allowed(evidence, "url", "quote");
            String url = BrowserAgent.safeUrl(clean(evidence, "url", 4096));
            String quote = clean(evidence, "quote", 800);
            if (!grounded(url, quote, observation, context)) return new JSONArray();
            clean.put(new JSONObject().put("url", url).put("quote", quote));
        }
        return clean;
    }

    private static boolean grounded(String url, String quote, JSONObject current, JSONObject context) throws Exception {
        String normalized = normalized(quote);
        if (normalized.length() < 8 || normalized.contains("[비공개]")) return false;
        if (current != null && url.equals(current.optString("url")) && normalized(observedText(current)).contains(normalized)) return true;
        JSONArray pages = context.optJSONArray("pages");
        if (pages != null) for (int i = 0; i < pages.length(); i++) {
            JSONObject page = pages.getJSONObject(i);
            if (url.equals(page.getString("url")) && normalized(page.getString("title") + "\n" + page.getString("text")).contains(normalized)) return true;
        }
        JSONArray facts = context.optJSONArray("facts");
        if (facts != null) for (int i = 0; i < facts.length(); i++) {
            JSONObject fact = facts.getJSONObject(i);
            if (url.equals(fact.getString("url")) && normalized(fact.getString("evidence")).contains(normalized)) return true;
        }
        return false;
    }

    private static String observedText(JSONObject observation) throws Exception {
        StringBuilder text = new StringBuilder(observation.optString("title")).append('\n').append(observation.optString("text"));
        JSONObject detail = observation.optJSONObject("detail");
        if (detail != null) text.append('\n').append(detail.optString("text"));
        JSONArray elements = observation.optJSONArray("elements");
        if (elements != null) for (int i = 0; i < elements.length(); i++) {
            JSONObject element = elements.getJSONObject(i);
            text.append('\n').append(element.optString("label"))
                    .append('\n').append(element.optString("group")).append('\n').append(element.optString("context"));
            JSONArray options = element.optJSONArray("options");
            if (options != null) for (int j = 0; j < options.length(); j++) text.append('\n').append(options.getJSONObject(j).optString("label"));
        }
        return text.toString();
    }

    private static String fingerprint(JSONObject safe) throws Exception {
        StringBuilder value = new StringBuilder(safe.getString("url")).append('\n').append(safe.optString("text"));
        JSONObject viewport = safe.optJSONObject("viewport");
        if (viewport != null) value.append('|').append(viewport.optDouble("x")).append('|').append(viewport.optDouble("y"));
        JSONArray elements = safe.getJSONArray("elements");
        for (int i = 0; i < elements.length(); i++) {
            JSONObject item = elements.getJSONObject(i);
            // Deliberately exclude volatile target IDs and mutation revisions.
            value.append('|').append(item.optString("group")).append('|').append(item.optString("context"))
                    .append('|').append(item.optString("label")).append('|').append(item.optString("href"));
            for (String flag : new String[] {"checked", "expanded", "selected", "disabled", "valueMatchesLastInput"}) value.append('|').append(item.opt(flag));
            value.append("|scrollTop:").append(item.opt("scrollTop"));
            JSONArray options = item.optJSONArray("options");
            if (options != null) for (int j = 0; j < options.length(); j++) {
                JSONObject option = options.getJSONObject(j);
                if (option.optBoolean("selected")) value.append("|selected:").append(option.optString("value"));
            }
        }
        JSONArray personalDataApplied = safe.optJSONArray("personalDataApplied");
        if (personalDataApplied != null) value.append("|personalDataApplied:").append(personalDataApplied);
        return value.toString();
    }

    private static JSONObject cleanFact(JSONObject raw) throws Exception {
        allowed(raw, "text", "url", "evidence");
        return new JSONObject().put("text", clean(raw, "text", 500)).put("url", BrowserAgent.safeUrl(clean(raw, "url", 4096)))
                .put("evidence", clean(raw, "evidence", 800));
    }

    private static JSONArray cleanPlan(JSONArray raw) throws Exception {
        if (raw.length() > 8) throw new IllegalArgumentException("Too many plan steps");
        JSONArray safe = new JSONArray();
        for (int i = 0; i < raw.length(); i++) {
            Object value = raw.get(i);
            if (!(value instanceof String) || ((String) value).length() > 200) throw new IllegalArgumentException("Invalid plan step");
            safe.put(BrowserAgent.publicText((String) value));
        }
        return safe;
    }

    private static String clean(JSONObject raw, String key, int max) throws Exception {
        Object value = raw.get(key);
        if (!(value instanceof String) || ((String) value).length() > max || ((String) value).matches("(?s).*[\\x00-\\x08\\x0B\\x0C\\x0E-\\x1F].*"))
            throw new IllegalArgumentException("Invalid memory value");
        // URL privacy is handled structurally by safeUrl; digit-only public product IDs must survive.
        return "url".equals(key) ? (String) value : BrowserAgent.publicText((String) value);
    }

    private static void allowed(JSONObject raw, String... keys) {
        java.util.Set<String> allowed = new java.util.HashSet<>(java.util.Arrays.asList(keys));
        Iterator<String> actual = raw.keys();
        while (actual.hasNext()) if (!allowed.contains(actual.next())) throw new IllegalArgumentException("Unsupported memory property");
    }
    private static String normalized(String text) { return Normalizer.normalize(text, Normalizer.Form.NFKC).replaceAll("\\s+", " ").trim(); }
    private static String clip(String text, int max) { return text.length() <= max ? text : text.substring(0, max); }
    private static void trim(LinkedHashMap<String, JSONObject> map, int max) { while (map.size() > max) map.remove(map.keySet().iterator().next()); }
    private static JSONArray array(Map<String, JSONObject> values) throws Exception {
        JSONArray array = new JSONArray();
        for (JSONObject value : values.values()) array.put(new JSONObject(value.toString()));
        return array;
    }
}
