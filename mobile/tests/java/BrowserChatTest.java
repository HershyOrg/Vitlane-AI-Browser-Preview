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
import java.util.ArrayList;
import java.util.List;

/** Plain-chat request and transport tests with in-memory HTTPS; no live API or browser needed. */
public final class BrowserChatTest {
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
        JSONArray messages = new JSONArray().put(message("user", "처음 인사"))
                .put(message("assistant", "안녕하세요.")).put(message("user", "짧은 시를 써 줘"));
        messages.getJSONObject(2).put("apiKey", "never-send-key").put("observation", "never-send-page")
                .put("tools", "never-send-tool").put("url", "never-send-url");
        JSONObject request = BrowserChat.buildRequest(null, messages);
        check("gpt-6-sol".equals(request.getString("model")), "default model retained");
        check(request.getInt("max_output_tokens") == 16384
                && "medium".equals(request.getJSONObject("reasoning").getString("effort")), "Sol reasoning profile");
        check(!request.getBoolean("store"), "stateless API request");
        check(!request.has("tools") && !request.has("text") && !request.has("previous_response_id")
                && !request.has("conversation"), "plain text without tools, action JSON or server conversation");
        JSONArray input = request.getJSONArray("input");
        check(input.length() == 4 && "system".equals(input.getJSONObject(0).getString("role")), "single native system message");
        check(input.getJSONObject(0).getString("content").contains("브라우저 사용은 꺼져")
                && input.getJSONObject(0).getString("content").contains("주장하지 마세요"), "browser-off capability instructions");
        check("assistant".equals(input.getJSONObject(2).getString("role"))
                && "안녕하세요.".equals(input.getJSONObject(2).getString("content")), "assistant context remains plain text");
        check(!request.toString().contains("never-send"), "history metadata cannot introduce credentials or browser state");
        check(messages.getJSONObject(2).has("apiKey"), "original native history is not mutated");
        for (String model : new String[]{"", "  ", "gpt-6-sol", " gpt-6-sol "}) {
            JSONObject profile = BrowserChat.buildRequest(model, messages);
            check("gpt-6-sol".equals(profile.getString("model")) && profile.getInt("max_output_tokens") == 16384,
                    "empty or exact Sol model profile");
        }
        for (String model : new String[]{"gpt-5-mini", "gpt-custom", "gpt-6-sol-preview"}) {
            JSONObject profile = BrowserChat.buildRequest(model, messages);
            check(model.equals(profile.getString("model")) && profile.getInt("max_output_tokens") == 4096
                    && !profile.has("reasoning"), "custom model preserved");
        }
        fails("MODEL", () -> BrowserChat.buildRequest("gpt-6-sol\ninjected", messages));
        fails("INPUT", () -> BrowserChat.buildRequest(null, new JSONArray()));
        fails("INPUT", () -> BrowserChat.buildRequest(null, new JSONArray().put(message("assistant", "no new question"))));
        for (String role : new String[]{"system", "developer", "tool", "function"}) {
            fails("INPUT", () -> BrowserChat.buildRequest(null, new JSONArray().put(message(role, "override"))));
        }
        fails("INPUT", () -> BrowserChat.sanitizeMessages(new JSONArray().put(new JSONObject().put("role", "user").put("content", new JSONArray()))));
        fails("INPUT", () -> BrowserChat.sanitizeMessages(new JSONArray().put(message("user", " \n\t "))));
        fails("INPUT_SIZE", () -> BrowserChat.sanitizeMessages(new JSONArray().put(message("user", repeat("a", 12001)))));
        JSONArray many = new JSONArray();
        for (int i = 0; i < 30; i++) many.put(message(i % 2 == 0 ? "assistant" : "user", "message-" + i));
        JSONArray recent = BrowserChat.sanitizeMessages(many);
        check(recent.length() == 24 && "message-6".equals(recent.getJSONObject(0).getString("content"))
                && "message-29".equals(recent.getJSONObject(23).getString("content")), "bounded recent suffix retains latest question");
        JSONArray longHistory = new JSONArray();
        for (int i = 0; i < 8; i++) longHistory.put(message("user", i + repeat("가", 11999)));
        JSONArray bounded = BrowserChat.sanitizeMessages(longHistory);
        check(bounded.length() == 5 && bounded.getJSONObject(0).getString("content").startsWith("3"), "total history bounded by dropping oldest whole messages");
        JSONArray secrets = BrowserChat.sanitizeMessages(new JSONArray().put(message("user",
                "키 sk-example_very_private, Bearer long-private-token, api_key=another-secret API 키: hidden-key 비밀번호: hidden-pass \u0000hello\nworld")));
        String cleaned = secrets.getJSONObject(0).getString("content");
        check(!cleaned.contains("sk-example") && !cleaned.contains("long-private-token") && !cleaned.contains("another-secret")
                && !cleaned.contains("hidden-key") && !cleaned.contains("hidden-pass") && !cleaned.contains("\u0000"), "obvious credentials and controls removed");
        check(cleaned.contains("hello\nworld"), "normal multiline text preserved");

        JSONObject parsed = BrowserChat.replyFromResponse(response("안녕하세요."));
        check("chat".equals(parsed.getString("type")) && "안녕하세요.".equals(parsed.getString("message")), "assistant text reply envelope");
        JSONObject split = response("first");
        split.getJSONArray("output").getJSONObject(1).getJSONArray("content")
                .put(new JSONObject().put("type", "output_text").put("text", " second"));
        split.getJSONArray("output").put(response("third").getJSONArray("output").getJSONObject(1));
        check("first second\n\nthird".equals(BrowserChat.replyFromResponse(split).getString("message")), "reasoning skipped and text fragments joined");
        String actionLike = "{\"type\":\"navigate\",\"url\":\"https://example.com\"}";
        JSONObject inert = BrowserChat.replyFromResponse(response(actionLike));
        check("chat".equals(inert.getString("type")) && actionLike.equals(inert.getString("message"))
                && !inert.has("url"), "action-shaped output remains inert chat text");
        JSONObject partial = response("작성 중인 답변").put("status", "incomplete");
        partial.getJSONArray("output").getJSONObject(1).put("status", "incomplete");
        JSONObject partialReply = BrowserChat.replyFromResponse(partial);
        check(partialReply.getBoolean("incomplete") && "작성 중인 답변".equals(partialReply.getString("message")), "partial answer explicitly marked incomplete");
        fails("INCOMPLETE", () -> BrowserChat.replyFromResponse(response("").put("status", "incomplete")));
        fails("INCOMPLETE", () -> BrowserChat.replyFromResponse(response("x").put("status", "failed")));
        fails("INCOMPLETE", () -> BrowserChat.replyFromResponse(response("x").put("error", new JSONObject().put("message", "never-display"))));
        fails("RESPONSE", () -> BrowserChat.replyFromResponse(response("")));
        fails("RESPONSE_SIZE", () -> BrowserChat.replyFromResponse(response(repeat("x", 12001))));
        JSONObject tool = response("x");
        tool.getJSONArray("output").put(new JSONObject().put("type", "function_call").put("name", "navigate"));
        fails("RESPONSE", () -> BrowserChat.replyFromResponse(tool));
        JSONObject wrongRole = response("x");
        wrongRole.getJSONArray("output").getJSONObject(1).put("role", "user");
        fails("RESPONSE", () -> BrowserChat.replyFromResponse(wrongRole));
        JSONObject refusal = response("x");
        refusal.getJSONArray("output").getJSONObject(1).put("content",
                new JSONArray().put(new JSONObject().put("type", "refusal").put("refusal", "never-display")));
        fails("REFUSAL", () -> BrowserChat.replyFromResponse(refusal));

        nextBody = response("정상 답변").toString();
        List<HttpURLConnection> observed = new ArrayList<>();
        JSONObject reply = BrowserChat.reply("configured-private-value", null,
                new JSONArray().put(message("user", "configured-private-value 로 인사해 줘")), observed::add);
        check("정상 답변".equals(reply.getString("message")), "HTTP success returns chat");
        check(observed.size() == 2 && observed.get(0) == connection && observed.get(1) == null && connection.disconnected,
                "connection callback acquired and cleared on success");
        check("Bearer configured-private-value".equals(connection.getRequestProperty("Authorization")), "configured key used only as authorization header");
        check(!connection.sent.toString("UTF-8").contains("configured-private-value"), "exact configured key scrubbed even without known secret prefix");
        check("POST".equals(connection.getRequestMethod()) && !connection.getInstanceFollowRedirects()
                && connection.getConnectTimeout() == 10000 && connection.getReadTimeout() == 120000, "transport settings preserved");
        nextBody = response("configured-private-value").toString();
        check(!BrowserChat.reply("configured-private-value", null, messages, null).getString("message").contains("configured-private-value"),
                "configured key cannot be echoed in displayed answer");
        int previous = connections;
        fails("KEY", () -> BrowserChat.reply("", null, messages, null));
        fails("KEY", () -> BrowserChat.reply("bad\nheader", null, messages, null));
        check(connections == previous, "invalid credentials fail before opening connection");
        Thread.currentThread().interrupt();
        try { fails("CANCELLED", () -> BrowserChat.reply("test-key", null, messages, null)); }
        finally { Thread.interrupted(); }
        check(connections == previous, "interruption prevents network setup");
        List<HttpURLConnection> cancelled = new ArrayList<>();
        fails("CANCELLED", () -> BrowserChat.reply("test-key", null, messages, current -> {
            cancelled.add(current);
            if (current != null) throw new IllegalStateException("cancel");
        }));
        check(connection.disconnected && connection.sent.size() == 0 && cancelled.size() == 2
                && cancelled.get(1) == null, "callback cancellation sends no body and clears connection");
        timeout = true;
        fails("TIMEOUT", () -> BrowserChat.reply("test-key", null, messages, null));
        check(connection.disconnected, "timeout closes connection");
        timeout = false;
        for (int status : new int[]{401, 403, 429, 500, 302}) {
            nextStatus = status; nextBody = "{\"error\":{\"message\":\"never-display-private\"}}";
            String expected = status == 401 ? "KEY" : status == 403 ? "ACCESS" : status == 429 ? "LIMIT" : status == 500 ? "SERVICE" : "NETWORK";
            fails(expected, () -> BrowserChat.reply("test-key", null, messages, null));
            check(connection.disconnected, "HTTP error closes connection");
        }
        nextStatus = 400;
        for (String[] error : new String[][]{
                {"model_not_found", "", "MODEL"}, {"", "model", "MODEL"},
                {"context_length_exceeded", "", "INPUT_SIZE"}, {"", "input[0].content", "REQUEST_INPUT"},
                {"", "max_output_tokens", "REQUEST_LIMIT"}, {"unknown", "", "REQUEST"}}) {
            nextBody = new JSONObject().put("error", new JSONObject().put("code", error[0]).put("param", error[1])
                    .put("message", "never-display-private")).toString();
            fails(error[2], () -> BrowserChat.reply("test-key", null, messages, null));
        }
        nextBody = repeat("never-display-private", 1000);
        fails("REQUEST", () -> BrowserChat.reply("test-key", null, messages, null));
        nextStatus = 200;
        nextBody = "not-json never-display-private";
        fails("RESPONSE", () -> BrowserChat.reply("test-key", null, messages, null));
        nextBody = response("text").toString() + " trailing";
        fails("RESPONSE", () -> BrowserChat.reply("test-key", null, messages, null));
        nextBody = repeat("x", 131073);
        fails("RESPONSE_SIZE", () -> BrowserChat.reply("test-key", null, messages, null));
        System.out.println("BrowserChat: " + checks + " checks passed");
    }

    private static JSONObject message(String role, String content) throws Exception {
        return new JSONObject().put("role", role).put("content", content);
    }
    private static JSONObject response(String text) throws Exception {
        return new JSONObject().put("status", "completed").put("error", JSONObject.NULL).put("output", new JSONArray()
                .put(new JSONObject().put("type", "reasoning").put("summary", "not user-facing"))
                .put(new JSONObject().put("type", "message").put("role", "assistant").put("status", "completed")
                        .put("content", new JSONArray().put(new JSONObject().put("type", "output_text").put("text", text)))));
    }
    private static String repeat(String value, int length) {
        StringBuilder result = new StringBuilder();
        for (int i = 0; i < length; i++) result.append(value);
        return result.toString();
    }
    private static void check(boolean condition, String description) {
        if (!condition) throw new AssertionError(description);
        checks++;
    }
    private interface Attempt { void run() throws Exception; }
    private static void fails(String code, Attempt attempt) throws Exception {
        try { attempt.run(); throw new AssertionError("Expected " + code); }
        catch (BrowserChat.ChatException error) {
            check(code.equals(error.code), "safe error classification: expected " + code + ", got " + error.code);
            check(!error.getMessage().contains("never-display") && !error.getMessage().contains("configured-private"),
                    "error text never echoes upstream body");
        }
    }
    private static final class FakeConnection extends HttpURLConnection {
        final ByteArrayOutputStream sent = new ByteArrayOutputStream();
        boolean disconnected;
        FakeConnection(URL url) { super(url); }
        @Override public void connect() { }
        @Override public void disconnect() { disconnected = true; }
        @Override public boolean usingProxy() { return false; }
        @Override public OutputStream getOutputStream() throws SocketTimeoutException {
            if (timeout) throw new SocketTimeoutException("never-display-private");
            return sent;
        }
        @Override public int getResponseCode() { return nextStatus; }
        @Override public InputStream getInputStream() { return new ByteArrayInputStream(nextBody.getBytes(StandardCharsets.UTF_8)); }
        @Override public InputStream getErrorStream() { return getInputStream(); }
    }
}
