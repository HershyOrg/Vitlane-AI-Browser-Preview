package com.vitlane.browser;

import org.json.JSONArray;
import org.json.JSONObject;
import org.json.JSONTokener;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.io.OutputStream;
import java.net.HttpURLConnection;
import java.net.SocketTimeoutException;
import java.net.URL;
import java.nio.charset.StandardCharsets;
import java.util.ArrayList;
import java.util.List;
import java.util.function.Consumer;

/** Plain conversation only. This client never observes pages or proposes executable browser actions. */
public final class BrowserChat {
    private static final String ENDPOINT = "https://api.openai.com/v1/responses";
    private static final int MAX_MESSAGES = 24;
    private static final int MAX_MESSAGE_CHARS = 12000;
    private static final int MAX_HISTORY_CHARS = 60000;
    private static final int MAX_REQUEST_BYTES = 262144;
    private static final int MAX_RESPONSE_BYTES = 131072;
    private static final String INSTRUCTIONS =
            "당신은 비트레인의 일반 대화 도우미입니다. 기본적으로 한국어로 명확하고 자연스럽게 답하세요. "
            + "현재 브라우저 사용은 꺼져 있습니다. 이 요청에는 웹페이지, 브라우저 상태, 검색 도구나 실행 도구가 없습니다. "
            + "대화에 제공된 내용과 일반 지식을 바탕으로 질문에 답하고, 설명·글쓰기·계산·계획을 도우세요. "
            + "웹사이트를 읽거나 검색·클릭·입력·구매 등의 작업을 수행했다고 주장하지 마세요. "
            + "실시간 확인이나 사이트 조작이 필요한 요청이면 그 한계를 짧게 밝히고 브라우저 사용을 켜도록 안내하세요. "
            + "필요한 정보가 없으면 짧게 질문하세요. API 키, 비밀번호, 인증 코드 등의 비밀 정보를 요청하거나 반복하지 마세요. "
            + "답변은 일반 텍스트로 작성하고, 앱 실행용 JSON 동작이나 도구 호출을 출력하지 마세요. "
            + "사용자가 제공한 코드나 JSON을 설명하는 것은 가능합니다.";

    private BrowserChat() {}

    /** Run off the UI thread. Connection ownership and cancellation match BrowserAgent.plan. */
    public static JSONObject reply(String apiKey, String model, JSONArray messages,
            Consumer<HttpURLConnection> onConnection) throws Exception {
        HttpURLConnection connection = null;
        try {
            String key = apiKey == null ? "" : apiKey.trim();
            if (key.isEmpty() || key.length() > 4096 || !key.matches("[!-~]+")) throw failure("KEY");
            JSONObject request = buildRequest(model, messages);
            // Even a nonstandard configured key pasted into chat must stay out of the message body.
            JSONArray input = request.getJSONArray("input");
            for (int i = 1; i < input.length(); i++) {
                JSONObject message = input.getJSONObject(i);
                message.put("content", message.getString("content").replace(key, "[비공개]"));
            }
            byte[] body = request.toString().getBytes(StandardCharsets.UTF_8);
            if (body.length > MAX_REQUEST_BYTES) throw failure("INPUT_SIZE");
            checkInterrupted();
            connection = (HttpURLConnection)new URL(ENDPOINT).openConnection();
            connection.setRequestMethod("POST");
            connection.setInstanceFollowRedirects(false);
            connection.setConnectTimeout(10000);
            connection.setReadTimeout(120000);
            connection.setUseCaches(false);
            connection.setDoOutput(true);
            connection.setRequestProperty("Authorization", "Bearer " + key);
            connection.setRequestProperty("Content-Type", "application/json");
            connection.setRequestProperty("Accept", "application/json");
            connection.setFixedLengthStreamingMode(body.length);
            if (onConnection != null) {
                try { onConnection.accept(connection); }
                catch (RuntimeException cancelled) { throw failure("CANCELLED"); }
            }
            checkInterrupted();
            long deadline = System.nanoTime() + 120_000_000_000L;
            try (OutputStream output = connection.getOutputStream()) { output.write(body); }
            int status = connection.getResponseCode();
            if (status != 200) {
                if (status == 401) throw failure("KEY");
                if (status == 403) throw failure("ACCESS");
                if (status == 429) throw failure("LIMIT");
                if (status == 400 || status == 404) throw requestFailure(connection);
                if (status >= 500) throw failure("SERVICE");
                throw failure("NETWORK");
            }
            try (InputStream inputStream = connection.getInputStream();
                    ByteArrayOutputStream output = new ByteArrayOutputStream()) {
                byte[] buffer = new byte[4096];
                int count;
                while ((count = inputStream.read(buffer)) != -1) {
                    checkInterrupted();
                    if (System.nanoTime() > deadline) throw failure("TIMEOUT");
                    if (output.size() + count > MAX_RESPONSE_BYTES) throw failure("RESPONSE_SIZE");
                    output.write(buffer, 0, count);
                }
                JSONObject reply = replyFromResponse(parseObject(output.toString("UTF-8")));
                reply.put("message", reply.getString("message").replace(key, "[비공개]"));
                return reply;
            }
        } catch (ChatException error) {
            throw error;
        } catch (SocketTimeoutException error) {
            throw failure(Thread.currentThread().isInterrupted() ? "CANCELLED" : "TIMEOUT");
        } catch (Exception error) {
            throw failure(Thread.currentThread().isInterrupted() ? "CANCELLED" : "NETWORK");
        } finally {
            if (connection != null) {
                connection.disconnect();
                if (onConnection != null) {
                    try { onConnection.accept(null); } catch (RuntimeException ignored) { }
                }
            }
        }
    }

    /** No browser goal, observation, tools, API key or model-supplied system roles enter this request. */
    public static JSONObject buildRequest(String model, JSONArray messages) throws Exception {
        String selected = model == null || model.trim().isEmpty() ? BrowserAgent.DEFAULT_MODEL : model.trim();
        if (!selected.matches("[A-Za-z0-9][A-Za-z0-9._:/-]{0,199}")) throw failure("MODEL");
        JSONArray history = sanitizeMessages(messages);
        if (history.length() == 0 || !"user".equals(history.getJSONObject(history.length() - 1).getString("role")))
            throw failure("INPUT");
        JSONArray input = new JSONArray().put(new JSONObject().put("role", "system").put("content", INSTRUCTIONS));
        for (int i = 0; i < history.length(); i++) input.put(history.getJSONObject(i));
        boolean sol = "gpt-6-sol".equals(selected);
        JSONObject request = new JSONObject().put("model", selected).put("input", input)
                .put("store", false).put("max_output_tokens", sol ? 16384 : 4096);
        if (sol) request.put("reasoning", new JSONObject().put("effort", "medium"));
        if (request.toString().getBytes(StandardCharsets.UTF_8).length > MAX_REQUEST_BYTES) throw failure("INPUT_SIZE");
        return request;
    }

    /** Keep a bounded recent suffix, copying only plain user/assistant text and redacting obvious credentials. */
    public static JSONArray sanitizeMessages(JSONArray source) throws Exception {
        if (source == null) throw failure("INPUT");
        List<JSONObject> reversed = new ArrayList<>();
        int chars = 0;
        for (int i = source.length() - 1; i >= 0 && reversed.size() < MAX_MESSAGES; i--) {
            JSONObject item = source.optJSONObject(i);
            if (item == null || !(item.opt("role") instanceof String) || !(item.opt("content") instanceof String))
                throw failure("INPUT");
            String role = item.getString("role");
            if (!"user".equals(role) && !"assistant".equals(role)) throw failure("INPUT");
            String raw = item.getString("content");
            if (raw.length() > MAX_MESSAGE_CHARS) throw failure("INPUT_SIZE");
            String content = cleanText(raw);
            if (content.isEmpty()) throw failure("INPUT");
            if (chars + content.length() > MAX_HISTORY_CHARS) break;
            chars += content.length();
            reversed.add(new JSONObject().put("role", role).put("content", content));
        }
        JSONArray result = new JSONArray();
        for (int i = reversed.size() - 1; i >= 0; i--) result.put(reversed.get(i));
        return result;
    }

    /** Extract only completed assistant text, ignoring reasoning and rejecting unexpected executable items. */
    public static JSONObject replyFromResponse(JSONObject response) throws Exception {
        try {
            boolean incomplete = response != null && "incomplete".equals(response.optString("status"));
            if (response == null || (!incomplete && !"completed".equals(response.optString("status"))) || !response.isNull("error"))
                throw failure("INCOMPLETE");
            JSONArray output = response.optJSONArray("output");
            if (output == null || output.length() > 100) throw failure("RESPONSE");
            StringBuilder answer = new StringBuilder();
            for (int i = 0; i < output.length(); i++) {
                JSONObject item = output.getJSONObject(i);
                String type = item.optString("type");
                if ("reasoning".equals(type)) continue;
                if (!"message".equals(type) || !"assistant".equals(item.optString("role"))) throw failure("RESPONSE");
                if (item.has("status") && !"completed".equals(item.optString("status"))
                        && !(incomplete && "incomplete".equals(item.optString("status")))) throw failure("INCOMPLETE");
                JSONArray content = item.getJSONArray("content");
                if (content.length() > 100) throw failure("RESPONSE");
                if (answer.length() > 0) answer.append("\n\n");
                for (int j = 0; j < content.length(); j++) {
                    JSONObject part = content.getJSONObject(j);
                    if ("refusal".equals(part.optString("type"))) throw failure("REFUSAL");
                    if (!"output_text".equals(part.optString("type")) || !(part.opt("text") instanceof String))
                        throw failure("RESPONSE");
                    String text = part.getString("text");
                    if (answer.length() + text.length() > MAX_MESSAGE_CHARS) throw failure("RESPONSE_SIZE");
                    answer.append(text);
                }
            }
            String message = cleanText(answer.toString());
            if (message.isEmpty()) throw failure(incomplete ? "INCOMPLETE" : "RESPONSE");
            // Action-looking text remains text; it is never parsed or forwarded to the page dispatcher.
            JSONObject result = new JSONObject().put("type", "chat").put("message", message);
            if (incomplete) result.put("incomplete", true);
            return result;
        } catch (ChatException error) {
            throw error;
        } catch (Exception malformed) {
            throw failure("RESPONSE");
        }
    }

    private static String cleanText(String text) {
        return text.replaceAll("[\\x00-\\x08\\x0B\\x0C\\x0E-\\x1F\\x7F]", "")
                .replaceAll("(?i)\\bsk-[A-Za-z0-9_-]{8,}", "[비공개]")
                .replaceAll("(?i)\\bBearer\\s+[A-Za-z0-9._~+/=-]{8,}", "Bearer [비공개]")
                .replaceAll("(?i)\\b(?:password|passwd|passcode|otp|cvv|cvc|access[_ -]?token|api[_ -]?key|secret)\\s*[:=]\\s*[^\\s<>&]+", "[비공개]")
                .replaceAll("(?:API\\s*키|비밀번호|인증번호|접근\\s*토큰)\\s*[:=]\\s*[^\\s<>&]+", "[비공개]")
                .trim();
    }

    private static JSONObject parseObject(String raw) throws ChatException {
        try {
            JSONTokener parser = new JSONTokener(raw);
            Object value = parser.nextValue();
            if (!(value instanceof JSONObject) || parser.nextClean() != 0) throw failure("RESPONSE");
            return (JSONObject)value;
        } catch (Exception malformed) { throw failure("RESPONSE"); }
    }

    private static ChatException requestFailure(HttpURLConnection connection) {
        // Upstream errors can echo user text or credentials: classify only known fields, never show raw bodies.
        try {
            connection.setReadTimeout(5000);
            long deadline = System.nanoTime() + 5_000_000_000L;
            try (InputStream input = connection.getErrorStream(); ByteArrayOutputStream output = new ByteArrayOutputStream()) {
                if (input == null) return failure("REQUEST");
                byte[] buffer = new byte[1024];
                int count;
                while ((count = input.read(buffer)) != -1) {
                    checkInterrupted();
                    if (System.nanoTime() > deadline || output.size() + count > 8192) return failure("REQUEST");
                    output.write(buffer, 0, count);
                }
                JSONObject error = parseObject(output.toString("UTF-8")).optJSONObject("error");
                if (error == null) return failure("REQUEST");
                String code = error.optString("code"), param = error.optString("param");
                if ("model_not_found".equals(code) || "model".equals(param)) return failure("MODEL");
                if ("context_length_exceeded".equals(code)) return failure("INPUT_SIZE");
                if ("input".equals(param) || param.startsWith("input[")) return failure("REQUEST_INPUT");
                if ("max_output_tokens".equals(param)) return failure("REQUEST_LIMIT");
            }
        } catch (Exception ignored) { }
        return failure(Thread.currentThread().isInterrupted() ? "CANCELLED" : "REQUEST");
    }

    private static void checkInterrupted() throws ChatException {
        if (Thread.currentThread().isInterrupted()) throw failure("CANCELLED");
    }

    private static ChatException failure(String code) {
        String message;
        switch (code) {
            case "KEY": message = "OpenAI API 키를 확인해 주세요."; break;
            case "ACCESS": message = "이 API 키로 모델에 접근할 수 없습니다."; break;
            case "MODEL": message = "설정한 모델을 찾을 수 없거나 이 API 키에 사용 권한이 없습니다. 모델 ID와 API 프로젝트 권한을 확인해 주세요."; break;
            case "LIMIT": message = "OpenAI API 사용량 또는 잔액을 확인한 뒤 다시 시도해 주세요."; break;
            case "SERVICE": message = "OpenAI 서비스에 일시적인 문제가 있습니다. 잠시 후 다시 시도해 주세요."; break;
            case "TIMEOUT": message = "OpenAI 응답 시간이 초과되었습니다. 다시 시도해 주세요."; break;
            case "CANCELLED": message = "요청을 중지했습니다."; break;
            case "INPUT": message = "대화 내용을 확인한 뒤 다시 보내 주세요."; break;
            case "INPUT_SIZE": message = "대화 내용이 너무 깁니다. 메시지를 줄여 다시 보내 주세요."; break;
            case "INCOMPLETE": message = "응답이 완료되지 않았습니다. 요청을 줄여 다시 시도해 주세요."; break;
            case "REFUSAL": message = "이 요청에 답변할 수 없습니다. 내용을 바꿔 다시 시도해 주세요."; break;
            case "RESPONSE": case "RESPONSE_SIZE": message = "OpenAI 응답을 읽을 수 없습니다. 다시 시도해 주세요."; break;
            case "REQUEST_INPUT": message = "OpenAI가 대화 입력 형식을 거절했습니다. 최신 앱으로 업데이트해 주세요."; break;
            case "REQUEST_LIMIT": message = "OpenAI가 응답 길이 설정을 거절했습니다. 최신 앱으로 업데이트해 주세요."; break;
            case "REQUEST": message = "OpenAI가 대화 요청을 거절했습니다. 모델 오류인지 확인되지 않았습니다. 최신 앱으로 업데이트해 주세요."; break;
            default: message = "OpenAI에 연결할 수 없습니다. 인터넷 연결을 확인해 주세요.";
        }
        return new ChatException(code, message);
    }

    public static final class ChatException extends Exception {
        public final String code;
        private ChatException(String code, String message) { super(message); this.code = code; }
    }
}
