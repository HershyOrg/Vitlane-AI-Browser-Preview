package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

import java.nio.charset.StandardCharsets;
import java.text.Normalizer;
import java.util.Arrays;
import java.util.HashSet;
import java.util.Iterator;
import java.util.LinkedHashMap;
import java.util.Map;
import java.util.Set;
import java.util.regex.Matcher;
import java.util.regex.Pattern;

/** Immutable goal criteria and a bounded RAM evidence ledger. Assessments remain model judgments. */
public final class BrowserTaskContract {
    private static final int MAX_REQUIREMENTS = 12, MAX_CANDIDATES = 12, MAX_SNAPSHOT_BYTES = 122880;
    private static final Pattern NUMBERS = Pattern.compile("(?<![0-9])[0-9]+(?:[,.][0-9]+)*(?![0-9])");
    private static final Pattern KOREAN_COUNT = Pattern.compile("(?<![0-9])([1-9][0-9]*|한|두|세|네|다섯|여섯|일곱|여덟|아홉|열)\\s*(?:개|가지|곳)(?=\\s|[를을이가의도와과만씩,.;!?]|$)");
    private static final Pattern ENGLISH_COUNT = Pattern.compile("(?i)\\b([1-9][0-9]*|one|two|three|four|five|six|seven|eight|nine|ten)\\s+(?:distinct\\s+|different\\s+)?(?:products?|items?|options?|candidates?|results?|places?|hotels?|restaurants?|monitors?|laptops?|phones?|flights?|models?)\\b");
    private final String goal;
    private final int targetCount;
    private final String countQuote;
    private final String unsupportedReason;
    private LinkedHashMap<String, JSONObject> requirements = new LinkedHashMap<>();
    private LinkedHashMap<String, JSONObject> candidates = new LinkedHashMap<>();
    private JSONArray scope = new JSONArray();
    private boolean initialized;
    private String lastError = "";

    public BrowserTaskContract(String sourceGoal) {
        String raw = sourceGoal == null ? "" : sourceGoal;
        goal = normalize(BrowserAgent.publicText(raw.length() <= 4000 ? raw : raw.substring(0, 4000)));
        Set<Integer> counts = new HashSet<>();
        StringBuilder quotes = new StringBuilder();
        for (Pattern pattern : new Pattern[] {KOREAN_COUNT, ENGLISH_COUNT}) {
            Matcher matches = pattern.matcher(goal);
            while (matches.find()) {
                if (pattern == KOREAN_COUNT && !isResultCount(goal, matches.start(), matches.end())) continue;
                counts.add(countValue(matches.group(1)));
                if (quotes.length() > 0) quotes.append(" | ");
                quotes.append(matches.group());
            }
        }
        int count = counts.isEmpty() ? 1 : counts.iterator().next();
        targetCount = count;
        countQuote = quotes.toString();
        unsupportedReason = raw.trim().isEmpty() || raw.length() > 4000 ? "목표 길이를 확인해 주세요."
                : counts.size() > 1 ? "서로 다른 결과 수량이 있어 자동 완료 수량을 확정할 수 없습니다."
                : count < 1 || count > MAX_CANDIDATES ? "요청한 결과 수량이 비교 기록 한도 12개를 벗어납니다." : "";
    }

    /** Malformed or oversized updates fail atomically; no prior evidence is silently evicted. */
    public boolean apply(JSONObject update, JSONObject observation, JSONObject memory) {
        try {
            if (!unsupportedReason.isEmpty()) throw invalid(unsupportedReason);
            if (update == null) throw invalid("작업 조건 업데이트가 없습니다.");
            only(update, "requirements", "scope", "candidates");
            JSONObject safeObservation = BrowserAgent.sanitizeObservation(observation);
            if (safeObservation.optBoolean("sensitive")) throw invalid("민감한 페이지는 조건 근거로 저장할 수 없습니다.");
            JSONObject safeMemory = BrowserTaskMemory.sanitize(memory);
            LinkedHashMap<String, JSONObject> nextRequirements = copy(requirements);
            JSONArray nextScope = new JSONArray(scope.toString());
            if (!initialized) {
                if (!update.has("requirements")) throw invalid("먼저 목표 원문에서 필수 조건을 설정해 주세요.");
                nextRequirements = parseRequirements(update.getJSONArray("requirements"));
                nextScope = update.has("scope") ? parseScope(update.getJSONArray("scope")) : new JSONArray();
                requireNumericCoverage(nextRequirements);
            } else {
                if (update.has("requirements") && !sameRequirements(requirements, parseRequirements(update.getJSONArray("requirements"))))
                    throw invalid("확정된 조건은 삭제하거나 바꿀 수 없습니다. 새 목표로 시작해 주세요.");
                if (update.has("scope") && !sameStrings(scope, parseScope(update.getJSONArray("scope"))))
                    throw invalid("확정된 작업 범위는 바꿀 수 없습니다.");
            }
            LinkedHashMap<String, JSONObject> nextCandidates = copy(candidates);
            if (update.has("candidates")) {
                JSONArray updates = update.getJSONArray("candidates");
                if (updates.length() > 4) throw invalid("한 번에 후보 4개까지 갱신할 수 있습니다.");
                Set<String> ids = new HashSet<>();
                for (int i = 0; i < updates.length(); i++) {
                    JSONObject candidate = updates.getJSONObject(i);
                    if (!ids.add(identifier(candidate, "id"))) throw invalid("후보 ID가 중복됩니다.");
                    updateCandidate(candidate, nextCandidates, nextRequirements, safeObservation, safeMemory);
                }
            }
            JSONObject next = snapshot(nextRequirements, nextScope, nextCandidates, true, "");
            if (next.toString().getBytes(StandardCharsets.UTF_8).length > MAX_SNAPSHOT_BYTES - 2048)
                throw invalid("조건 근거 기록 한도에 도달했습니다. 기존 근거는 유지됩니다.");
            requirements = nextRequirements; scope = nextScope; candidates = nextCandidates;
            initialized = true; lastError = "";
            return true;
        } catch (ContractException error) {
            lastError = error.getMessage();
        } catch (Exception error) {
            lastError = "조건·후보·근거의 형식 또는 출처를 확인해 주세요.";
        }
        return false;
    }

    public JSONObject toJson() {
        try { return snapshot(requirements, scope, candidates, initialized, lastError); }
        catch (Exception ignored) { return new JSONObject(); }
    }

    /** Necessary conditions only, not an oracle for semantic truth or real-world task success. */
    public boolean canFinish(JSONArray candidateIds) { return completionIssues(candidateIds).length() == 0; }

    /**
     * nativeTaskSnapshot must originate from this class's toJson(), never from the current model output.
     * Retains validated provenance after rolling observation memory has evicted an earlier source page.
     */
    public static JSONArray validateFinishEvidence(JSONArray source, JSONObject observation, JSONObject memory,
            JSONObject nativeTaskSnapshot) throws Exception {
        JSONArray result = new JSONArray();
        if (source == null || source.length() < 1 || source.length() > 6) return result;
        JSONObject safeObservation = BrowserAgent.sanitizeObservation(observation);
        if (safeObservation.optBoolean("sensitive")) return result;
        for (int i = 0; i < source.length(); i++) {
            JSONObject raw = source.getJSONObject(i);
            only(raw, "url", "quote");
            JSONObject clean = new JSONObject().put("url", BrowserAgent.safeUrl(string(raw, "url", 4096)))
                    .put("quote", BrowserAgent.publicText(string(raw, "quote", 800)));
            JSONArray validated = BrowserTaskMemory.validateEvidence(new JSONArray().put(clean), safeObservation, memory);
            if (validated.length() == 1) { result.put(validated.getJSONObject(0)); continue; }
            String quote = clean.getString("quote");
            if (normalize(quote).length() < 8 || quote.contains("[비공개]")) return new JSONArray();
            boolean found = false;
            JSONArray candidates = nativeTaskSnapshot == null ? null : nativeTaskSnapshot.optJSONArray("candidates");
            if (candidates != null && candidates.length() <= MAX_CANDIDATES) for (int j = 0; j < candidates.length() && !found; j++) {
                JSONObject candidate = candidates.getJSONObject(j);
                if (supports(candidate.getJSONObject("identity"), clean)) { found = true; break; }
                JSONArray checks = candidate.getJSONArray("checks");
                if (checks.length() > MAX_REQUIREMENTS) continue;
                for (int k = 0; k < checks.length() && !found; k++) {
                    JSONArray quotes = checks.getJSONObject(k).getJSONArray("evidence");
                    if (quotes.length() > 2) continue;
                    for (int q = 0; q < quotes.length(); q++) if (supports(quotes.getJSONObject(q), clean)) { found = true; break; }
                }
            }
            if (!found) return new JSONArray();
            result.put(clean);
        }
        return result;
    }

    public JSONArray completionIssues(JSONArray candidateIds) {
        JSONArray issues = new JSONArray();
        try {
            if (!unsupportedReason.isEmpty()) issues.put(unsupportedReason);
            if (!initialized) issues.put("목표의 필수 조건이 아직 확정되지 않았습니다.");
            if (candidateIds == null || candidateIds.length() < targetCount || candidateIds.length() > MAX_CANDIDATES) {
                issues.put("명시한 결과 수량에 맞는 서로 다른 후보가 필요합니다.");
                return issues;
            }
            Set<String> ids = new HashSet<>(), names = new HashSet<>();
            Set<String> collectionSupported = new HashSet<>();
            for (int i = 0; i < candidateIds.length(); i++) {
                Object value = candidateIds.get(i);
                if (!(value instanceof String) || !ids.add((String) value)) { issues.put("결과 후보 ID가 잘못되었거나 중복됩니다."); continue; }
                JSONObject candidate = candidates.get((String) value);
                if (candidate == null) { issues.put("기록되지 않은 후보는 완료 결과에 포함할 수 없습니다."); continue; }
                if (!names.add(normalizeName(candidate.getString("name")))) issues.put("같은 이름의 후보를 서로 다른 결과로 셀 수 없습니다.");
                JSONArray missing = candidateIssues(candidate, requirements);
                for (int j = 0; j < missing.length(); j++) issues.put(missing.get(j));
                JSONArray checks = candidate.getJSONArray("checks");
                for (int j = 0; j < checks.length(); j++) {
                    JSONObject check = checks.getJSONObject(j);
                    if ("supported".equals(check.getString("status"))) collectionSupported.add(check.getString("requirementId"));
                }
            }
            for (JSONObject rule : requirements.values()) if ("required".equals(rule.getString("kind"))
                    && "any".equals(rule.optString("coverage")) && !collectionSupported.contains(rule.getString("id")))
                issues.put(rule.getString("id") + " 조건을 뒷받침하는 선택된 결과가 없습니다.");
        } catch (Exception ignored) { issues.put("완료 후보와 필수 조건을 다시 확인해 주세요."); }
        return issues;
    }

    private JSONObject snapshot(Map<String, JSONObject> rules, JSONArray scopes, Map<String, JSONObject> ledger,
            boolean ready, String error) throws Exception {
        JSONArray eligible = new JSONArray();
        for (Map.Entry<String, JSONObject> entry : ledger.entrySet())
            if (candidateIssues(entry.getValue(), rules).length() == 0) eligible.put(entry.getKey());
        return new JSONObject().put("goal", goal).put("initialized", ready)
                .put("targetCount", targetCount).put("countQuote", countQuote)
                .put("unsupported", !unsupportedReason.isEmpty()).put("unsupportedReason", unsupportedReason)
                .put("requirements", array(rules)).put("scope", new JSONArray(scopes.toString()))
                .put("candidates", array(ledger)).put("eligibleCandidateIds", eligible)
                .put("lastError", error)
                .put("assessmentPolicy", "supported는 모델의 근거 해석입니다. 인용 존재 확인은 조건 충족의 독립적 증명이 아닙니다.");
    }

    private LinkedHashMap<String, JSONObject> parseRequirements(JSONArray source) throws Exception {
        if (source.length() < 1 || source.length() > MAX_REQUIREMENTS) throw invalid("조건은 1개 이상 12개 이하로 설정해 주세요.");
        LinkedHashMap<String, JSONObject> result = new LinkedHashMap<>();
        Set<String> quotes = new HashSet<>();
        boolean required = false;
        for (int i = 0; i < source.length(); i++) {
            JSONObject raw = source.getJSONObject(i);
            only(raw, "id", "quote", "kind", "coverage");
            String id = identifier(raw, "id"), quote = sourceQuote(string(raw, "quote", 600)), kind = string(raw, "kind", 20);
            String coverage = raw.has("coverage") ? string(raw, "coverage", 10) : "each";
            if (!("required".equals(kind) || "preference".equals(kind))) throw invalid("조건 종류를 확인해 주세요.");
            if (!("each".equals(coverage) || "any".equals(coverage))) throw invalid("조건의 적용 범위를 확인해 주세요.");
            if ("preference".equals(kind) && !quote.matches("(?is).*(?:선호|가능하면|가급적|되도록|좋겠|prefer|ideally|if possible|nice to have).*"))
                throw invalid("선호 조건에는 목표 원문의 선호 표현이 포함되어야 합니다.");
            if (result.containsKey(id) || !quotes.add(quote)) throw invalid("조건 ID 또는 인용문이 중복됩니다.");
            required |= "required".equals(kind);
            result.put(id, new JSONObject().put("id", id).put("quote", quote).put("kind", kind).put("coverage", coverage));
        }
        if (!required) throw invalid("필수 조건을 최소 1개 설정해야 합니다.");
        return result;
    }

    private JSONArray parseScope(JSONArray source) throws Exception {
        if (source.length() > 8) throw invalid("작업 범위 인용은 8개 이하로 설정해 주세요.");
        JSONArray result = new JSONArray();
        Set<String> seen = new HashSet<>();
        for (int i = 0; i < source.length(); i++) {
            Object value = source.get(i);
            if (!(value instanceof String)) throw invalid("작업 범위는 목표 원문 인용이어야 합니다.");
            String quote = sourceQuote((String) value);
            // Scope is permission/prohibition, never a way to hide product conditions or budgets.
            if (!quote.matches("(?is).*(?:하지\\s*마|하지\\s*않|금지|까지만|조회만|검색만|비교만|구매는|결제는|승인|허용|do not|don't|never|only (?:search|read|compare)|without (?:buying|purchasing)|permission).*"))
                throw invalid("작업 범위에는 허용·금지에 관한 목표 원문을 넣어 주세요.");
            if (!seen.add(quote)) throw invalid("작업 범위 인용이 중복됩니다.");
            result.put(quote);
        }
        return result;
    }

    private void requireNumericCoverage(Map<String, JSONObject> rules) throws Exception {
        Set<String> covered = new HashSet<>();
        collectNumbers(countQuote, covered);
        for (JSONObject rule : rules.values()) collectNumbers(rule.getString("quote"), covered);
        Set<String> original = new HashSet<>(); collectNumbers(goal, original);
        if (!covered.containsAll(original)) throw invalid("목표에 있는 숫자 조건을 빠뜨리지 말고 원문 그대로 포함해 주세요.");
    }

    private void updateCandidate(JSONObject raw, LinkedHashMap<String, JSONObject> ledger,
            Map<String, JSONObject> rules, JSONObject observation, JSONObject memory) throws Exception {
        only(raw, "id", "name", "identity", "checks");
        String id = identifier(raw, "id");
        JSONObject previous = ledger.get(id), candidate;
        if (previous == null) {
            if (ledger.size() >= MAX_CANDIDATES) throw invalid("후보 기록 한도 12개에 도달했습니다.");
            String name = normalize(BrowserAgent.publicText(string(raw, "name", 200)));
            if (name.length() < 2 || name.contains("[비공개]")) throw invalid("후보 이름을 확인해 주세요.");
            JSONObject identity = evidence(raw.getJSONObject("identity"), observation, memory);
            if (!normalizeName(identity.getString("quote")).contains(normalizeName(name))) throw invalid("후보 이름이 식별 근거 인용문에 있어야 합니다.");
            String key = identity.getString("url") + "\n" + normalizeName(name);
            for (JSONObject item : ledger.values()) {
                String existing = item.getJSONObject("identity").getString("url") + "\n" + normalizeName(item.getString("name"));
                if (existing.equals(key)) throw invalid("같은 출처·이름의 후보가 이미 기록되어 있습니다.");
            }
            JSONArray checks = new JSONArray();
            for (String requirementId : rules.keySet()) checks.put(new JSONObject().put("requirementId", requirementId)
                    .put("status", "unknown").put("evidence", new JSONArray()));
            candidate = new JSONObject().put("id", id).put("name", name).put("identity", identity).put("checks", checks);
        } else {
            candidate = new JSONObject(previous.toString());
            if (raw.has("name") && !normalize(string(raw, "name", 200)).equals(candidate.getString("name")))
                throw invalid("기존 후보의 이름을 다른 대상으로 바꿀 수 없습니다.");
            if (raw.has("identity")) {
                JSONObject identity = raw.getJSONObject("identity");
                only(identity, "url", "quote");
                JSONObject old = candidate.getJSONObject("identity");
                if (!BrowserAgent.safeUrl(string(identity, "url", 4096)).equals(old.getString("url"))
                        || !normalize(BrowserAgent.publicText(string(identity, "quote", 400))).equals(normalize(old.getString("quote"))))
                    throw invalid("기존 후보의 식별 출처를 바꿀 수 없습니다.");
            }
        }
        if (raw.has("checks")) {
            JSONArray updates = raw.getJSONArray("checks");
            if (updates.length() > MAX_REQUIREMENTS) throw invalid("후보 조건 확인 개수가 너무 많습니다.");
            Set<String> seen = new HashSet<>();
            for (int i = 0; i < updates.length(); i++) {
                JSONObject update = updates.getJSONObject(i);
                only(update, "requirementId", "status", "evidence");
                String requirementId = identifier(update, "requirementId"), status = string(update, "status", 20);
                if (!rules.containsKey(requirementId) || !seen.add(requirementId)) throw invalid("확인할 조건 ID가 없거나 중복됩니다.");
                if (!Arrays.asList("unknown", "supported", "contradicted", "unavailable").contains(status)) throw invalid("조건 확인 상태를 확인해 주세요.");
                JSONArray quotes = new JSONArray();
                if (!"unknown".equals(status)) {
                    JSONArray source = update.getJSONArray("evidence");
                    if (source.length() < 1 || source.length() > 2) throw invalid("조건마다 근거 1개 이상 2개 이하가 필요합니다.");
                    for (int j = 0; j < source.length(); j++) quotes.put(evidence(source.getJSONObject(j), observation, memory));
                } else if (update.has("evidence") && update.getJSONArray("evidence").length() > 0) {
                    throw invalid("미확인 상태로 바꿀 때에는 이전 근거를 비워 주세요.");
                }
                JSONArray checks = candidate.getJSONArray("checks");
                for (int j = 0; j < checks.length(); j++) if (requirementId.equals(checks.getJSONObject(j).getString("requirementId")))
                    checks.put(j, new JSONObject().put("requirementId", requirementId).put("status", status).put("evidence", quotes));
            }
        }
        ledger.put(id, candidate);
    }

    /** Previously validated ledger quotes remain usable after the rolling page memory is evicted. */
    private JSONObject evidence(JSONObject raw, JSONObject observation, JSONObject memory) throws Exception {
        only(raw, "url", "quote");
        String url = BrowserAgent.safeUrl(string(raw, "url", 4096));
        String quote = BrowserAgent.publicText(string(raw, "quote", 400));
        JSONObject clean = new JSONObject().put("url", url).put("quote", quote);
        JSONArray validated = BrowserTaskMemory.validateEvidence(new JSONArray().put(clean), observation, memory);
        if (validated.length() == 1) return validated.getJSONObject(0);
        if (normalize(quote).length() < 8 || quote.contains("[비공개]")) throw invalid("인용문이 너무 짧거나 민감한 정보가 있습니다.");
        for (JSONObject candidate : candidates.values()) {
            if (supports(candidate.getJSONObject("identity"), clean)) return clean;
            JSONArray checks = candidate.getJSONArray("checks");
            for (int i = 0; i < checks.length(); i++) {
                JSONArray quotes = checks.getJSONObject(i).getJSONArray("evidence");
                for (int j = 0; j < quotes.length(); j++) if (supports(quotes.getJSONObject(j), clean)) return clean;
            }
        }
        throw invalid("인용문과 출처가 실제 관찰한 페이지에서 확인되지 않습니다.");
    }

    private static boolean supports(JSONObject existing, JSONObject proposed) throws Exception {
        return existing.getString("url").equals(proposed.getString("url"))
                && normalize(existing.getString("quote")).contains(normalize(proposed.getString("quote")));
    }
    private static JSONArray candidateIssues(JSONObject candidate, Map<String, JSONObject> rules) throws Exception {
        JSONArray issues = new JSONArray(), checks = candidate.getJSONArray("checks");
        Map<String, String> states = new LinkedHashMap<>();
        for (int i = 0; i < checks.length(); i++) states.put(checks.getJSONObject(i).getString("requirementId"), checks.getJSONObject(i).getString("status"));
        for (JSONObject rule : rules.values()) if ("required".equals(rule.getString("kind")) && !"any".equals(rule.optString("coverage"))
                && !"supported".equals(states.get(rule.getString("id"))))
            issues.put(candidate.getString("id") + ": " + rule.getString("id") + " 필수 조건 근거가 충족되지 않았습니다.");
        return issues;
    }
    private String sourceQuote(String raw) throws Exception {
        String quote = normalize(raw);
        if (quote.length() < 2 || quote.length() > 600 || !goal.contains(quote)) throw invalid("조건은 목표 원문에 있는 인용문이어야 합니다.");
        return quote;
    }
    private static String identifier(JSONObject raw, String key) throws Exception {
        String id = string(raw, key, 40);
        if (!id.matches("[A-Za-z0-9][A-Za-z0-9_-]{0,39}")) throw invalid("기록 ID 형식을 확인해 주세요.");
        return id;
    }
    private static String string(JSONObject raw, String key, int max) throws Exception {
        Object value = raw.get(key);
        if (!(value instanceof String) || ((String) value).trim().isEmpty() || ((String) value).length() > max
                || ((String) value).matches("(?s).*[\\x00-\\x08\\x0B\\x0C\\x0E-\\x1F].*")) throw invalid("기록 문자열 형식을 확인해 주세요.");
        return (String) value;
    }
    private static void only(JSONObject raw, String... keys) throws Exception {
        Set<String> allowed = new HashSet<>(Arrays.asList(keys));
        Iterator<String> actual = raw.keys();
        while (actual.hasNext()) if (!allowed.contains(actual.next())) throw invalid("지원하지 않는 작업 조건 속성이 있습니다.");
    }
    private static boolean sameRequirements(Map<String, JSONObject> a, Map<String, JSONObject> b) throws Exception {
        if (!a.keySet().equals(b.keySet())) return false;
        for (String key : a.keySet()) if (!a.get(key).getString("quote").equals(b.get(key).getString("quote"))
                || !a.get(key).getString("kind").equals(b.get(key).getString("kind"))
                || !a.get(key).getString("coverage").equals(b.get(key).getString("coverage"))) return false;
        return true;
    }
    private static boolean sameStrings(JSONArray a, JSONArray b) throws Exception {
        Set<String> first = new HashSet<>(), second = new HashSet<>();
        for (int i = 0; i < a.length(); i++) first.add(a.getString(i));
        for (int i = 0; i < b.length(); i++) second.add(b.getString(i));
        return first.equals(second);
    }
    private static LinkedHashMap<String, JSONObject> copy(Map<String, JSONObject> source) throws Exception {
        LinkedHashMap<String, JSONObject> result = new LinkedHashMap<>();
        for (Map.Entry<String, JSONObject> entry : source.entrySet()) result.put(entry.getKey(), new JSONObject(entry.getValue().toString()));
        return result;
    }
    private static JSONArray array(Map<String, JSONObject> source) throws Exception {
        JSONArray result = new JSONArray();
        for (JSONObject value : source.values()) result.put(new JSONObject(value.toString()));
        return result;
    }
    private static void collectNumbers(String text, Set<String> target) { Matcher matcher = NUMBERS.matcher(text); while (matcher.find()) target.add(matcher.group()); }
    private static String normalize(String value) { return Normalizer.normalize(value, Normalizer.Form.NFKC).replaceAll("\\s+", " ").trim(); }
    private static String normalizeName(String value) { return normalize(value).toLowerCase(java.util.Locale.ROOT); }
    private static int countValue(String value) {
        String[] korean = {"한", "두", "세", "네", "다섯", "여섯", "일곱", "여덟", "아홉", "열"};
        String[] english = {"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"};
        for (int i = 0; i < korean.length; i++) if (korean[i].equals(value) || english[i].equalsIgnoreCase(value)) return i + 1;
        try { return Integer.parseInt(value); } catch (NumberFormatException ignored) { return -1; }
    }
    private static boolean isResultCount(String goal, int start, int end) {
        String nouns = "(?:제품|상품|모니터|노트북|호텔|식당|맛집|후보|결과|옵션|항공편|숙소|장소|모델|대안)";
        String before = goal.substring(Math.max(0, start - 24), start).trim();
        String after = goal.substring(end, Math.min(goal.length(), end + 32)).trim();
        return before.matches("(?s).*" + nouns + "\\s*$")
                || after.matches("(?s)^(?:의\\s*)?" + nouns + ".*")
                || after.matches("(?s)^(?:를|을|만|씩)?\\s*(?:찾|비교|추천|보여|알려|정리|선정|골라|뽑).*");
    }
    private static ContractException invalid(String message) { return new ContractException(message); }
    private static final class ContractException extends Exception { ContractException(String message) { super(message); } }
}
