package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

/** Observable postconditions, not a claim that an arbitrary user goal is complete. */
public final class BrowserObservationPolicy {
    private BrowserObservationPolicy() {}

    public static boolean ready(JSONObject observation, long waitedMillis) {
        JSONObject state = observation.optJSONObject("readiness");
        if (state == null) return true; // Compatibility with a page adapter from an older document.
        if (!"complete".equals(state.optString("readyState"))) return false;
        if (waitedMillis >= 12000) return true; // Live tickers must not block every observation forever.
        return !state.optBoolean("pending") && state.optLong("domQuietMs", 0) >= 700
                && state.optLong("resourceQuietMs", 1000) >= 600;
    }

    public static boolean documentChanged(JSONObject before, JSONObject after) {
        if (before == null || after == null) return false;
        return !before.optString("url").equals(after.optString("url"))
                || !before.optString("documentId").equals(after.optString("documentId"));
    }

    public static String outcome(JSONObject action, JSONObject before, JSONObject after) {
        if (before == null || after == null) return "unknown";
        if (after.optBoolean("sensitive", true)) return "unknown";
        String type = action.optString("type");
        boolean navigated = documentChanged(before, after);
        JSONObject previousTarget = target(before, action.optString("targetId"));
        JSONObject target = correspondingTarget(after, action.optString("targetId"), previousTarget);
        if ("type".equals(type) && !action.optBoolean("submit")) {
            return target != null && target.optBoolean("valueMatchesLastInput") ? "applied" : "unknown";
        }
        if ("select".equals(type)) {
            JSONArray options = target == null ? null : target.optJSONArray("options");
            if (options != null) for (int i = 0; i < options.length(); i++) {
                JSONObject option = options.optJSONObject(i);
                if (option != null && option.optBoolean("selected")
                        && (action.optString("value").equals(option.optString("value"))
                        || action.optString("value").equals(option.optString("label")))) return "applied";
            }
            return "unknown";
        }
        if ("check".equals(type)) {
            return target != null && target.has("checked")
                    && target.optBoolean("checked") == action.optBoolean("checked") ? "applied" : "unknown";
        }
        if ("scroll".equals(type)) {
            if (previousTarget != null && target != null && previousTarget.has("scrollTop") && target.has("scrollTop")
                    && previousTarget.optDouble("scrollTop") != target.optDouble("scrollTop")) return "applied";
            JSONObject previousViewport = before.optJSONObject("viewport"), nextViewport = after.optJSONObject("viewport");
            if (previousViewport != null && nextViewport != null
                    && previousViewport.optDouble("y") != nextViewport.optDouble("y")) return "applied";
            return "unknown";
        }
        if ("navigate".equals(type) || "back".equals(type) || "search".equals(type)
                || ("type".equals(type) && action.optBoolean("submit"))) {
            return navigated ? "applied" : "unknown";
        }
        if ("click".equals(type)) {
            // A new document is a concrete click effect. Within one document, require the clicked
            // control itself to change or a newly visible dialog to name that exact activation;
            // unrelated ads, clocks and result counters are not proof.
            if (navigated) return "applied";
            return interactiveStateChanged(previousTarget, target)
                    || newInterruptTriggeredBy(action.optString("targetId"), before, after)
                    || newActionSurfaceTriggeredBy(action.optString("targetId"), after)
                    || interruptClosedBy(action.optString("targetId"), after) ? "applied" : "unknown";
        }
        return "unknown";
    }

    public static String signature(JSONObject observation) {
        StringBuilder value = new StringBuilder(observation.optString("url"))
                .append('|').append(observation.optString("documentId")).append('|').append(observation.optString("title"))
                .append('|').append(observation.optString("text")).append('|').append(observation.optBoolean("paymentRequired"));
        JSONArray elements = observation.optJSONArray("elements");
        if (elements != null) for (int i = 0; i < elements.length(); i++) {
            JSONObject element = elements.optJSONObject(i);
            if (element == null) continue;
            value.append('|').append(element.optString("id")).append(':').append(element.optString("label"))
                    .append(':').append(element.optString("href")).append(':').append(element.optString("group")).append(':').append(element.optString("context"))
                    .append(':').append(element.opt("valueMatchesLastInput")).append(':').append(element.opt("checked"))
                    .append(':').append(element.opt("options")).append(':').append(element.opt("scrollTop"))
                    .append(':').append(element.opt("disabled")).append(':').append(element.opt("selected"))
                    .append(':').append(element.opt("pressed")).append(':').append(element.opt("expanded")).append(':').append(element.opt("inViewport"));
        }
        JSONArray interrupts = observation.optJSONArray("interrupts");
        if (interrupts != null) value.append("|interrupts:").append(interrupts);
        JSONObject activation = observation.optJSONObject("activation");
        if (activation != null) value.append("|activation:").append(activation);
        JSONObject deactivation = observation.optJSONObject("deactivation");
        if (deactivation != null) value.append("|deactivation:").append(deactivation);
        return value.toString();
    }

    public static String actionKey(JSONObject action, JSONObject observation) {
        JSONObject target = target(observation, action.optString("targetId"));
        String targetKey = target == null ? action.optString("targetId") : targetKey(target);
        return observation.optString("documentId") + '|' + action.optString("type") + '|'
                + targetKey + '|' + action.optString("method", "dom") + '|' + action.optString("url") + '|'
                + action.optString("query") + '|' + action.optString("text") + '|' + action.optString("value") + '|'
                + action.optString("checked") + '|' + action.optString("direction") + '|'
                + action.optString("submit");
    }

    /** A blocking modal owns the actionable surface until it is dismissed or the page is left. */
    public static boolean actionAllowedByInterrupt(JSONObject action, JSONObject observation) {
        JSONArray interrupts = observation == null ? null : observation.optJSONArray("interrupts");
        boolean blocking = false;
        if (interrupts != null) for (int i = 0; i < interrupts.length(); i++) {
            JSONObject interrupt = interrupts.optJSONObject(i);
            if (interrupt != null && interrupt.optBoolean("blocking")) { blocking = true; break; }
        }
        if (!blocking || action == null) return true;
        String type = action.optString("type");
        if ("navigate".equals(type) || "research".equals(type) || "search".equals(type) || "back".equals(type)
                || "wait".equals(type) || "inspect".equals(type) || "ask_user".equals(type)
                || "impossible".equals(type) || "handoff".equals(type)) return true;
        if (!action.has("targetId")) return false;
        JSONObject target = target(observation, action.optString("targetId"));
        return target != null && target.optBoolean("interrupt");
    }

    private static JSONObject correspondingTarget(JSONObject observation, String id, JSONObject previous) {
        JSONObject exact = target(observation, id);
        if (exact != null || previous == null) return exact;
        String expected = targetKey(previous);
        JSONObject match = null;
        JSONArray elements = observation.optJSONArray("elements");
        if (elements != null) for (int i = 0; i < elements.length(); i++) {
            JSONObject item = elements.optJSONObject(i);
            if (item == null || !expected.equals(targetKey(item))) continue;
            if (match != null) return null; // Ambiguous duplicate labels must never verify each other.
            match = item;
        }
        return match;
    }

    private static boolean interactiveStateChanged(JSONObject before, JSONObject after) {
        if (before == null || after == null) return false;
        for (String key : new String[] {"checked", "expanded", "selected", "pressed", "disabled", "hasValue"}) {
            if ((before.has(key) || after.has(key)) && !String.valueOf(before.opt(key)).equals(String.valueOf(after.opt(key)))) return true;
        }
        // A label change on the same observed node commonly represents Add -> Added or Show -> Hide.
        return before.optString("id").equals(after.optString("id"))
                && !before.optString("label").equals(after.optString("label"));
    }

    private static boolean newInterruptTriggeredBy(String targetId, JSONObject before, JSONObject after) {
        if (targetId.isEmpty()) return false;
        JSONArray next = after.optJSONArray("interrupts");
        if (next == null) return false;
        JSONArray previous = before.optJSONArray("interrupts");
        for (int i = 0; i < next.length(); i++) {
            JSONObject candidate = next.optJSONObject(i);
            if (candidate == null || !targetId.equals(candidate.optString("triggerId"))) continue;
            boolean alreadyVisible = false;
            if (previous != null) for (int j = 0; j < previous.length(); j++) {
                JSONObject old = previous.optJSONObject(j);
                if (old != null && sameInterrupt(old, candidate)) { alreadyVisible = true; break; }
            }
            if (!alreadyVisible) return true;
        }
        return false;
    }

    private static boolean sameInterrupt(JSONObject left, JSONObject right) {
        return left.optString("type").equals(right.optString("type"))
                && left.optString("label").equals(right.optString("label"))
                && left.optString("text").equals(right.optString("text"));
    }

    private static boolean newActionSurfaceTriggeredBy(String targetId, JSONObject after) {
        JSONObject activation = after.optJSONObject("activation");
        JSONArray newActionIds = activation == null ? null : activation.optJSONArray("newActionIds");
        return !targetId.isEmpty() && targetId.equals(activation == null ? "" : activation.optString("triggerId"))
                && newActionIds != null && newActionIds.length() > 0;
    }

    private static boolean interruptClosedBy(String targetId, JSONObject after) {
        JSONObject deactivation = after.optJSONObject("deactivation");
        return !targetId.isEmpty() && deactivation != null
                && targetId.equals(deactivation.optString("triggerId"))
                && "interrupt_closed".equals(deactivation.optString("effect"));
    }

    private static String targetKey(JSONObject item) {
        return item.optString("tag") + '|' + item.optString("role") + '|' + item.optString("label") + '|'
                + item.optString("href") + '|' + item.optString("group") + '|' + item.optString("context") + '|'
                + item.optString("inputType");
    }

    private static JSONObject target(JSONObject observation, String id) {
        JSONArray elements = observation.optJSONArray("elements");
        if (elements != null) for (int i = 0; i < elements.length(); i++) {
            JSONObject item = elements.optJSONObject(i);
            if (item != null && id.equals(item.optString("id"))) return item;
        }
        return null;
    }
}
