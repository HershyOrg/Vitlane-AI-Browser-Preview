package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

import java.net.URI;
import java.text.Normalizer;
import java.util.Locale;

/** Compares the research graph with the live browser state without treating the plan as truth. */
public final class BrowserExecutionVerifier {
    private BrowserExecutionVerifier() {}

    public static JSONObject inspect(JSONObject research, JSONObject observation) {
        JSONObject result = new JSONObject();
        try {
            result.put("state", "no_research").put("matchedStep", "")
                    .put("expectedNext", "").put("entryMatch", false);
            JSONObject plan = BrowserResearchPlanner.sanitize(research);
            JSONObject page = BrowserAgent.sanitizeObservation(observation);
            if (!plan.optBoolean("searched")) return result;
            if (page.optBoolean("sensitive")) return result.put("state", "sensitive");
            String current = page.optString("url"), entry = plan.optString("entryUrl");
            boolean entryMatch = sameHost(current, entry);
            result.put("entryMatch", entryMatch);
            String observed = normalize(page.optString("title") + "\n" + page.optString("text") + "\n" + page.optString("url"));
            JSONArray graph = plan.optJSONArray("executionGraph");
            int matchedIndex = -1, matchedLength = -1;
            if (graph != null) for (int i = 0; i < graph.length(); i++) {
                JSONArray signals = graph.getJSONObject(i).getJSONArray("successSignals");
                for (int j = 0; j < signals.length(); j++) {
                    String signal = normalize(signals.getString(j));
                    // Prefer the most specific visible phrase. This avoids treating "Add to Cart"
                    // as proof that the later generic "Cart" stage has already been reached.
                    if (signal.length() >= 3 && observed.contains(signal) && signal.length() > matchedLength) {
                        matchedIndex = i;
                        matchedLength = signal.length();
                    }
                }
            }
            if (matchedIndex >= 0) {
                result.put("state", "expected_stage").put("matchedStep", graph.getJSONObject(matchedIndex).getString("id"));
                if (matchedIndex + 1 < graph.length()) result.put("expectedNext", graph.getJSONObject(matchedIndex + 1).getString("expected"));
                return result;
            }
            if (looksLikeSearch(current)) return result.put("state", "fallback_search");
            if (looksLikeLogin(observed)) return result.put("state", "authentication");
            if (entryMatch) {
                if (graph != null && graph.length() > 0) result.put("expectedNext", graph.getJSONObject(0).getString("expected"));
                return result.put("state", "destination");
            }
            return result.put("state", "outside_route");
        } catch (Exception invalid) {
            return result;
        }
    }

    public static String actionOutcome(JSONObject action, JSONObject before, JSONObject after) {
        return BrowserObservationPolicy.outcome(action, before, after);
    }

    private static boolean sameHost(String first, String second) {
        if (first == null || second == null || first.isEmpty() || second.isEmpty()) return false;
        try {
            String a = new URI(first).getHost(), b = new URI(second).getHost();
            return a != null && b != null && a.equalsIgnoreCase(b);
        } catch (Exception ignored) { return false; }
    }

    private static boolean looksLikeSearch(String url) {
        try {
            String host = new URI(url).getHost().toLowerCase(Locale.ROOT);
            return host.contains("google.") || host.contains("bing.") || host.contains("search.");
        } catch (Exception ignored) { return false; }
    }

    private static boolean looksLikeLogin(String text) {
        return text.matches("(?is).*(?:로그인|로그 인|sign in|log in|계정으로 계속|continue with account).*");
    }

    private static String normalize(String text) {
        return Normalizer.normalize(text == null ? "" : text, Normalizer.Form.NFKC)
                .toLowerCase(Locale.ROOT).replaceAll("\\s+", " ").trim();
    }
}
