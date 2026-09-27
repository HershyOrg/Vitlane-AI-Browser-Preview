package com.vitlane.browser;

import org.junit.Test;
import static org.junit.Assert.*;

public class BrowserTouchPolicyTest {
    private final BrowserTouchPolicy policy = new BrowserTouchPolicy(10);

    @Test public void stationaryTapEndsSequence() {
        policy.down(100, 200, 1000);
        assertTrue(policy.isActive());
        assertEquals(BrowserTouchPolicy.Result.TAP, policy.up(100, 200, 1080));
        assertFalse(policy.isActive());
        assertEquals(BrowserTouchPolicy.Result.NONE, policy.up(100, 200, 1100));
    }

    @Test public void smallFingerJitterAndSlopBoundaryRemainTap() {
        policy.down(100, 200, 1000);
        policy.move(102, 198, 1);
        policy.move(106, 208, 1); // Exactly ten pixels from the initial point.
        assertEquals(BrowserTouchPolicy.Result.TAP, policy.up(104, 204, 1150));
    }

    @Test public void diagonalMovementUsesDistanceRatherThanEachAxis() {
        policy.down(100, 200, 1000);
        policy.move(108, 208, 1);
        assertEquals(BrowserTouchPolicy.Result.SCROLL, policy.up(108, 208, 1150));
    }

    @Test public void dragRemainsGestureAfterReturningToOrigin() {
        policy.down(100, 200, 1000);
        policy.move(100, 240, 1);
        policy.move(100, 200, 1);
        assertEquals(BrowserTouchPolicy.Result.SCROLL, policy.up(100, 200, 1300));
    }

    @Test public void finalUpMovementCountsEvenWithoutMoveEvent() {
        policy.down(100, 200, 1000);
        assertEquals(BrowserTouchPolicy.Result.SCROLL, policy.up(100, 220, 1100));
    }

    @Test public void stationaryPinchStaysGestureAfterSecondFingerLifts() {
        policy.down(100, 200, 1000);
        policy.move(100, 200, 2);
        policy.move(100, 200, 1);
        assertEquals(BrowserTouchPolicy.Result.SCROLL, policy.up(100, 200, 1300));
    }

    @Test public void longPressIsManualInterventionAndNotScroll() {
        policy.down(100, 200, 1000);
        policy.move(102, 200, 1);
        assertEquals(BrowserTouchPolicy.Result.TAP, policy.up(102, 200, 11000));
    }

    @Test public void longDragIsStillScroll() {
        policy.down(100, 200, 1000);
        policy.move(100, 220, 1);
        assertEquals(BrowserTouchPolicy.Result.SCROLL, policy.up(100, 220, 11000));
    }

    @Test public void cancelDoesNotMisreportGestureOrPolluteNextTap() {
        policy.down(100, 200, 1000);
        policy.move(100, 240, 1);
        assertEquals(BrowserTouchPolicy.Result.CANCELLED, policy.cancel());
        assertFalse(policy.isActive());
        policy.move(100, 300, 2);
        assertEquals(BrowserTouchPolicy.Result.NONE, policy.up(100, 300, 1200));
        assertEquals(BrowserTouchPolicy.Result.NONE, policy.cancel());
        policy.down(10, 20, 1300);
        assertEquals(BrowserTouchPolicy.Result.TAP, policy.up(10, 20, 1350));
    }

    @Test public void newDownReplacesAbandonedSequence() {
        policy.down(100, 200, 1000);
        policy.move(100, 240, 1);
        policy.down(50, 60, 1200);
        assertEquals(BrowserTouchPolicy.Result.TAP, policy.up(50, 60, 1300));
    }

    @Test public void finishedGestureDoesNotPolluteNextTap() {
        policy.down(100, 200, 1000);
        policy.move(100, 200, 2);
        assertEquals(BrowserTouchPolicy.Result.SCROLL, policy.up(100, 200, 1100));
        policy.down(100, 200, 1200);
        assertEquals(BrowserTouchPolicy.Result.TAP, policy.up(100, 200, 1250));
    }

    @Test public void malformedCoordinatesAndReversedTimeCancel() {
        policy.down(Float.NaN, 20, 1000);
        assertEquals(BrowserTouchPolicy.Result.CANCELLED, policy.up(10, 20, 1100));
        policy.down(10, 20, 1200);
        policy.move(Float.POSITIVE_INFINITY, 20, 1);
        assertEquals(BrowserTouchPolicy.Result.CANCELLED, policy.up(10, 20, 1300));
        policy.down(10, 20, 1400);
        assertEquals(BrowserTouchPolicy.Result.CANCELLED, policy.up(10, 20, 1399));
        assertFalse(policy.isActive());
    }

    @Test public void missingPointersCancelInsteadOfCountingAsTap() {
        policy.down(10, 20, 1000);
        policy.move(10, 20, 0);
        assertEquals(BrowserTouchPolicy.Result.CANCELLED, policy.up(10, 20, 1100));
    }

    @Test public void rejectsInvalidSlop() {
        for (float slop : new float[] {-1, Float.NaN, Float.POSITIVE_INFINITY}) {
            try {
                new BrowserTouchPolicy(slop);
                fail("Accepted invalid touch slop: " + slop);
            } catch (IllegalArgumentException expected) { }
        }
    }
}
