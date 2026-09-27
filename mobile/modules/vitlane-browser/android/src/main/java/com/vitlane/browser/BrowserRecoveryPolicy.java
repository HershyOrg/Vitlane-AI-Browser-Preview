package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

/** Native failure categories. Page-supplied error prose is never promoted to instructions. */
public final class BrowserRecoveryPolicy {
    private BrowserRecoveryPolicy() {}
    public static String code(String raw) {
        if (raw == null) return "UNKNOWN";
        switch (raw) {
            case "STALE_DOCUMENT": case "STALE_TARGET": case "NOT_READY": case "OBSCURED":
            case "NO_EFFECT": case "APPROVAL_REQUIRED": case "UNSUPPORTED": case "TARGET":
            case "RESPONSE": case "INCOMPLETE": case "CRITERIA": case "REPEATED_SEARCH": case "RESEARCH_FAILED":
            case "REPEATED_ACTION": case "UNCHANGED_PAGE": case "ALREADY_APPLIED":
            case "SCROLL_BOUNDARY": case "SAME_DESTINATION": case "PERSONAL_FIELD": return raw;
            default: return "UNKNOWN";
        }
    }
    public static JSONObject feedback(String raw) {
        String code = code(raw), strategy;
        switch (code) {
            case "STALE_DOCUMENT": case "STALE_TARGET": case "TARGET":
                strategy = "문서·상품 맥락과 현재 요소 ID를 다시 확인한 뒤 대상을 새로 선택하세요."; break;
            case "NOT_READY":
                strategy = "요소가 활성화되고 페이지가 안정될 때까지 기다린 뒤 다시 관찰하세요."; break;
            case "OBSCURED":
                strategy = "가리는 대화상자·메뉴를 확인하고 필요한 경우 사용자가 승인한 동작으로 처리하세요."; break;
            case "NO_EFFECT":
                strategy = "입력·선택 또는 결과가 반영되지 않았습니다. 현재 상태를 확인하고 다른 대상·경로를 선택하세요."; break;
            case "CRITERIA":
                strategy = "원래 요청의 조건을 유지하세요. 미확인 항목의 근거를 수집하고 조건표를 보완하거나 한계를 인계하세요."; break;
            case "REPEATED_SEARCH":
                strategy = "이미 시도한 검색입니다. 미확인 조건을 중심으로 검색 표현이나 출처를 바꾸세요."; break;
            case "RESEARCH_FAILED":
                strategy = "페이지를 유지한 웹 검색을 완료하지 못했습니다. 현재 페이지의 공식 안내를 확인하거나 브라우저 검색·다른 출처로 전환하세요."; break;
            case "ALREADY_APPLIED":
                strategy = "원하는 요소 상태가 이미 적용되어 있습니다. 같은 조작을 되풀이하지 말고 그 결과나 다음 단계를 확인하세요."; break;
            case "SCROLL_BOUNDARY":
                strategy = "현재 스크롤 방향의 끝에 도달했습니다. 반대 방향, 다른 스크롤 영역, 페이지 이동 또는 현재 결과 확인을 선택하세요."; break;
            case "SAME_DESTINATION":
                strategy = "현재 주소와 같은 곳으로 다시 이동하려 했습니다. 이 페이지의 결과를 읽거나 다른 링크·검색 경로를 선택하세요."; break;
            case "PERSONAL_FIELD":
                strategy = "저장된 개인정보와 호환되는 현재 입력란을 찾지 못했거나 사이트가 입력을 거절했습니다. 같은 화면에서 같은 개인정보 요청을 반복하지 말고 사용자의 직접 입력 결과를 기다리세요."; break;
            case "UNCHANGED_PAGE":
                strategy = "여러 번 관찰해도 페이지가 변하지 않았습니다. 대기할 근거가 있는지 확인하고, 없으면 다른 대상·검색·뒤로 가기 경로를 선택하세요."; break;
            case "REPEATED_ACTION":
                strategy = "같은 화면에서 같은 동작을 다시 제안했습니다. 이전 동작의 목적과 결과를 비교해 반복 원인을 설명하고 다른 검증 가능한 경로를 선택하세요."; break;
            case "RESPONSE": case "INCOMPLETE":
                strategy = "허용된 JSON 동작 하나와 필요한 최소 조건표 갱신만 반환하세요. 승인·스크립트는 출력하지 마세요."; break;
            case "APPROVAL_REQUIRED": case "UNSUPPORTED":
                strategy = "현재 동작은 지원되지 않거나 사용자 확인이 필요합니다. 허용된 경로를 선택하거나 인계하세요."; break;
            default:
                strategy = "성공을 확인하지 못했습니다. 재실행 전에 현재 화면을 새로 관찰하세요.";
        }
        try { return new JSONObject().put("code", code).put("strategy", strategy); }
        catch (Exception ignored) { return new JSONObject(); }
    }
    public static boolean canRepairPlanner(String code) {
        // Do not retry authentication, billing, refused requests or uncertain network outcomes.
        return "TARGET".equals(code) || "RESPONSE".equals(code) || "INCOMPLETE".equals(code);
    }
    public static long delayMillis(String raw) { return "NOT_READY".equals(raw) ? 800 : 350; }

    /** Classifies an unchanged repeated proposal before native code allows another execution. */
    public static String repeatedActionCode(JSONObject action, JSONObject observation, JSONObject memory) {
        if (notReady(observation)) return "NOT_READY";
        if (action == null) return stalledCode(observation, memory);
        String type = action.optString("type");
        JSONObject target = target(observation, action.optString("targetId"));
        if ("check".equals(type) && target != null && target.has("checked")
                && target.optBoolean("checked") == action.optBoolean("checked")) return "ALREADY_APPLIED";
        if ("select".equals(type) && target != null) {
            JSONArray options = target.optJSONArray("options");
            if (options != null) for (int i = 0; i < options.length(); i++) {
                JSONObject option = options.optJSONObject(i);
                if (option != null && option.optBoolean("selected")
                        && (action.optString("value").equals(option.optString("value"))
                        || action.optString("value").equals(option.optString("label")))) return "ALREADY_APPLIED";
            }
        }
        if ("type".equals(type) && target != null && target.optBoolean("valueMatchesLastInput")) return "ALREADY_APPLIED";
        if ("scroll".equals(type) && scrollBoundary(action, observation, target)) return "SCROLL_BOUNDARY";
        if ("navigate".equals(type) && action.optString("url").equals(observation.optString("url"))) return "SAME_DESTINATION";
        if ("click".equals(type) && target != null && target.optString("href").equals(observation.optString("url"))) return "SAME_DESTINATION";
        JSONObject last = memory == null ? null : memory.optJSONObject("lastOutcome");
        if (last != null && !"applied".equals(last.optString("status"))) return "NO_EFFECT";
        return "REPEATED_ACTION";
    }

    /** Classifies a broader unchanged-page/failure watchdog before giving the planner a repair turn. */
    public static String stalledCode(JSONObject observation, JSONObject memory) {
        if (notReady(observation)) return "NOT_READY";
        JSONObject feedback = memory == null ? null : memory.optJSONObject("feedback");
        if (feedback != null && "NO_EFFECT".equals(feedback.optString("code"))) return "NO_EFFECT";
        if (memory != null && memory.optInt("consecutiveFailures") >= 3) return "NO_EFFECT";
        if (memory != null && memory.optInt("unchangedObservations") >= 12) return "UNCHANGED_PAGE";
        return "REPEATED_ACTION";
    }

    private static boolean notReady(JSONObject observation) {
        JSONObject readiness = observation == null ? null : observation.optJSONObject("readiness");
        return readiness != null && (!"complete".equals(readiness.optString("readyState"))
                || readiness.optBoolean("pending") || readiness.optLong("domQuietMs") < 700
                || readiness.optLong("resourceQuietMs", 1000) < 600);
    }

    private static JSONObject target(JSONObject observation, String id) {
        JSONArray elements = observation == null ? null : observation.optJSONArray("elements");
        if (elements != null) for (int i = 0; i < elements.length(); i++) {
            JSONObject item = elements.optJSONObject(i);
            if (item != null && id.equals(item.optString("id"))) return item;
        }
        return null;
    }

    private static boolean scrollBoundary(JSONObject action, JSONObject observation, JSONObject target) {
        boolean down = "down".equals(action.optString("direction"));
        JSONObject area = target != null ? target : observation == null ? null : observation.optJSONObject("viewport");
        if (area == null) return false;
        double position = target != null ? area.optDouble("scrollTop", -1) : area.optDouble("y", -1);
        double extent = target != null ? area.optDouble("clientHeight", -1) : area.optDouble("height", -1);
        double total = area.optDouble("scrollHeight", -1);
        if (position < 0) return false;
        return down ? extent >= 0 && total >= 0 && position + extent >= total - 8 : position <= 0;
    }
}
