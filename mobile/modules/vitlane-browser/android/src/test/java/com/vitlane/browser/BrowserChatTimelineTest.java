package com.vitlane.browser;

import static org.junit.Assert.*;

import android.graphics.Bitmap;
import android.graphics.Color;
import android.graphics.drawable.BitmapDrawable;
import android.graphics.drawable.ColorDrawable;
import android.view.View;
import android.view.ViewGroup;
import android.widget.Button;
import android.widget.ImageView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.atomic.AtomicInteger;

import org.json.JSONArray;
import org.json.JSONObject;

import org.junit.After;
import org.junit.Before;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.RuntimeEnvironment;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28, manifest = Config.NONE)
public class BrowserChatTimelineTest {
    private BrowserChatTimeline timeline;
    private final List<Bitmap> originals = new ArrayList<>();

    @Before public void setUp() { timeline = new BrowserChatTimeline(RuntimeEnvironment.getApplication()); }
    @After public void tearDown() {
        timeline.release();
        for (Bitmap bitmap : originals) if (!bitmap.isRecycled()) bitmap.recycle();
    }

    @Test public void rendersOrderedSelectableMessagesOnAnOpaqueSurfaceWithoutLinkNavigation() {
        timeline.addUser("신발을 찾아 줘");
        timeline.addAssistant("결과 https://example.com/details");
        ScrollView view = timeline.getView();
        assertEquals(Color.WHITE, ((ColorDrawable) view.getBackground()).getColor());
        List<TextView> texts = descendants(view, TextView.class);
        assertEquals("나", texts.get(0).getText().toString());
        assertEquals("신발을 찾아 줘", texts.get(1).getText().toString());
        assertEquals("Vitlane AI", texts.get(2).getText().toString());
        assertEquals("결과 https://example.com/details", texts.get(3).getText().toString());
        assertTrue(texts.get(1).isTextSelectable());
        assertTrue(texts.get(3).isTextSelectable());
        assertEquals(0, texts.get(3).getAutoLinkMask());
        assertFalse(texts.get(3).getLinksClickable());
    }

    @Test public void rendersReusableCatalogCardsWithoutOpeningTheBrowser() throws Exception {
        List<View> cards = timeline.addCatalog(new JSONArray().put(new JSONObject().put("title", "오전 직항")
                .put("subtitle", "09:10 출발 · 12:30 도착").put("price", "₩420,000")
                .put("badges", new JSONArray().put("직항").put("시간 조건 충족"))));
        String rendered = allText(timeline.getView());
        assertTrue(rendered.contains("오전 직항"));
        assertTrue(rendered.contains("09:10 출발"));
        assertTrue(rendered.contains("₩420,000"));
        assertTrue(rendered.contains("직항  ·  시간 조건 충족"));
        assertEquals(0, descendants(timeline.getView(), Button.class).size());
        assertEquals(1, cards.size());
        Bitmap source = bitmap(640, 480);
        timeline.attachCatalogImage(cards.get(0), source, "오전 직항 대표 이미지");
        assertEquals(1, descendants(cards.get(0), ImageView.class).size());
        assertEquals("오전 직항 대표 이미지", descendants(cards.get(0), ImageView.class).get(0).getContentDescription());
        Bitmap owned = imageBitmap(descendants(cards.get(0), ImageView.class).get(0));
        timeline.clear();
        assertTrue(owned.isRecycled());
        assertEquals(0, messageContainer().getChildCount());
    }

    @Test public void starterSuggestionsAreAccessibleAndDisappearAfterTheFirstUserMessage() {
        List<View> actions = timeline.addSuggestions("상품 비교", "여행 탐색", "자료 조사");
        assertEquals(3, actions.size());
        assertEquals("상품 비교", actions.get(0).getContentDescription());
        assertTrue(allText(timeline.getView()).contains("무엇을 해볼까요?"));
        assertEquals(1, messageContainer().getChildCount());
        timeline.addUser("상품을 찾아줘");
        assertFalse(allText(timeline.getView()).contains("무엇을 해볼까요?"));
        assertEquals(1, messageContainer().getChildCount());
        assertTrue(allText(timeline.getView()).contains("상품을 찾아줘"));
    }

    @Test public void asynchronousAttachmentStaysWithItsOriginalMessageAndLabelsHistoricalSource() {
        View first = timeline.addAssistantMessage("첫 번째 결과");
        View second = timeline.addAssistantMessage("두 번째 결과");
        AtomicInteger opened = new AtomicInteger();
        timeline.attachScreenshot(first, bitmap(100, 200), "https://member:private@shop.example.com/details?token=PRIVATE_TOKEN#PRIVATE_FRAGMENT", opened::incrementAndGet);
        assertEquals(1, descendants(first, ImageView.class).size());
        assertEquals(0, descendants(second, ImageView.class).size());
        String text = allText(first);
        assertTrue(text.contains("캡처한 화면 · "));
        assertTrue(text.contains("shop.example.com"));
        assertTrue(text.contains("현재 페이지는 이 화면과 다를 수 있어요."));
        assertFalse(text.contains("private"));
        assertFalse(text.contains("PRIVATE_TOKEN"));
        assertFalse(text.contains("PRIVATE_FRAGMENT"));
        assertEquals("현재 브라우저 열기", descendants(first, Button.class).get(0).getText().toString());
        descendants(first, ImageView.class).get(0).performClick();
        descendants(first, Button.class).get(0).performClick();
        assertEquals(2, opened.get());
        assertEquals(2, messageContainer().getChildCount());
        assertSame(first.getParent(), messageContainer().getChildAt(0));
    }

    @Test public void boundedThumbnailPreservesAspectRatioAndIndependentOwnership() {
        Bitmap original = bitmap(1600, 2400);
        View message = timeline.addAssistantMessage("화면 확인");
        timeline.attachScreenshot(message, original, "https://shop.example.com/", null);
        Bitmap owned = imageBitmap(descendants(message, ImageView.class).get(0));
        assertNotSame(original, owned);
        assertEquals(640, owned.getWidth());
        assertEquals(960, owned.getHeight());
        original.recycle();
        assertFalse(owned.isRecycled());
        assertEquals(ImageView.ScaleType.FIT_CENTER, descendants(message, ImageView.class).get(0).getScaleType());
        timeline.clear();
        assertTrue(owned.isRecycled());
    }

    @Test public void smallImagesAreCopiedWithoutUpscalingAndReplacementsRecycleOnlyOwnedImage() {
        View message = timeline.addAssistantMessage("결과");
        Bitmap original = bitmap(50, 30);
        timeline.attachScreenshot(message, original, "https://first.example.com", null);
        Bitmap first = imageBitmap(descendants(message, ImageView.class).get(0));
        assertEquals(50, first.getWidth());
        assertEquals(30, first.getHeight());
        timeline.attachScreenshot(message, original, "https://second.example.com", null);
        assertTrue(first.isRecycled());
        assertFalse(original.isRecycled());
        assertEquals(1, descendants(message, ImageView.class).size());
        assertFalse(allText(message).contains("first.example.com"));
        assertTrue(allText(message).contains("second.example.com"));
    }

    @Test public void sourceImageIsLabeledAsSiteMediaInsteadOfAScreenshot() {
        View message = timeline.addAssistantMessage("상품 이미지");
        Bitmap original = bitmap(320, 200);
        timeline.attachSourceImage(message, original, "https://shop.example.com/products/alpha", "파란 러닝화");
        assertEquals(1, descendants(message, ImageView.class).size());
        assertTrue(allText(message).contains("페이지 원본 이미지 · shop.example.com"));
        assertTrue(allText(message).contains("사이트가 제공한 원본 이미지를 직접 불러왔어요."));
        assertFalse(allText(message).contains("캡처한 화면"));
        assertEquals("파란 러닝화", descendants(message, ImageView.class).get(0).getContentDescription().toString());
        assertFalse(original.isRecycled());
    }

    @Test public void sixScreenshotLimitRecyclesOldPreviewWhileKeepingConversationAndCurrentBrowserButton() {
        AtomicInteger opened = new AtomicInteger();
        View first = timeline.addAssistantMessage("첫 결과");
        Bitmap original = bitmap(80, 100);
        timeline.attachScreenshot(first, original, "https://shop.example.com", opened::incrementAndGet);
        Bitmap firstCopy = imageBitmap(descendants(first, ImageView.class).get(0));
        for (int i = 0; i < 6; i++) timeline.addAssistant("결과 " + i, original, "https://shop.example.com", null);
        assertEquals(6, descendants(timeline.getView(), ImageView.class).size());
        assertTrue(firstCopy.isRecycled());
        assertFalse(original.isRecycled());
        assertEquals(7, messageContainer().getChildCount());
        assertTrue(allText(first).contains("첫 결과"));
        assertTrue(allText(first).contains("이전 미리보기는 메모리에서 삭제됐어요."));
        descendants(first, Button.class).get(0).performClick();
        assertEquals(1, opened.get());
    }

    @Test public void combinedScreenshotMemoryIsBoundedEvenBeforeSixImages() {
        Bitmap original = bitmap(720, 960);
        List<Bitmap> copies = new ArrayList<>();
        for (int i = 0; i < 5; i++) {
            View handle = timeline.addAssistantMessage("큰 화면 " + i);
            timeline.attachScreenshot(handle, original, "https://shop.example.com", null);
            copies.add(imageBitmap(descendants(handle, ImageView.class).get(0)));
        }
        long bytes = 0;
        for (ImageView image : descendants(timeline.getView(), ImageView.class)) {
            Bitmap copy = imageBitmap(image);
            bytes += (long) copy.getRowBytes() * copy.getHeight();
        }
        assertTrue(bytes <= 12L * 1024 * 1024);
        assertEquals(4, descendants(timeline.getView(), ImageView.class).size());
        assertTrue(copies.get(0).isRecycled());
        assertFalse(original.isRecycled());
    }

    @Test public void lastSixtyMessagesBoundHistoryAndRejectDelayedAttachmentToAnEvictedHandle() {
        View old = timeline.addAssistantMessage("오래된 결과");
        Bitmap original = bitmap(40, 40);
        timeline.attachScreenshot(old, original, "https://shop.example.com", null);
        Bitmap owned = imageBitmap(descendants(old, ImageView.class).get(0));
        for (int i = 0; i < 60; i++) timeline.addUser("요청 " + i);
        assertEquals(60, messageContainer().getChildCount());
        assertTrue(owned.isRecycled());
        assertFalse(allText(timeline.getView()).contains("오래된 결과"));
        timeline.attachScreenshot(old, original, "https://shop.example.com", null);
        assertEquals(0, descendants(timeline.getView(), ImageView.class).size());
        assertEquals(0, descendants(old, ImageView.class).size());
        assertFalse(original.isRecycled());
    }

    @Test public void foreignOrDetachedMessageHandlesCannotReceiveAsyncScreenshots() {
        BrowserChatTimeline other = new BrowserChatTimeline(RuntimeEnvironment.getApplication());
        try {
            View foreign = other.addAssistantMessage("다른 대화");
            Bitmap original = bitmap(40, 40);
            timeline.attachScreenshot(foreign, original, "https://shop.example.com", null);
            assertEquals(0, descendants(foreign, ImageView.class).size());
            View own = timeline.addAssistantMessage("우리 대화");
            ((ViewGroup) own.getParent()).removeView(own);
            timeline.attachScreenshot(own, original, "https://shop.example.com", null);
            assertEquals(0, descendants(own, ImageView.class).size());
            assertFalse(original.isRecycled());
        } finally { other.release(); }
    }

    @Test public void interactionRemainsBelowMessagesAndOnlyActivePanelIsRemoved() {
        LinearLayout oldParent = new LinearLayout(RuntimeEnvironment.getApplication());
        TextView first = new TextView(RuntimeEnvironment.getApplication());
        first.setText("승인이 필요해요");
        oldParent.addView(first);
        timeline.addInteraction(first);
        timeline.addAssistant("질문 설명");
        LinearLayout content = (LinearLayout) timeline.getView().getChildAt(0);
        assertSame(first.getParent(), content.getChildAt(1));
        assertEquals(0, oldParent.getChildCount());
        timeline.addInteraction(first);
        assertEquals(1, ((ViewGroup) first.getParent()).getChildCount());
        TextView second = new TextView(RuntimeEnvironment.getApplication());
        timeline.addInteraction(second);
        assertNull(first.getParent());
        oldParent.addView(first);
        timeline.removeInteraction(first);
        assertSame(oldParent, first.getParent());
        assertSame(content.getChildAt(1), second.getParent());
        timeline.removeInteraction(second);
        assertNull(second.getParent());
        assertTrue(allText(timeline.getView()).contains("질문 설명"));
    }

    @Test public void clearAndReleaseRecycleCopiesDetachInteractionsAndDisableOldCallbacks() {
        AtomicInteger opened = new AtomicInteger();
        Bitmap original = bitmap(40, 40);
        View message = timeline.addAssistantMessage("결과");
        timeline.attachScreenshot(message, original, "https://shop.example.com", opened::incrementAndGet);
        ImageView image = descendants(message, ImageView.class).get(0);
        Button button = descendants(message, Button.class).get(0);
        Bitmap copy = imageBitmap(image);
        TextView interaction = new TextView(RuntimeEnvironment.getApplication());
        timeline.addInteraction(interaction);
        timeline.clear();
        assertTrue(copy.isRecycled());
        assertNull(image.getDrawable());
        assertNull(interaction.getParent());
        assertEquals(0, messageContainer().getChildCount());
        image.performClick(); button.performClick();
        assertEquals(0, opened.get());
        assertFalse(original.isRecycled());
        timeline.addAssistant("새 대화");
        timeline.release(); timeline.release();
        timeline.addUser("무시될 메시지");
        assertNull(timeline.addAssistantMessage("무시될 결과"));
        timeline.attachScreenshot(message, original, "https://shop.example.com", opened::incrementAndGet);
        timeline.addInteraction(interaction);
        assertEquals(0, messageContainer().getChildCount());
        assertNull(interaction.getParent());
        assertFalse(original.isRecycled());
    }

    @Test public void invalidOrRecycledScreenshotDoesNotRemoveTheAssistantText() {
        View message = timeline.addAssistantMessage("텍스트 결과는 유지해요");
        Bitmap recycled = bitmap(20, 20);
        recycled.recycle();
        timeline.attachScreenshot(message, recycled, "javascript:alert(1)", null);
        timeline.attachScreenshot(message, null, "", null);
        assertEquals(0, descendants(message, ImageView.class).size());
        assertTrue(allText(message).contains("텍스트 결과는 유지해요"));
        timeline.attachScreenshot(message, bitmap(20, 20), "javascript:PRIVATE_SCRIPT", null);
        assertTrue(allText(message).contains(" · 사이트"));
        assertFalse(allText(message).contains("PRIVATE_SCRIPT"));
    }

    private LinearLayout messageContainer() { return (LinearLayout) ((LinearLayout) timeline.getView().getChildAt(0)).getChildAt(0); }
    private Bitmap bitmap(int width, int height) { Bitmap bitmap = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888); originals.add(bitmap); return bitmap; }
    private static Bitmap imageBitmap(ImageView view) { return ((BitmapDrawable) view.getDrawable()).getBitmap(); }
    private static String allText(View root) { StringBuilder text = new StringBuilder(); for (TextView view : descendants(root, TextView.class)) text.append(view.getText()).append('\n'); return text.toString(); }
    private static <T extends View> List<T> descendants(View root, Class<T> type) {
        List<T> found = new ArrayList<>();
        if (type.isInstance(root)) found.add(type.cast(root));
        if (root instanceof ViewGroup) for (int i = 0; i < ((ViewGroup) root).getChildCount(); i++) found.addAll(descendants(((ViewGroup) root).getChildAt(i), type));
        return found;
    }
}
