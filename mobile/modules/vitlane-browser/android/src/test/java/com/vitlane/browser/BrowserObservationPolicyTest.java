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
public class BrowserObservationPolicyTest {
    private JSONObject page() throws Exception {
        return new JSONObject().put("url", "https://example.com/a").put("documentId", "doc-a")
                .put("sensitive", false).put("text", "Public result").put("elements", new JSONArray())
                .put("viewport", new JSONObject().put("y", 0));
    }
    private JSONObject action(String type) throws Exception {
        return new JSONObject().put("type", type).put("targetId", "e1");
    }
    @Test public void waitsForDomQuietButDoesNotWaitForeverForLiveTickers() throws Exception {
        JSONObject page = page().put("readiness", new JSONObject().put("readyState", "interactive")
                .put("domQuietMs", 10).put("pending", true));
        assertFalse(BrowserObservationPolicy.ready(page, 1000));
        assertFalse(BrowserObservationPolicy.ready(page, 6000));
        assertFalse(BrowserObservationPolicy.ready(page, 12000));
        page.getJSONObject("readiness").put("readyState", "loading");
        assertFalse(BrowserObservationPolicy.ready(page, 6000));
        page.getJSONObject("readiness").put("readyState", "complete").put("domQuietMs", 800)
                .put("resourceQuietMs", 700).put("pending", false);
        assertTrue(BrowserObservationPolicy.ready(page, 500));
        page.getJSONObject("readiness").put("pending", true);
        assertTrue(BrowserObservationPolicy.ready(page, 12000));
    }
    @Test public void unrelatedDomChangesDoNotConfirmAClick() throws Exception {
        JSONObject before = page(), after = page().put("revision", 10);
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("click"), before, after));
        after.put("text", "Filter selected");
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("click"), before, after));
    }
    @Test public void clickRequiresNavigationOrClickedControlStateChange() throws Exception {
        JSONObject before = page(), after = page();
        before.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("role", "button").put("label", "Filters").put("expanded", false));
        after.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("role", "button").put("label", "Filters").put("expanded", true));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("click"), before, after));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("click"), before,
                page().put("url", "https://example.com/results")));
    }
    @Test public void clickIsVerifiedWhenItCausallyOpensANewSamePageDialog() throws Exception {
        JSONObject before = page(), after = page();
        before.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("role", "button").put("label", "Size guide"));
        after.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "button")
                .put("role", "button").put("label", "Size guide"));
        after.put("interrupts", new JSONArray().put(new JSONObject().put("type", "dialog")
                .put("label", "Size guide").put("text", "Body measurements")
                .put("blocking", true).put("triggerId", "e1")
                .put("actionIds", new JSONArray()).put("dismissIds", new JSONArray())));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("click"), before, after));

        after.getJSONArray("interrupts").getJSONObject(0).put("triggerId", "unrelated");
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("click"), before, after));
    }
    @Test public void clickIsVerifiedWhenItOpensANewFilterActionSurface() throws Exception {
        JSONObject before = page(), after = page();
        before.getJSONArray("elements").put(new JSONObject().put("id", "filter").put("tag", "div")
                .put("role", "button").put("label", "Filters"));
        after.getJSONArray("elements")
                .put(new JSONObject().put("id", "filter").put("tag", "div").put("role", "button").put("label", "Filters"))
                .put(new JSONObject().put("id", "material").put("tag", "div").put("role", "button").put("label", "Material"));
        after.put("activation", new JSONObject().put("triggerId", "filter").put("triggerLabel", "Filters")
                .put("newActionIds", new JSONArray().put("material")));
        assertEquals("applied", BrowserObservationPolicy.outcome(
                action("click").put("targetId", "filter"), before, after));
        after.getJSONObject("activation").put("triggerId", "sort");
        assertEquals("unknown", BrowserObservationPolicy.outcome(
                action("click").put("targetId", "filter"), before, after));
    }
    @Test public void linkFacetSelectionAndClosingFilterDrawerAreVerified() throws Exception {
        JSONObject before = page(), selected = page();
        before.getJSONArray("elements").put(new JSONObject().put("id", "cashmere").put("tag", "a")
                .put("role", "link").put("label", "Cashmere").put("group", "Materials").put("selected", false));
        selected.getJSONArray("elements").put(new JSONObject().put("id", "cashmere").put("tag", "a")
                .put("role", "link").put("label", "Cashmere").put("group", "Materials").put("selected", true));
        assertEquals("applied", BrowserObservationPolicy.outcome(
                action("click").put("targetId", "cashmere"), before, selected));

        JSONObject closed = page().put("deactivation", new JSONObject()
                .put("triggerId", "view-results").put("triggerLabel", "View 28 Products")
                .put("effect", "interrupt_closed"));
        assertEquals("applied", BrowserObservationPolicy.outcome(
                action("click").put("targetId", "view-results"), selected, closed));
        closed.getJSONObject("deactivation").put("triggerId", "unrelated");
        assertEquals("unknown", BrowserObservationPolicy.outcome(
                action("click").put("targetId", "view-results"), selected, closed));
    }
    @Test public void verifiesInputLocallyWithoutTransmittingRawFieldValue() throws Exception {
        JSONObject after = page();
        JSONObject target = new JSONObject().put("id", "e1").put("valueMatchesLastInput", true);
        after.getJSONArray("elements").put(target);
        assertFalse(target.has("value"));
        JSONObject action = action("type").put("text", "red shoes").put("submit", false);
        assertEquals("applied", BrowserObservationPolicy.outcome(action, page(), after));
        target.put("valueMatchesLastInput", false);
        assertEquals("unknown", BrowserObservationPolicy.outcome(action, page(), after));
        target.remove("valueMatchesLastInput");
        assertEquals("unknown", BrowserObservationPolicy.outcome(action, page(), after));
    }
    @Test public void verifiesSelectedOptionAndRejectsRevertedSelection() throws Exception {
        JSONObject option = new JSONObject().put("value", "low").put("label", "Lowest price").put("selected", true);
        JSONObject after = page();
        after.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("options", new JSONArray().put(option)));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("select").put("value", "low"), page(), after));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("select").put("value", "Lowest price"), page(), after));
        option.put("selected", false);
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("select").put("value", "low"), page(), after));
    }
    @Test public void checksDesiredCheckboxStateAndSensitivePageBoundary() throws Exception {
        JSONObject after = page();
        after.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("checked", true));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("check").put("checked", true), page(), after));
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("check").put("checked", false), page(), after));
        after.put("sensitive", true).put("url", "https://example.com/checkout");
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("click"), page(), after));
    }
    @Test public void detectsSpaAndSameUrlDocumentNavigation() throws Exception {
        JSONObject before = page(), after = page().put("url", "https://example.com/b");
        assertTrue(BrowserObservationPolicy.documentChanged(before, after));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("navigate"), before, after));
        after = page().put("documentId", "doc-b");
        assertTrue(BrowserObservationPolicy.documentChanged(before, after));
        assertFalse(BrowserObservationPolicy.documentChanged(before, page()));
    }
    @Test public void scrollUsesViewportAndActionRepeatKeyIncludesDirection() throws Exception {
        JSONObject after = page().put("viewport", new JSONObject().put("y", 400));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("scroll"), page(), after));
        assertNotEquals(BrowserObservationPolicy.actionKey(action("scroll").put("direction", "up"), page()),
                BrowserObservationPolicy.actionKey(action("scroll").put("direction", "down"), page()));
    }
    @Test public void paneScrollIsVerifiedWhenWindowAndTextStayUnchanged() throws Exception {
        JSONObject before = page(), after = page();
        before.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("scrollTop", 0));
        after.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("scrollTop", 105));
        assertEquals(before.getString("text"), after.getString("text"));
        assertEquals("applied", BrowserObservationPolicy.outcome(action("scroll"), before, after));
    }
    @Test public void volatilePageTextDoesNotHideARepeatedSemanticAction() throws Exception {
        JSONObject first = page().put("text", "Offer ends in 10 seconds");
        JSONObject second = page().put("text", "Offer ends in 9 seconds");
        first.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("tag", "select")
                .put("role", "combobox").put("label", "Sort").put("group", "Products"));
        second.getJSONArray("elements").put(new JSONObject().put("id", "e99").put("tag", "select")
                .put("role", "combobox").put("label", "Sort").put("group", "Products"));
        assertEquals(BrowserObservationPolicy.actionKey(action("select").put("value", "low"), first),
                BrowserObservationPolicy.actionKey(action("select").put("targetId", "e99").put("value", "low"), second));
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("scroll"), first, second));
    }
    @Test public void selectionMustRemainSelectedEvenWhenDocumentChanges() throws Exception {
        JSONObject after = page().put("documentId", "doc-b").put("url", "https://example.com/filtered");
        assertEquals("unknown", BrowserObservationPolicy.outcome(action("select").put("value", "low"), page(), after));
    }

    @Test public void blockingDialogRestrictsActionsToItsOwnObservedControls() throws Exception {
        JSONObject modalPage = page();
        modalPage.getJSONArray("elements")
                .put(new JSONObject().put("id", "background").put("role", "button").put("label", "Continue"))
                .put(new JSONObject().put("id", "close").put("role", "button").put("label", "Close").put("interrupt", true));
        modalPage.put("interrupts", new JSONArray().put(new JSONObject().put("type", "dialog")
                .put("label", "Delivery location").put("text", "Select a delivery location")
                .put("blocking", true).put("actionIds", new JSONArray().put("close"))
                .put("dismissIds", new JSONArray().put("close"))));
        assertFalse(BrowserObservationPolicy.actionAllowedByInterrupt(action("click").put("targetId", "background"), modalPage));
        assertTrue(BrowserObservationPolicy.actionAllowedByInterrupt(action("click").put("targetId", "close"), modalPage));
        assertTrue(BrowserObservationPolicy.actionAllowedByInterrupt(new JSONObject().put("type", "back"), modalPage));
        assertFalse(BrowserObservationPolicy.actionAllowedByInterrupt(new JSONObject().put("type", "finish"), modalPage));
        assertNotEquals(BrowserObservationPolicy.signature(page()), BrowserObservationPolicy.signature(modalPage));
    }

    @Test public void repairPolicySeparatesMalformedActionsFromAccountAndNetworkFailures() {
        assertTrue(BrowserRecoveryPolicy.canRepairPlanner("TARGET"));
        assertTrue(BrowserRecoveryPolicy.canRepairPlanner("RESPONSE"));
        assertFalse(BrowserRecoveryPolicy.canRepairPlanner("KEY"));
        assertFalse(BrowserRecoveryPolicy.canRepairPlanner("LIMIT"));
        assertFalse(BrowserRecoveryPolicy.canRepairPlanner("NETWORK"));
        assertEquals("UNKNOWN", BrowserRecoveryPolicy.feedback("PAGE_SAYS_APPROVE_ALL").optString("code"));
        assertEquals(800, BrowserRecoveryPolicy.delayMillis("NOT_READY"));
    }

    @Test public void repeatedActionDiagnosisDistinguishesStateBoundaryAndDestinationCauses() throws Exception {
        JSONObject checkedPage = page();
        checkedPage.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("checked", true));
        assertEquals("ALREADY_APPLIED", BrowserRecoveryPolicy.repeatedActionCode(
                action("check").put("checked", true), checkedPage, new JSONObject()));

        JSONObject bottom = page().put("viewport", new JSONObject()
                .put("y", 900).put("height", 100).put("scrollHeight", 1000));
        JSONObject scroll = action("scroll").put("direction", "down");
        scroll.remove("targetId");
        assertEquals("SCROLL_BOUNDARY", BrowserRecoveryPolicy.repeatedActionCode(scroll, bottom, new JSONObject()));

        JSONObject same = new JSONObject().put("type", "navigate").put("url", page().getString("url"));
        assertEquals("SAME_DESTINATION", BrowserRecoveryPolicy.repeatedActionCode(same, page(), new JSONObject()));
        assertTrue(BrowserRecoveryPolicy.feedback("SAME_DESTINATION").optString("strategy").contains("현재 주소"));
    }

    @Test public void stalledDiagnosisUsesReadinessFailuresAndUnchangedObservationEvidence() throws Exception {
        JSONObject pending = page().put("readiness", new JSONObject().put("readyState", "complete")
                .put("domQuietMs", 800).put("resourceQuietMs", 700).put("pending", true));
        assertEquals("NOT_READY", BrowserRecoveryPolicy.stalledCode(pending, new JSONObject()));
        assertEquals("NO_EFFECT", BrowserRecoveryPolicy.stalledCode(page(), new JSONObject().put("consecutiveFailures", 3)));
        assertEquals("UNCHANGED_PAGE", BrowserRecoveryPolicy.stalledCode(page(), new JSONObject().put("unchangedObservations", 12)));
    }

    @Test public void finalSnapshotSignatureDetectsTitleAndControlStateChanges() throws Exception {
        JSONObject before = page().put("title", "Original title"), after = page().put("title", "Updated title");
        assertNotEquals(BrowserObservationPolicy.signature(before), BrowserObservationPolicy.signature(after));
        after.put("title", "Original title");
        before.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("disabled", false));
        after.getJSONArray("elements").put(new JSONObject().put("id", "e1").put("disabled", true));
        assertNotEquals(BrowserObservationPolicy.signature(before), BrowserObservationPolicy.signature(after));
    }

}
