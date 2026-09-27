package com.vitlane.browser;

import android.animation.ValueAnimator;
import android.content.Context;
import android.graphics.Bitmap;
import android.graphics.Canvas;
import android.graphics.Color;
import android.graphics.Paint;
import android.graphics.Rect;
import android.graphics.Typeface;
import android.graphics.drawable.GradientDrawable;
import android.view.Gravity;
import android.view.MotionEvent;
import android.view.View;
import android.view.ViewGroup;
import android.view.animation.DecelerateInterpolator;
import android.view.animation.OvershootInterpolator;
import android.widget.Button;
import android.widget.ImageView;
import android.widget.HorizontalScrollView;
import android.widget.LinearLayout;
import android.widget.ScrollView;
import android.widget.TextView;

import org.json.JSONArray;
import org.json.JSONObject;

import java.net.URI;
import java.text.SimpleDateFormat;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Date;
import java.util.IdentityHashMap;
import java.util.List;
import java.util.Locale;

/**
 * UI-thread-only, in-memory chat history. This class performs no capture, navigation or persistence.
 * Screenshot arguments remain caller-owned: a bounded independent copy is made synchronously, so
 * callers may recycle their original immediately after addAssistant/attachScreenshot returns.
 * Native callers must check page privacy and freshness before supplying any screenshot.
 */
public final class BrowserChatTimeline {
    private static final int MAX_MESSAGES = 60;
    private static final int MAX_SCREENSHOTS = 6;
    private static final int MAX_SCREENSHOT_WIDTH = 720;
    private static final int MAX_SCREENSHOT_HEIGHT = 960;
    private static final long MAX_SCREENSHOT_BYTES = 12L * 1024 * 1024;
    private static final int MAX_CATALOGS = 12;
    private static final int MAX_CATALOG_IMAGES = 16;
    private static final long MAX_CATALOG_IMAGE_BYTES = 8L * 1024 * 1024;
    private static final int MAX_MESSAGE_CHARACTERS = 24000;
    private static final int INK = 0xff171821;
    private static final int MUTED = 0xff6f7280;
    private static final int SURFACE = 0xfff6f7fb;
    private static final int ACCENT = 0xffeeedff;
    private static final int BRAND = 0xff5856d6;
    private static final int BORDER = 0xffe7e7ef;

    private final Context context;
    private final ScrollView scroll;
    private final LinearLayout content;
    private final LinearLayout messages;
    private final LinearLayout interactions;
    private final ArrayDeque<Message> history = new ArrayDeque<>();
    private final ArrayDeque<Message> screenshots = new ArrayDeque<>();
    private final ArrayDeque<CatalogGroup> catalogs = new ArrayDeque<>();
    private final ArrayDeque<CatalogPreview> catalogImages = new ArrayDeque<>();
    private final IdentityHashMap<View, Message> handles = new IdentityHashMap<>();
    private final IdentityHashMap<View, CatalogGroup> catalogHandles = new IdentityHashMap<>();
    private final IdentityHashMap<View, CatalogPreview> catalogPreviews = new IdentityHashMap<>();
    private final Runnable scrollBottom;
    private View activeInteraction;
    private View suggestions;
    private long screenshotBytes;
    private long catalogImageBytes;
    private boolean released;

    private static final class Message {
        final LinearLayout row;
        final LinearLayout bubble;
        final boolean assistant;
        ImageView image;
        TextView caption;
        Button openButton;
        Bitmap bitmap;
        long bytes;
        String captureLabel;

        Message(LinearLayout row, LinearLayout bubble, boolean assistant) {
            this.row = row;
            this.bubble = bubble;
            this.assistant = assistant;
        }
    }

    private static final class CatalogGroup {
        final View scroller;
        final List<View> cards;
        CatalogGroup(View scroller, List<View> cards) { this.scroller = scroller; this.cards = cards; }
    }

    private static final class CatalogPreview {
        final View card;
        final ImageView image;
        final Bitmap bitmap;
        final long bytes;
        CatalogPreview(View card, ImageView image, Bitmap bitmap) {
            this.card = card; this.image = image; this.bitmap = bitmap;
            this.bytes = (long) bitmap.getRowBytes() * bitmap.getHeight();
        }
    }

    public BrowserChatTimeline(Context context) {
        this.context = context;
        scroll = new ScrollView(context);
        scroll.setBackgroundColor(Color.WHITE);
        scroll.setFillViewport(true);
        scroll.setClipToPadding(false);
        scroll.setContentDescription("AI와 대화");
        content = column();
        content.setPadding(dp(14), dp(18), dp(14), dp(24));
        messages = column();
        interactions = column();
        content.addView(messages, matchWrap());
        content.addView(interactions, matchWrap());
        scroll.addView(content, new ScrollView.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        // scrollTo does not move focus away from the user's active text input.
        scrollBottom = () -> { if (!released) scroll.scrollTo(0, content.getHeight()); };
    }

    public ScrollView getView() { return scroll; }

    public void addUser(String text) { append(text, false); }

    public void addAssistant(String text) { addAssistantMessage(text); }

    /** Returns an opaque, owned message handle for an optional later screenshot attachment. */
    public View addAssistantMessage(String text) {
        Message message = append(text, true);
        return message == null ? null : message.bubble;
    }

    /** Compact starter actions keep common tasks discoverable without turning the chat into a menu. */
    public List<View> addSuggestions(String... options) {
        List<View> result = new ArrayList<>();
        if (released || options == null || options.length == 0) return result;
        removeSuggestions();
        LinearLayout block = column();
        block.setContentDescription("빠르게 시작하기");
        TextView heading = text("무엇을 해볼까요?", 12, MUTED);
        heading.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        heading.setPadding(dp(2), 0, 0, dp(9));
        block.addView(heading, matchWrap());
        HorizontalScrollView scroller = new HorizontalScrollView(context);
        scroller.setHorizontalScrollBarEnabled(false);
        scroller.setClipToPadding(false);
        LinearLayout actions = new LinearLayout(context);
        actions.setOrientation(LinearLayout.HORIZONTAL);
        for (int i = 0; i < Math.min(4, options.length); i++) {
            String option = options[i] == null ? "" : options[i].trim();
            if (option.isEmpty()) continue;
            Button action = new Button(context);
            action.setText(option);
            action.setAllCaps(false);
            action.setTextSize(13);
            action.setTextColor(INK);
            action.setMinWidth(0);
            action.setMinimumWidth(0);
            action.setPadding(dp(16), 0, dp(16), 0);
            action.setBackground(roundBackground(Color.WHITE, 20, BORDER));
            action.setContentDescription(option);
            pressable(action);
            LinearLayout.LayoutParams actionParams = new LinearLayout.LayoutParams(
                    ViewGroup.LayoutParams.WRAP_CONTENT, dp(42));
            actionParams.setMarginEnd(dp(8));
            actions.addView(action, actionParams);
            result.add(action);
        }
        if (result.isEmpty()) return result;
        scroller.addView(actions, new HorizontalScrollView.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        block.addView(scroller, matchWrap());
        LinearLayout.LayoutParams blockParams = matchWrap();
        blockParams.bottomMargin = dp(18);
        messages.addView(block, blockParams);
        suggestions = block;
        animateIn(block, 70, dp(8));
        for (int i = 0; i < result.size(); i++) animateIn(result.get(i), 45L * i, dp(8));
        scrollToBottom();
        return result;
    }

    public void removeSuggestions() {
        if (suggestions != null && suggestions.getParent() == messages) messages.removeView(suggestions);
        suggestions = null;
    }

    public void addAssistant(String text, Bitmap screenshot, String sourceUrl, Runnable openBrowser) {
        attachScreenshot(addAssistantMessage(text), screenshot, sourceUrl, openBrowser);
    }

    /** Reusable horizontally scrolling option cards for travel, shopping and booking results. */
    public List<View> addCatalog(JSONArray catalog) {
        List<View> result = new ArrayList<>();
        if (released || catalog == null || catalog.length() == 0) return result;
        HorizontalScrollView scroller = new HorizontalScrollView(context);
        scroller.setHorizontalScrollBarEnabled(false);
        scroller.setClipToPadding(false);
        scroller.setPadding(0, 0, dp(12), 0);
        LinearLayout cards = new LinearLayout(context);
        cards.setOrientation(LinearLayout.HORIZONTAL);
        for (int i = 0; i < Math.min(8, catalog.length()); i++) {
            JSONObject item = catalog.optJSONObject(i);
            if (item == null || item.optString("title").trim().isEmpty()) continue;
            LinearLayout card = column();
            card.setPadding(dp(15), dp(15), dp(15), dp(15));
            card.setBackground(roundBackground(Color.WHITE, 20, BORDER));
            card.setElevation(dp(2));
            card.setClipToOutline(false);
            TextView title = text(item.optString("title"), 15, INK);
            title.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
            card.addView(title, matchWrap());
            if (!item.optString("subtitle").isEmpty()) {
                TextView subtitle = text(item.optString("subtitle"), 13, MUTED);
                subtitle.setPadding(0, dp(6), 0, 0);
                card.addView(subtitle, matchWrap());
            }
            if (!item.optString("price").isEmpty()) {
                TextView price = text(item.optString("price"), 15, INK);
                price.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
                price.setPadding(0, dp(9), 0, 0);
                card.addView(price, matchWrap());
            }
            JSONArray badges = item.optJSONArray("badges");
            if (badges != null && badges.length() > 0) {
                StringBuilder value = new StringBuilder();
                for (int j = 0; j < Math.min(4, badges.length()); j++) {
                    String badge = badges.optString(j).trim();
                    if (!badge.isEmpty()) value.append(value.length() == 0 ? "" : "  ·  ").append(badge);
                }
                if (value.length() > 0) {
                    TextView badge = text(value.toString(), 11, BRAND);
                    badge.setPadding(dp(10), dp(6), dp(10), dp(6));
                    badge.setBackground(background(ACCENT));
                    LinearLayout.LayoutParams badgeParams = matchWrap();
                    badgeParams.topMargin = dp(10);
                    card.addView(badge, badgeParams);
                }
            }
            pressable(card);
            LinearLayout.LayoutParams cardParams = new LinearLayout.LayoutParams(dp(260), ViewGroup.LayoutParams.WRAP_CONTENT);
            cardParams.setMarginEnd(dp(10));
            cards.addView(card, cardParams);
            result.add(card);
        }
        if (cards.getChildCount() == 0) return result;
        scroller.addView(cards, new HorizontalScrollView.LayoutParams(ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        LinearLayout.LayoutParams params = matchWrap();
        params.bottomMargin = dp(14);
        messages.addView(scroller, params);
        CatalogGroup group = new CatalogGroup(scroller, new ArrayList<>(result));
        catalogs.addLast(group);
        for (View card : result) catalogHandles.put(card, group);
        while (catalogs.size() > MAX_CATALOGS) removeCatalog(catalogs.peekFirst());
        animateIn(scroller, 0, dp(10));
        for (int i = 0; i < result.size(); i++) animateIn(result.get(i), 45L * i, dp(8));
        scrollToBottom();
        return result;
    }

    /** Adds a bounded local copy of a page-provided product image to its catalog card. */
    public void attachCatalogImage(View handle, Bitmap source, String alt) {
        if (released || source == null || source.isRecycled() || !catalogHandles.containsKey(handle)
                || !(handle instanceof LinearLayout) || handle.getParent() == null) return;
        int width = source.getWidth(), height = source.getHeight();
        if (width < 1 || height < 1) return;
        double scale = Math.min(1d, Math.min((double) dp(232) / width, (double) dp(180) / height));
        int targetWidth = Math.max(1, (int) Math.round(width * scale));
        int targetHeight = Math.max(1, (int) Math.round(height * scale));
        long expected = (long) targetWidth * targetHeight * 4;
        removeCatalogPreview(handle);
        while (!catalogImages.isEmpty() && (catalogImages.size() >= MAX_CATALOG_IMAGES
                || catalogImageBytes + expected > MAX_CATALOG_IMAGE_BYTES)) removeCatalogPreview(catalogImages.peekFirst().card);
        Bitmap copy = thumbnail(source, targetWidth, targetHeight);
        if (copy == null) return;
        ImageView image = new ImageView(context);
        image.setImageBitmap(copy);
        image.setAdjustViewBounds(true);
        image.setScaleType(ImageView.ScaleType.CENTER_CROP);
        image.setContentDescription(alt == null || alt.trim().isEmpty() ? "상품 이미지" : alt.trim());
        image.setBackground(background(SURFACE));
        image.setClipToOutline(true);
        LinearLayout.LayoutParams params = matchWrap();
        params.bottomMargin = dp(10);
        ((LinearLayout) handle).addView(image, 0, params);
        CatalogPreview preview = new CatalogPreview(handle, image, copy);
        catalogPreviews.put(handle, preview);
        catalogImages.addLast(preview);
        catalogImageBytes += preview.bytes;
        animateIn(image, 0, dp(4));
        scrollToBottom();
    }

    /**
     * Attaches only to a still-present assistant message. Obsolete/foreign/released handles are
     * ignored, and the caller's bitmap is never recycled or retained, including on failure.
     * Clicking the historical image opens the CURRENT browser via the supplied callback only.
     */
    public void attachScreenshot(View handle, Bitmap screenshot, String sourceUrl, Runnable openBrowser) {
        attachPreview(handle, screenshot, sourceUrl, null, openBrowser, false);
    }

    /** Attaches decoded bytes from the page's actual image URL, without exposing that URL to the model. */
    public void attachSourceImage(View handle, Bitmap image, String sourceUrl, String alt) {
        attachPreview(handle, image, sourceUrl, alt, null, true);
    }

    private void attachPreview(View handle, Bitmap screenshot, String sourceUrl, String alt,
            Runnable openBrowser, boolean sourceImage) {
        if (released || screenshot == null || screenshot.isRecycled()) return;
        Message message = handles.get(handle);
        if (message == null || !message.assistant || message.row.getParent() != messages
                || message.bubble.getParent() != message.row) return;
        boolean follow = isAtBottom();
        int width = screenshot.getWidth(), height = screenshot.getHeight();
        if (width <= 0 || height <= 0) return;
        double scale = Math.min(1d, Math.min((double) MAX_SCREENSHOT_WIDTH / width, (double) MAX_SCREENSHOT_HEIGHT / height));
        int targetWidth = Math.max(1, (int) Math.round(width * scale));
        int targetHeight = Math.max(1, (int) Math.round(height * scale));
        long expectedBytes = (long) targetWidth * targetHeight * 4;
        removePreview(message, false);
        // Free old copies before allocating the next one, not just after attaching it.
        while (!screenshots.isEmpty() && (screenshots.size() >= MAX_SCREENSHOTS
                || screenshotBytes + expectedBytes > MAX_SCREENSHOT_BYTES)) removePreview(screenshots.peekFirst(), true);
        Bitmap copy = thumbnail(screenshot, targetWidth, targetHeight);
        if (copy == null) return;
        message.bitmap = copy;
        message.bytes = (long) copy.getRowBytes() * copy.getHeight();
        screenshotBytes += message.bytes;
        screenshots.addLast(message);
        message.captureLabel = (sourceImage ? "페이지 원본 이미지" : "캡처한 화면 · "
                + new SimpleDateFormat("MM/dd HH:mm", Locale.KOREA).format(new Date())) + " · " + sourceOrigin(sourceUrl);

        message.caption = text(message.captureLabel + "\n" + (sourceImage
                ? "사이트가 제공한 원본 이미지를 직접 불러왔어요."
                : "현재 페이지는 이 화면과 다를 수 있어요."), 11, MUTED);
        message.caption.setPadding(0, dp(10), 0, dp(6));
        message.bubble.addView(message.caption, matchWrap());
        message.image = new ImageView(context);
        message.image.setImageBitmap(copy);
        message.image.setAdjustViewBounds(true);
        message.image.setScaleType(ImageView.ScaleType.FIT_CENTER);
        message.image.setMaxHeight(dp(360));
        message.image.setBackground(background(SURFACE));
        message.image.setClipToOutline(true);
        String description = alt == null || alt.trim().isEmpty() ? message.captureLabel : alt.trim();
        message.image.setContentDescription(description + (openBrowser == null ? "" : ". 누르면 현재 브라우저를 엽니다."));
        message.bubble.addView(message.image, matchWrap());
        if (openBrowser != null) {
            message.image.setOnClickListener(view -> { if (valid(message)) openBrowser.run(); });
            message.openButton = new Button(context);
            message.openButton.setText("현재 브라우저 열기");
            message.openButton.setAllCaps(false);
            message.openButton.setTextSize(13);
            message.openButton.setTextColor(INK);
            message.openButton.setBackground(background(ACCENT));
            pressable(message.openButton);
            message.openButton.setOnClickListener(view -> { if (valid(message)) openBrowser.run(); });
            LinearLayout.LayoutParams params = matchWrap();
            params.topMargin = dp(8);
            message.bubble.addView(message.openButton, params);
        }
        while (screenshots.size() > MAX_SCREENSHOTS || screenshotBytes > MAX_SCREENSHOT_BYTES) {
            removePreview(screenshots.peekFirst(), true);
        }
        if (follow) scrollToBottom();
    }

    /** One active native question/action panel remains below all conversation messages. */
    public void addInteraction(View view) {
        if (released || view == null || view == scroll || view == content || view == messages || view == interactions) return;
        // Do not reparent an ancestor of this timeline into its own descendant.
        for (View ancestor = scroll; ancestor != null;) {
            if (ancestor == view) return;
            ancestor = ancestor.getParent() instanceof View ? (View) ancestor.getParent() : null;
        }
        if (view == activeInteraction && view.getParent() == interactions) { scrollToBottom(); return; }
        if (activeInteraction != null) removeInteraction(activeInteraction);
        if (view.getParent() instanceof ViewGroup) ((ViewGroup) view.getParent()).removeView(view);
        activeInteraction = view;
        interactions.addView(view, matchWrap());
        animateIn(view, 0, dp(12));
        scrollToBottom();
    }

    /** Removes only the active panel, never an unrelated view or an old panel's new parent. */
    public void removeInteraction(View view) {
        if (view == null || activeInteraction != view) return;
        if (view.getParent() == interactions) interactions.removeView(view);
        activeInteraction = null;
    }

    public void scrollToBottom() {
        if (released) return;
        scroll.removeCallbacks(scrollBottom);
        scroll.post(scrollBottom);
    }

    /** Clears this RAM-only conversation, its owned screenshots, and the active native panel. */
    public void clear() {
        scroll.removeCallbacks(scrollBottom);
        while (!history.isEmpty()) removeMessage(history.removeFirst());
        handles.clear();
        screenshots.clear();
        screenshotBytes = 0;
        while (!catalogs.isEmpty()) removeCatalog(catalogs.peekFirst());
        catalogHandles.clear();
        catalogPreviews.clear();
        catalogImages.clear();
        catalogImageBytes = 0;
        if (activeInteraction != null) removeInteraction(activeInteraction);
        removeSuggestions();
        scroll.scrollTo(0, 0);
    }

    /** Idempotent lifecycle cleanup. Later calls to add/attach are ignored. */
    public void release() {
        if (released) return;
        released = true;
        clear();
    }

    private Message append(String value, boolean assistant) {
        if (released) return null;
        if (!assistant) removeSuggestions();
        boolean follow = !assistant || isAtBottom();
        String body = value == null ? "" : value;
        if (body.length() > MAX_MESSAGE_CHARACTERS) body = body.substring(0, MAX_MESSAGE_CHARACTERS) + "…";
        LinearLayout row = column();
        row.setGravity(assistant ? Gravity.START : Gravity.END);
        LinearLayout bubble = column();
        bubble.setPadding(dp(14), dp(12), dp(14), dp(14));
        bubble.setBackground(assistant
                ? roundBackground(SURFACE, 18, BORDER)
                : background(BRAND));
        LinearLayout identity = new LinearLayout(context);
        identity.setOrientation(LinearLayout.HORIZONTAL);
        identity.setGravity(Gravity.CENTER_VERTICAL);
        View dot = new View(context);
        dot.setBackground(circle(assistant ? BRAND : 0x66ffffff));
        LinearLayout.LayoutParams dotParams = new LinearLayout.LayoutParams(dp(7), dp(7));
        dotParams.setMarginEnd(dp(7));
        identity.addView(dot, dotParams);
        TextView speaker = text(assistant ? "Vitlane AI" : "나", 11, assistant ? MUTED : 0xffe9e9ff);
        speaker.setTypeface(Typeface.DEFAULT, Typeface.BOLD);
        identity.addView(speaker, new LinearLayout.LayoutParams(
                ViewGroup.LayoutParams.WRAP_CONTENT, ViewGroup.LayoutParams.WRAP_CONTENT));
        identity.setPadding(0, 0, 0, dp(6));
        bubble.addView(identity, matchWrap());
        TextView messageText = text(body, 15, assistant ? INK : Color.WHITE);
        messageText.setTextIsSelectable(true);
        messageText.setAutoLinkMask(0);
        messageText.setLinksClickable(false);
        messageText.setLineSpacing(dp(3), 1f);
        bubble.addView(messageText, matchWrap());
        LinearLayout.LayoutParams bubbleParams = matchWrap();
        if (assistant) bubbleParams.setMarginEnd(dp(24)); else bubbleParams.setMarginStart(dp(48));
        row.addView(bubble, bubbleParams);
        LinearLayout.LayoutParams rowParams = matchWrap();
        rowParams.bottomMargin = dp(12);
        messages.addView(row, rowParams);
        animateIn(row, 0, dp(9));
        Message message = new Message(row, bubble, assistant);
        history.addLast(message);
        handles.put(bubble, message);
        while (history.size() > MAX_MESSAGES) removeMessage(history.removeFirst());
        if (follow) scrollToBottom();
        return message;
    }

    private boolean valid(Message message) {
        return !released && handles.get(message.bubble) == message && message.row.getParent() == messages
                && message.bubble.getParent() == message.row;
    }

    private void removeMessage(Message message) {
        handles.remove(message.bubble);
        removePreview(message, false);
        messages.removeView(message.row);
    }

    private void removePreview(Message message, boolean preserveCaption) {
        if (message == null) return;
        screenshots.remove(message);
        if (message.image != null) {
            message.image.setOnClickListener(null);
            message.image.setImageDrawable(null);
            message.bubble.removeView(message.image);
            message.image = null;
        }
        if (message.bitmap != null) {
            screenshotBytes -= message.bytes;
            if (!message.bitmap.isRecycled()) message.bitmap.recycle();
            message.bitmap = null;
            message.bytes = 0;
        }
        if (preserveCaption && message.caption != null) {
            message.caption.setText(message.captureLabel + "\n이전 미리보기는 메모리에서 삭제됐어요. 현재 브라우저를 열어 확인할 수 있어요.");
        } else {
            if (message.caption != null) message.bubble.removeView(message.caption);
            if (message.openButton != null) {
                message.openButton.setOnClickListener(null);
                message.bubble.removeView(message.openButton);
            }
            message.caption = null;
            message.openButton = null;
            message.captureLabel = null;
        }
    }

    private void removeCatalog(CatalogGroup group) {
        if (group == null) return;
        catalogs.remove(group);
        for (View card : group.cards) {
            removeCatalogPreview(card);
            catalogHandles.remove(card);
        }
        messages.removeView(group.scroller);
    }

    private void removeCatalogPreview(View card) {
        CatalogPreview preview = catalogPreviews.remove(card);
        if (preview == null) return;
        catalogImages.remove(preview);
        preview.image.setImageDrawable(null);
        if (preview.image.getParent() instanceof ViewGroup) ((ViewGroup) preview.image.getParent()).removeView(preview.image);
        if (!preview.bitmap.isRecycled()) preview.bitmap.recycle();
        catalogImageBytes = Math.max(0, catalogImageBytes - preview.bytes);
    }

    private Bitmap thumbnail(Bitmap original, int width, int height) {
        Bitmap copy = null;
        try {
            copy = Bitmap.createBitmap(width, height, Bitmap.Config.ARGB_8888);
            new Canvas(copy).drawBitmap(original, null, new Rect(0, 0, copy.getWidth(), copy.getHeight()),
                    new Paint(Paint.ANTI_ALIAS_FLAG | Paint.FILTER_BITMAP_FLAG));
            return copy;
        } catch (RuntimeException | OutOfMemoryError ignored) {
            if (copy != null && !copy.isRecycled()) copy.recycle();
            return null; // Keep the text result available if a preview cannot be copied.
        }
    }

    private String sourceOrigin(String value) {
        if (value == null || value.length() > 4096) return "사이트";
        try {
            URI uri = new URI(value);
            if (!("https".equalsIgnoreCase(uri.getScheme()) || "http".equalsIgnoreCase(uri.getScheme()))
                    || uri.getHost() == null) return "사이트";
            String host = uri.getHost();
            if (host.length() > 160) host = host.substring(0, 160) + "…";
            return host + (uri.getPort() < 0 ? "" : ":" + uri.getPort());
        } catch (Exception ignored) { return "사이트"; }
    }

    private boolean isAtBottom() {
        return content.getHeight() - (scroll.getScrollY() + scroll.getHeight()) <= dp(80);
    }

    private boolean motionEnabled() {
        return android.os.Build.VERSION.SDK_INT < 26 || ValueAnimator.areAnimatorsEnabled();
    }

    private void animateIn(View view, long delay, float distance) {
        if (!motionEnabled() || !scroll.isAttachedToWindow()) {
            view.setAlpha(1f);
            view.setTranslationY(0f);
            return;
        }
        view.animate().cancel();
        view.setAlpha(0f);
        view.setTranslationY(distance);
        view.setScaleX(0.985f);
        view.setScaleY(0.985f);
        view.animate().alpha(1f).translationY(0f).scaleX(1f).scaleY(1f)
                .setStartDelay(delay).setDuration(240)
                .setInterpolator(new DecelerateInterpolator(1.6f)).start();
    }

    private void pressable(View view) {
        view.setOnTouchListener((pressed, event) -> {
            if (!pressed.isEnabled() || !motionEnabled()) return false;
            int action = event.getActionMasked();
            if (action == MotionEvent.ACTION_DOWN) {
                pressed.animate().cancel();
                pressed.animate().scaleX(0.97f).scaleY(0.97f).setDuration(90).start();
            } else if (action == MotionEvent.ACTION_UP || action == MotionEvent.ACTION_CANCEL) {
                pressed.animate().cancel();
                pressed.animate().scaleX(1f).scaleY(1f).setDuration(220)
                        .setInterpolator(new OvershootInterpolator(1.2f)).start();
            }
            return false;
        });
    }

    private int dp(int value) { return Math.round(value * context.getResources().getDisplayMetrics().density); }

    private GradientDrawable roundBackground(int color, int radius, int stroke) {
        GradientDrawable shape = new GradientDrawable();
        shape.setColor(color);
        shape.setCornerRadius(dp(radius));
        shape.setStroke(dp(1), stroke);
        return shape;
    }
    private GradientDrawable circle(int color) {
        GradientDrawable drawable = new GradientDrawable();
        drawable.setShape(GradientDrawable.OVAL);
        drawable.setColor(color);
        return drawable;
    }
    private LinearLayout column() { LinearLayout view = new LinearLayout(context); view.setOrientation(LinearLayout.VERTICAL); return view; }
    private LinearLayout.LayoutParams matchWrap() { return new LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT); }
    private TextView text(String value, int size, int color) { TextView view = new TextView(context); view.setText(value); view.setTextSize(size); view.setTextColor(color); return view; }
    private GradientDrawable background(int color) { GradientDrawable drawable = new GradientDrawable(); drawable.setColor(color); drawable.setCornerRadius(dp(14)); return drawable; }
}
