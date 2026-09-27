package com.vitlane.browser;

import static org.junit.Assert.*;
import org.json.JSONArray;
import org.json.JSONObject;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28, manifest = Config.NONE)
public class BrowserPageScriptPrivacyTest {
    @Test public void pageExecutionCannotReceiveCrossPageMemoryOrPlannerMessages() throws Exception {
        JSONObject action = new JSONObject().put("type", "type").put("targetId", "e_fixture_1")
                .put("text", "Public draft").put("submit", false).put("approved", true)
                .put("message", "PRIVATE_PLANNER_MESSAGE_SENTINEL")
                .put("state", new JSONObject().put("facts", "PRIVATE_CROSS_PAGE_FACT_SENTINEL"))
                .put("task", new JSONObject().put("goal", "PRIVATE_TASK_LEDGER_SENTINEL"))
                .put("evidence", "PRIVATE_EVIDENCE_SENTINEL").put("goal", "PRIVATE_GOAL_SENTINEL");
        String script = BrowserPageScript.action(action, "https://shop.example.com/", "d_fixture");
        assertTrue(script.contains("\"targetId\":\"e_fixture_1\""));
        assertTrue(script.contains("\"text\":\"Public draft\""));
        assertTrue(script.contains("\"approved\":true"));
        for (String privateContent : new String[] {"PRIVATE_PLANNER_MESSAGE_SENTINEL", "PRIVATE_CROSS_PAGE_FACT_SENTINEL",
                "PRIVATE_EVIDENCE_SENTINEL", "PRIVATE_GOAL_SENTINEL", "PRIVATE_TASK_LEDGER_SENTINEL"}) assertFalse(script.contains(privateContent));
        assertTrue(action.has("state")); // Native memory stays available to the caller.
    }

    @Test public void selectionAndScrollExecutionFieldsAreRetained() throws Exception {
        String script = BrowserPageScript.action(new JSONObject().put("type", "select").put("value", "large")
                .put("checked", true).put("direction", "down").put("targetId", "e_fixture_2"),
                "https://shop.example.com/", "d_fixture");
        assertTrue(script.contains("\"value\":\"large\""));
        assertTrue(script.contains("\"checked\":true"));
        assertTrue(script.contains("\"direction\":\"down\""));
        assertTrue(script.endsWith("," + JSONObject.quote("https://shop.example.com/") + ",\"d_fixture\")"));
    }

    @Test public void localHumanAnswersUseOnlyAnExplicitFieldValueAllowlist() throws Exception {
        JSONObject command = new JSONObject().put("values", new JSONArray().put(new JSONObject()
                .put("id", "he_fixture_1").put("value", "Example Person")
                .put("label", "PRIVATE_LABEL_SENTINEL").put("submit", true)))
                .put("state", "PRIVATE_STATE_SENTINEL").put("task", "PRIVATE_TASK_SENTINEL")
                .put("approved", true).put("submit", true);
        String script = BrowserPageScript.fillHumanFields(command, "https://shop.example.com/shipping", "d_fixture");
        String invocation = script.substring(script.lastIndexOf(").fillHumanFields("));
        assertTrue(invocation.contains("\"id\":\"he_fixture_1\""));
        assertTrue(invocation.contains("\"value\":\"Example Person\""));
        for (String disallowed : new String[] { "PRIVATE_LABEL_SENTINEL", "PRIVATE_STATE_SENTINEL", "PRIVATE_TASK_SENTINEL", "submit", "approved" }) {
            assertFalse(invocation.contains(disallowed));
        }
        assertTrue(invocation.endsWith("," + JSONObject.quote("https://shop.example.com/shipping") + ",\"d_fixture\")"));
        assertTrue(BrowserPageScript.humanFields().endsWith(").humanFields()"));
        assertTrue(BrowserPageScript.manualState().endsWith(").manualState()"));
    }

    @Test public void malformedLocalBatchNeverPartiallySerializesAnswers() throws Exception {
        JSONObject command = new JSONObject().put("values", new JSONArray()
                .put(new JSONObject().put("id", "he_fixture_1").put("value", "PRIVATE_PARTIAL_VALUE_SENTINEL"))
                .put(new JSONObject().put("id", "he_fixture_2").put("value", new JSONObject())));
        String script = BrowserPageScript.fillHumanFields(command, null, null);
        assertFalse(script.contains("PRIVATE_PARTIAL_VALUE_SENTINEL"));
        assertTrue(script.endsWith(").fillHumanFields({\"values\":[]},\"\",\"\")"));
        assertTrue(BrowserPageScript.fillHumanFields(null, null, null).endsWith(").fillHumanFields({\"values\":[]},\"\",\"\")"));
    }

    @Test public void secretChannelSerializesOnlyTargetAndValueToTheCurrentDocument() throws Exception {
        JSONObject command = new JSONObject().put("values", new JSONArray().put(new JSONObject()
                .put("id", "se_fixture_1").put("value", "LOCAL_SECRET_SENTINEL")
                .put("kind", "password").put("label", "PRIVATE_FIELD_LABEL")
                .put("vaultId", 42).put("purpose", "PRIVATE_USE_PURPOSE")))
                .put("history", "PRIVATE_HISTORY_SENTINEL").put("goal", "PRIVATE_GOAL_SENTINEL");
        String script = BrowserPageScript.fillSecretFields(command, "https://shop.example.com/login", "d_fixture");
        String invocation = script.substring(script.lastIndexOf(").fillSecretFields("));
        assertTrue(invocation.contains("\"id\":\"se_fixture_1\""));
        assertTrue(invocation.contains("\"value\":\"LOCAL_SECRET_SENTINEL\""));
        for (String disallowed : new String[] {"kind", "PRIVATE_FIELD_LABEL", "vaultId", "PRIVATE_USE_PURPOSE",
                "PRIVATE_HISTORY_SENTINEL", "PRIVATE_GOAL_SENTINEL"}) assertFalse(invocation.contains(disallowed));
        assertTrue(invocation.endsWith("," + JSONObject.quote("https://shop.example.com/login") + ",\"d_fixture\")"));
        assertTrue(BrowserPageScript.secretFields().endsWith(").secretFields()"));
    }
}
