package org.chromium.chrome.browser.lane;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.URL;
import java.net.URLConnection;
import java.net.URLStreamHandler;
import java.nio.charset.StandardCharsets;
import java.nio.file.Files;
import java.nio.file.Paths;

/** JVM contract checks using a real org.json implementation and an in-memory HTTPS transport. */
public final class LaneOpenAiPlannerTest {
    private static String fixture;
    private static int checks;
    private static FakeConnection connection;
    private static int nextStatus = 200;
    private static String nextBody;

    public static void main(String[] args) throws Exception {
        fixture = new String(Files.readAllBytes(Paths.get(args[0])), StandardCharsets.UTF_8);
        URL.setURLStreamHandlerFactory(protocol -> "https".equals(protocol) ? new URLStreamHandler() {
            @Override protected URLConnection openConnection(URL url) {
                if (!"https://api.openai.com/v1/responses".equals(url.toString())) {
                    throw new AssertionError("Unexpected endpoint");
                }
                connection = new FakeConnection(url, nextStatus, nextBody);
                return connection;
            }
        } : null);

        JSONObject step = step();
        step.put("approvedPreparation", new JSONObject().put("secret", "never-send-approval"));
        step.put("apiKey", "never-send-body-key");
        step.getJSONObject("observation").put("rawCookies", "never-send-cookies");
        step.getJSONObject("observation").getJSONObject("nativeMetadata").put("extra", "never-send-metadata");
        JSONObject request = LaneOpenAiPlanner.buildRequest(null, step);
        check("gpt-5-mini".equals(request.getString("model")), "default model");
        check(!request.getBoolean("store") && request.getInt("max_output_tokens") == 4096, "request controls");
        check("json_object".equals(request.getJSONObject("text").getJSONObject("format").getString("type")), "JSON mode");
        check(!request.toString().contains("never-send") && !request.toString().contains("commandContext"), "private fields omitted");
        JSONObject input = new JSONObject(request.getString("input"));
        check(input.length() == 3 && input.has("goal") && input.has("observation") && input.has("history"), "input allowlist");

        JSONObject finish = new JSONObject().put("kind", "finish").put("reason", "done").put("message", "Result");
        JSONObject forged = new JSONObject(finish.toString()).put("page", new JSONObject().put("tabId", "attacker"))
                .put("serverPermit", "never-trust-model-permit");
        JSONObject action = LaneOpenAiPlanner.actionFromResponse(response(forged), observation());
        check("tab_001".equals(action.getJSONObject("page").getString("tabId")) && !action.has("serverPermit"), "native identity binding");
        check(action.getJSONObject("page").getLong("documentEpoch") == 7, "native epoch binding");

        JSONObject split = response(finish);
        split.getJSONArray("output").getJSONObject(1).put("content", new JSONArray()
                .put(new JSONObject().put("type", "output_text").put("text", "{\"kind\":\"finish\","))
                .put(new JSONObject().put("type", "output_text").put("text", "\"reason\":\"done\",\"message\":\"ok\"}")));
        check("finish".equals(LaneOpenAiPlanner.actionFromResponse(split, observation()).getString("kind")), "reasoning and split output");

        JSONObject link = new JSONObject().put("kind", "open_candidate").put("candidateRef", "candidate_1").put("reason", "inspect");
        check("candidate_1".equals(LaneOpenAiPlanner.actionFromResponse(response(link), observation()).getString("candidateRef")), "valid candidate");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(new JSONObject(link.toString()).put("candidateRef", "unknown")), observation()), "missing candidate");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(new JSONObject(link.toString()).put("candidateRef", "candidate_2")), observation()), "candidate kind");
        JSONObject query = new JSONObject().put("kind", "run_preparation_step").put("adapterId", "builtin.public-search")
                .put("recipeVersion", "1").put("stepId", "prepare_query").put("candidateRef", "candidate_2")
                .put("query", "running shoes").put("reason", "search");
        check("running shoes".equals(LaneOpenAiPlanner.actionFromResponse(response(query), observation())
                .getJSONObject("bindings").getString("query")), "public search binding");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(new JSONObject(query.toString())
                .put("adapterId", "builtin.coupang.purchase-preparation").put("stepId", "buy_now")), observation()), "model purchase authority");
        for (String sensitive : new String[] {"person@example.com", "０１０１２３４５６７８", "https://shop.example.com", "api key abc"}) {
            reject(() -> LaneOpenAiPlanner.actionFromResponse(response(new JSONObject(query.toString()).put("query", sensitive)), observation()), "sensitive query");
        }
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(new JSONObject().put("kind", "click").put("reason", "go")), observation()), "generic click");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(finish).put("status", "incomplete"), observation()), "incomplete response");
        JSONObject refused = response(finish);
        refused.getJSONArray("output").getJSONObject(1).getJSONArray("content")
                .put(new JSONObject().put("type", "refusal").put("refusal", "upstream-secret"));
        reject(() -> LaneOpenAiPlanner.actionFromResponse(refused, observation()), "refusal even with text");
        JSONObject extra = response(finish);
        extra.getJSONArray("output").getJSONObject(1).getJSONArray("content").getJSONObject(0)
                .put("text", finish.toString() + " {}");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(extra, observation()), "multiple JSON objects");
        JSONObject tools = response(finish);
        tools.getJSONArray("output").put(new JSONObject().put("type", "function_call").put("name", "pay"));
        reject(() -> LaneOpenAiPlanner.actionFromResponse(tools, observation()), "tool output");

        JSONObject unsafe = step();
        unsafe.getJSONObject("observation").getJSONObject("privacy").put("secretsOmitted", false);
        reject(() -> LaneOpenAiPlanner.buildRequest(null, unsafe), "privacy declaration");
        JSONObject crossOrigin = observation();
        crossOrigin.getJSONObject("untrustedPageData").getJSONArray("candidates").getJSONObject(0)
                .put("href", "https://other.example.com/product");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(link), crossOrigin), "cross-origin candidate");
        JSONObject sensitiveUrl = observation();
        sensitiveUrl.getJSONObject("untrustedPageData").getJSONArray("candidates").getJSONObject(0)
                .put("href", "https://shop.example.com/product?token=secret");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(link), sensitiveUrl), "query data candidate");
        JSONObject background = observation();
        background.getJSONObject("nativeMetadata").put("foreground", false);
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(finish), background), "background observation");
        JSONObject privateHost = observation();
        privateHost.getJSONObject("nativeMetadata").put("topOrigin", "http://127.0.0.1").put("frameOrigin", "http://127.0.0.1");
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(finish), privateHost), "private origin");
        JSONObject duplicate = observation();
        JSONArray candidates = duplicate.getJSONObject("untrustedPageData").getJSONArray("candidates");
        candidates.put(candidates.getJSONObject(0));
        reject(() -> LaneOpenAiPlanner.actionFromResponse(response(finish), duplicate), "duplicate refs");
        JSONObject overflowingHistory = step();
        for (int i = 0; i < 21; i++) overflowingHistory.getJSONArray("history").put(new JSONObject());
        reject(() -> LaneOpenAiPlanner.buildRequest(null, overflowingHistory), "history limit");

        JSONObject blocked = step();
        JSONObject observation = blocked.getJSONObject("observation");
        observation.put("untrustedPageData", new JSONObject().put("pageTypeHint", "unknown")
                .put("title", "").put("visibleText", "").put("candidates", new JSONArray()));
        observation.getJSONObject("privacy").put("collectionStatus", "handoff_required")
                .put("handoffReasonCodes", new JSONArray().put("PAYMENT_OR_COMMITMENT"))
                .put("excludedBoundaryCodes", new JSONArray());
        JSONObject handoff = LaneOpenAiPlanner.plan(null, null, blocked, ignored -> { throw new AssertionError("No network allowed"); });
        check("request_human".equals(handoff.getString("kind")), "local privacy handoff without key");
        reject(() -> LaneOpenAiPlanner.buildRequest(null, blocked), "blocked serialization");
        observation.getJSONObject("untrustedPageData").put("visibleText", "private-account-data");
        reject(() -> LaneOpenAiPlanner.plan(null, null, blocked, ignored -> { throw new AssertionError("No network allowed"); }), "blocked leaked data");

        nextBody = response(finish).toString();
        final int[] callbacks = {0};
        LaneOpenAiPlanner.plan("sk-local-test-key", null, step(), registered -> { callbacks[0]++; });
        check(callbacks[0] == 2 && connection.disconnected, "connection ownership cleanup");
        check(!connection.getInstanceFollowRedirects() && "POST".equals(connection.getRequestMethod()), "fixed POST without redirect");
        check("Bearer sk-local-test-key".equals(connection.getRequestProperty("Authorization")), "native auth header");
        check(!connection.written.toString("UTF-8").contains("sk-local-test-key"), "key absent from body");
        nextStatus = 302;
        nextBody = "upstream-secret";
        reject(() -> LaneOpenAiPlanner.plan("sk-local-test-key", null, step(), null), "redirect response rejected");
        check(!connection.inputRead && connection.disconnected, "error body not read");
        nextStatus = 200;
        nextBody = new String(new char[140000]).replace('\0', ' ');
        reject(() -> LaneOpenAiPlanner.plan("sk-local-test-key", null, step(), null), "response size limit");
        nextBody = response(finish).toString();
        reject(() -> LaneOpenAiPlanner.plan("sk-local-test-key", null, step(), registered -> {
            if (registered != null) throw new IllegalStateException("upstream-secret");
        }), "cancelled registration");
        check(connection.disconnected && connection.written.size() == 0, "cancel before request writes");
        System.out.println("LaneOpenAiPlanner: " + checks + " checks passed (no network requests)");
    }

    private static JSONObject step() throws Exception { return new JSONObject(fixture); }
    private static JSONObject observation() throws Exception { return step().getJSONObject("observation"); }
    private static JSONObject response(JSONObject action) throws Exception {
        return new JSONObject().put("status", "completed").put("output", new JSONArray()
                .put(new JSONObject().put("type", "reasoning").put("summary", new JSONArray()))
                .put(new JSONObject().put("type", "message").put("role", "assistant").put("status", "completed")
                        .put("content", new JSONArray().put(new JSONObject().put("type", "output_text").put("text", action.toString())))));
    }
    private static void check(boolean result, String label) {
        if (!result) throw new AssertionError(label);
        checks++;
    }
    private interface Checked { void run() throws Exception; }
    private static void reject(Checked action, String label) throws Exception {
        try { action.run(); } catch (Exception e) {
            check(e.getMessage() != null && e.getMessage().matches("[A-Z_]+"), label + " must have generic error");
            return;
        }
        throw new AssertionError(label + " should reject");
    }
    private static final class FakeConnection extends HttpURLConnection {
        final ByteArrayOutputStream written = new ByteArrayOutputStream();
        final int status;
        final String body;
        boolean disconnected;
        boolean inputRead;
        FakeConnection(URL url, int status, String body) { super(url); this.status = status; this.body = body; }
        @Override public void disconnect() { disconnected = true; }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() {}
        @Override public int getResponseCode() { return status; }
        @Override public OutputStream getOutputStream() { return written; }
        @Override public InputStream getInputStream() {
            inputRead = true;
            return new ByteArrayInputStream(body.getBytes(StandardCharsets.UTF_8));
        }
    }
}
