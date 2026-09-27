package com.vitlane.browser;

/** Classifies one native touch sequence without depending on Android event classes. */
public final class BrowserTouchPolicy {
    public enum Result { NONE, TAP, SCROLL, CANCELLED }

    private final double touchSlopSquared;
    private boolean active;
    private boolean gesture;
    private boolean invalid;
    private float startX;
    private float startY;
    private long downTimeMillis;

    public BrowserTouchPolicy(float touchSlop) {
        if (!finite(touchSlop) || touchSlop < 0) {
            throw new IllegalArgumentException("touchSlop must be finite and nonnegative");
        }
        touchSlopSquared = (double) touchSlop * touchSlop;
    }

    /** Starts a new sequence; the caller should hold agent dispatch until it ends. */
    public void down(float x, float y, long timeMillis) {
        reset();
        active = true;
        startX = x;
        startY = y;
        downTimeMillis = timeMillis;
        invalid = !finite(x) || !finite(y);
    }

    /** Also call for pointer-down events so stationary multi-finger gestures count. */
    public void move(float x, float y, int pointerCount) {
        if (!active) return;
        if (!finite(x) || !finite(y) || pointerCount < 1) {
            invalid = true;
            return;
        }
        if (pointerCount > 1 || outsideSlop(x, y)) gesture = true;
    }

    public Result up(float x, float y, long timeMillis) {
        if (!active) return Result.NONE;
        // Movement can occur between the last MOVE and UP. Once moved, returning to
        // the origin cannot turn a drag/pinch into a tap. Duration alone never turns
        // a stationary long press into a scroll: it remains a manual intervention.
        move(x, y, 1);
        Result result = invalid || timeMillis < downTimeMillis
                ? Result.CANCELLED : gesture ? Result.SCROLL : Result.TAP;
        reset();
        return result;
    }

    /** Cancellation is not proof of a completed scroll or of a user tap. */
    public Result cancel() {
        Result result = active ? Result.CANCELLED : Result.NONE;
        reset();
        return result;
    }

    public boolean isActive() { return active; }

    private boolean outsideSlop(float x, float y) {
        double dx = (double) x - startX;
        double dy = (double) y - startY;
        return dx * dx + dy * dy > touchSlopSquared;
    }

    private void reset() {
        active = false;
        gesture = false;
        invalid = false;
        startX = 0;
        startY = 0;
        downTimeMillis = 0;
    }

    private static boolean finite(float value) {
        return !Float.isNaN(value) && !Float.isInfinite(value);
    }
}
