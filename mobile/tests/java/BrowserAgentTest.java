package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;

import java.io.ByteArrayInputStream;
import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.net.URL;
import java.net.URLConnection;
import java.net.URLStreamHandler;
import java.nio.charset.StandardCharsets;

/** Pure JVM checks with in-memory HTTPS; no account, credentials or network required. */
public final class BrowserAgentTest {
    private static int checks;
    private static int connections;
    private static FakeConnection connection;
    private static int nextStatus = 200;
    private static String nextBody;
    private static boolean timeout;

    public static void main(String[] args) throws Exception {
        URL.setURLStreamHandlerFactory(protocol -> "https".equals(protocol) ? new URLStreamHandler() {
            @Override protected URLConnection openConnection(URL url) {
                if (!"https://api.openai.com/v1/responses".equals(url.toString())) throw new AssertionError("Unexpected endpoint");
                connections++;
                return connection = new FakeConnection(url);
            }
        } : null);

        JSONObject observation = observation();
        observation.put("cookies", "never-send-cookie").put("device", "never-send-device")
                .put("approved", "never-send-approval").put("apiKey", "never-send-key");
        observation.getJSONArray("elements").getJSONObject(0).put("value", "never-send-input");
        JSONArray history = new JSONArray().put(new JSONObject().put("type", "inspect").put("status", "applied")
                .put("message", "페이지 확인").put("approved", "never-send-history"));
        JSONObject request = BrowserAgent.buildRequest(null, "러닝화 찾기", observation, history);
        check("gpt-5-mini".equals(request.getString("model")), "default model");
        check(!request.getBoolean("store") && request.getInt("max_output_tokens") == 4096, "bounded unpersisted response");
        check("json_object".equals(request.getJSONObject("text").getJSONObject("format").getString("type")), "JSON format");
        check(!request.toString().contains("never-send"), "private properties excluded recursively");
        JSONObject input = new JSONObject(request.getString("input"));
        check(input.length() == 3 && input.has("goal") && input.has("history") && input.has("observation"), "input allowlist");
        check("https://shop.example.com/search".equals(input.getJSONObject("observation").getString("url")), "page query omitted");
        check("https://shop.example.com/product".equals(input.getJSONObject("observation").getJSONArray("elements").getJSONObject(0).getString("href")), "link query omitted");
        check(observation.getString("url").contains("?q="), "native observation is not mutated");
        JSONObject publicData = observation();
        publicData.put("text", "contact person@example.com sk-example_private_token 010-1234-5678");
        String sanitized = BrowserAgent.buildRequest("gpt-custom", "검색", publicData, new JSONArray()).getString("input");
        check(!sanitized.contains("person@example.com") && !sanitized.contains("sk-example") && !sanitized.contains("010-1234"), "obvious secrets scrubbed");

        JSONObject finish = action("finish").put("message", "완료");
        check("finish".equals(parse(finish).getString("type")), "finish action");
        JSONObject split = response(finish);
        split.getJSONArray("output").getJSONObject(1).put("content", new JSONArray()
                .put(new JSONObject().put("type", "output_text").put("text", "{\"type\":\"finish\","))
                .put(new JSONObject().put("type", "output_text").put("text", "\"message\":\"완료\"}")));
        check("완료".equals(BrowserAgent.actionFromResponse(split, observation()).getString("message")), "reasoning skipped and fragments joined");
        check("e1".equals(parse(action("click").put("targetId", "e1")).getString("targetId")), "current public link ID");
        JSONObject button = parse(action("click").put("targetId", "e3"));
        check("click".equals(button.getString("type")) && !button.has("approved"), "button proposal never grants native approval");
        check("handoff".equals(parse(action("click").put("targetId", "e4")).getString("type")), "payment always manual");
        JSONObject search = action("type").put("targetId", "e2").put("text", "가벼운 러닝화").put("submit", true);
        JSONObject searchAction = parse(search);
        check(searchAction.getBoolean("submit") && "가벼운 러닝화".equals(searchAction.getString("text")), "public search submission");
        check("handoff".equals(parse(action("type").put("targetId", "e5").put("text", "홍길동")).getString("type")), "nonsearch field manual");
        for (String privateText : new String[] {"person@example.com", "01012345678", "０１０１２３４５６７８", "password: secret", "https://private.example.com"}) {
            check("handoff".equals(parse(new JSONObject(search.toString()).put("text", privateText)).getString("type")), "private query manual");
        }
        check("down".equals(parse(action("scroll").put("direction", "down")).getString("direction")), "scroll direction");
        check("inspect".equals(parse(action("inspect")).getString("type")), "inspect action");
        check("handoff".equals(parse(action("handoff")).getString("type")), "handoff action");
        check("https://www.google.com/search?q=running+shoes".equals(parse(action("navigate")
                .put("url", "https://www.google.com/search?q=running+shoes")).getString("url")), "public search URL");
        check("handoff".equals(parse(action("navigate").put("url", "https://shop.example.com/checkout")).getString("type")), "checkout navigation manual");
        check("handoff".equals(parse(action("navigate").put("url", "https://shop.example.com/?token=private")).getString("type")), "credential URL manual");

        reject(() -> parse(action("click").put("targetId", "missing")), "TARGET", "invented target ID");
        reject(() -> parse(action("click").put("targetId", "e1").put("approved", true)), "POLICY", "forged approval");
        reject(() -> parse(action("click").put("targetId", "e1").put("selector", "body")), "POLICY", "arbitrary selector");
        reject(() -> parse(action("inspect").put("script", "alert(1)")), "POLICY", "arbitrary script");
        reject(() -> parse(action("eval")), "POLICY", "unknown action");
        reject(() -> parse(action("scroll").put("direction", "left")), "POLICY", "invalid scroll");
        reject(() -> parse(new JSONObject(search.toString()).put("submit", "true")), "POLICY", "typed submission flag");
        for (String url : new String[] {"http://example.com", "javascript:alert(1)", "https://127.0.0.1/", "https://169.254.169.254/", "https://192.168.1.1/", "https://localhost/", "https://intranet.local/", "https://user:secret@example.com/", "https://0177.0.0.1/", "https://[::1]/", "https://example.com:8080/"}) {
            reject(() -> parse(action("navigate").put("url", url)), "URL", "unsafe URL");
        }
        reject(() -> BrowserAgent.actionFromResponse(response(finish).put("status", "incomplete"), observation()), "INCOMPLETE", "partial JSON response");
        JSONObject refused = response(finish);
        refused.getJSONArray("output").getJSONObject(1).getJSONArray("content").put(new JSONObject().put("type", "refusal").put("refusal", "upstream-secret"));
        reject(() -> BrowserAgent.actionFromResponse(refused, observation()), "REFUSAL", "refusal alongside text");
        JSONObject tools = response(finish);
        tools.getJSONArray("output").put(new JSONObject().put("type", "function_call").put("name", "pay"));
        reject(() -> BrowserAgent.actionFromResponse(tools, observation()), "RESPONSE", "tool outputs denied");
        JSONObject extra = response(finish);
        extra.getJSONArray("output").getJSONObject(1).getJSONArray("content").getJSONObject(0).put("text", finish.toString() + " {}");
        reject(() -> BrowserAgent.actionFromResponse(extra, observation()), "RESPONSE", "trailing object denied");
        JSONObject malformed = observation();
        malformed.put("sensitive", "false");
        reject(() -> BrowserAgent.buildRequest(null, "검색", malformed, new JSONArray()), "INPUT", "privacy flag type");
        JSONObject duplicate = observation();
        duplicate.getJSONArray("elements").put(duplicate.getJSONArray("elements").getJSONObject(0));
        reject(() -> BrowserAgent.buildRequest(null, "검색", duplicate, new JSONArray()), "INPUT", "duplicate targets");
        JSONArray longHistory = new JSONArray();
        for (int i = 0; i < 21; i++) longHistory.put(new JSONObject());
        reject(() -> BrowserAgent.buildRequest(null, "검색", observation(), longHistory), "INPUT", "history cap");
        reject(() -> BrowserAgent.buildRequest(null, repeat('a', 4001), observation(), new JSONArray()), "INPUT", "goal cap");

        JSONObject sensitive = observation().put("sensitive", true).put("text", "never-send-sensitive-page");
        int before = connections;
        check("handoff".equals(BrowserAgent.plan(null, null, "검색", sensitive, new JSONArray(), ignored -> { throw new AssertionError("No connection"); }).getString("type")), "sensitive page local handoff");
        check("handoff".equals(BrowserAgent.plan(null, null, "email person@example.com", observation(), new JSONArray(), null).getString("type")), "private goal local handoff");
        check(connections == before, "privacy handoffs do not use network or credentials");
        reject(() -> BrowserAgent.buildRequest(null, "검색", sensitive, new JSONArray()), "SENSITIVE", "sensitive data not serialized");
        reject(() -> BrowserAgent.plan("key\r\nheader:secret", null, "검색", observation(), new JSONArray(), null), "KEY", "header injection rejected");

        nextBody = response(finish).toString();
        final int[] callbacks = {0};
        BrowserAgent.plan("sk-test-local-only", null, "검색", observation(), new JSONArray(), registered -> { callbacks[0]++; });
        check(callbacks[0] == 2 && connection.disconnected, "connection registered and released");
        check(!connection.getInstanceFollowRedirects() && "POST".equals(connection.getRequestMethod()), "POST cannot redirect credentials");
        check(connection.getConnectTimeout() == 10000 && connection.getReadTimeout() == 120000, "network timeouts");
        check("Bearer sk-test-local-only".equals(connection.getRequestProperty("Authorization")), "authorization header");
        check(!connection.written.toString("UTF-8").contains("sk-test-local-only"), "key not in request body");
        for (int status : new int[] {302, 400, 401, 403, 404, 429, 500}) {
            nextStatus = status;
            nextBody = "upstream-secret";
            int count = connections;
            reject(() -> BrowserAgent.plan("sk-test-local-only", null, "검색", observation(), new JSONArray(), null), null, "HTTP failure sanitized");
            check(!connection.inputRead && connection.disconnected && connections == count + 1, "error bodies unread, no automatic retry");
        }
        nextStatus = 200;
        nextBody = repeat(' ', 140000);
        reject(() -> BrowserAgent.plan("sk-test-local-only", null, "검색", observation(), new JSONArray(), null), "RESPONSE_SIZE", "bounded response IO");
        nextBody = response(finish).toString();
        timeout = true;
        reject(() -> BrowserAgent.plan("sk-test-local-only", null, "검색", observation(), new JSONArray(), null), "TIMEOUT", "socket timeout sanitized");
        timeout = false;
        reject(() -> BrowserAgent.plan("sk-test-local-only", null, "검색", observation(), new JSONArray(), registered -> {
            if (registered != null) throw new IllegalStateException("upstream-secret");
        }), "NETWORK", "cancel registration failure sanitized");
        check(connection.disconnected && connection.written.size() == 0, "cancel before request writes");
        Thread.currentThread().interrupt();
        try {
            reject(() -> BrowserAgent.plan("sk-test-local-only", null, "검색", observation(), new JSONArray(), null), "CANCELLED", "interrupted thread does not send");
        } finally { Thread.interrupted(); }
        System.out.println("BrowserAgent: " + checks + " checks passed (no network requests)");
    }

    private static JSONObject observation() throws Exception {
        return new JSONObject().put("url", "https://shop.example.com/search?q=shoes#results").put("title", "공개 상품 검색")
                .put("text", "러닝화 상품 목록").put("sensitive", false).put("elements", new JSONArray()
                        .put(element("e1", "a", "link", "러닝화").put("href", "https://shop.example.com/product?tracking=ignored#top"))
                        .put(element("e2", "input", "searchbox", "상품 검색").put("inputType", "search").put("search", true))
                        .put(element("e3", "button", "button", "장바구니 담기"))
                        .put(element("e4", "button", "button", "결제하기"))
                        .put(element("e5", "input", "textbox", "이름").put("inputType", "text").put("search", false)));
    }
    private static JSONObject element(String id, String tag, String role, String label) throws Exception {
        return new JSONObject().put("id", id).put("tag", tag).put("role", role).put("label", label);
    }
    private static JSONObject action(String type) throws Exception { return new JSONObject().put("type", type).put("message", "다음 단계"); }
    private static JSONObject parse(JSONObject action) throws Exception { return BrowserAgent.actionFromResponse(response(action), observation()); }
    private static JSONObject response(JSONObject action) throws Exception {
        return new JSONObject().put("status", "completed").put("output", new JSONArray()
                .put(new JSONObject().put("type", "reasoning").put("summary", new JSONArray()))
                .put(new JSONObject().put("type", "message").put("role", "assistant").put("status", "completed")
                        .put("content", new JSONArray().put(new JSONObject().put("type", "output_text").put("text", action.toString())))));
    }
    private static String repeat(char value, int count) { char[] text = new char[count]; java.util.Arrays.fill(text, value); return new String(text); }
    private static void check(boolean condition, String label) { if (!condition) throw new AssertionError(label); checks++; }
    private interface Checked { void run() throws Exception; }
    private static void reject(Checked action, String code, String label) throws Exception {
        try { action.run(); } catch (BrowserAgent.PlannerException error) {
            check((code == null || code.equals(error.code)) && error.getMessage() != null
                    && !error.getMessage().contains("upstream-secret") && !error.getMessage().contains("sk-test"), label + " (" + error.code + ")");
            return;
        }
        throw new AssertionError(label + " should reject");
    }
    private static final class FakeConnection extends HttpURLConnection {
        final ByteArrayOutputStream written = new ByteArrayOutputStream();
        boolean disconnected;
        boolean inputRead;
        FakeConnection(URL url) { super(url); }
        @Override public void disconnect() { disconnected = true; }
        @Override public boolean usingProxy() { return false; }
        @Override public void connect() { }
        @Override public int getResponseCode() throws SocketTimeoutException { if (timeout) throw new SocketTimeoutException("upstream-secret"); return nextStatus; }
        @Override public OutputStream getOutputStream() { return written; }
        @Override public InputStream getInputStream() { inputRead = true; return new ByteArrayInputStream(nextBody.getBytes(StandardCharsets.UTF_8)); }
    }
}
