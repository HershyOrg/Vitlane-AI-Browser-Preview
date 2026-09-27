package com.vitlane.browser;

import static org.junit.Assert.*;
import java.lang.reflect.Field;
import org.junit.Test;
import org.junit.runner.RunWith;
import org.robolectric.Robolectric;
import org.robolectric.RobolectricTestRunner;
import org.robolectric.annotation.Config;

@RunWith(RobolectricTestRunner.class)
@Config(sdk = 28, manifest = Config.NONE)
public class BrowserSettingsLifecycleTest {
    private Object get(VitlaneBrowserActivity activity, String name) throws Exception {
        Field field = VitlaneBrowserActivity.class.getDeclaredField(name);
        field.setAccessible(true);
        return field.get(activity);
    }
    private void set(VitlaneBrowserActivity activity, String name, Object value) throws Exception {
        Field field = VitlaneBrowserActivity.class.getDeclaredField(name);
        field.setAccessible(true);
        field.set(activity, value);
    }

    @Test public void returningToBrowserRestoresLatestSettingsButDoesNotRestartAgent() throws Exception {
        VitlaneBrowserActivity activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        set(activity, "mApiKey", "sk-old-key");
        set(activity, "mSettingsHandler", (VitlaneBrowserActivity.SettingsHandler)
            (restore, key, model, steps, seconds, callback) -> {
                assertTrue(restore);
                assertEquals("", key);
                callback.onComplete("sk-saved-key", "gpt-custom", 4, 60, null);
            });
        activity.onStop();
        assertEquals("", get(activity, "mApiKey"));
        activity.onResume();
        assertEquals("sk-saved-key", get(activity, "mApiKey"));
        assertEquals("gpt-custom", get(activity, "mModel"));
        assertEquals(4, get(activity, "mMaxSteps"));
        assertEquals(60, get(activity, "mTimeoutSeconds"));
        assertEquals(false, get(activity, "mRunning"));
        activity.onDestroy();
    }

    @Test public void restoreCompletingAfterLeavingBrowserCannotReintroduceKey() throws Exception {
        VitlaneBrowserActivity activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        VitlaneBrowserActivity.SettingsCallback[] pending = new VitlaneBrowserActivity.SettingsCallback[1];
        set(activity, "mSettingsHandler", (VitlaneBrowserActivity.SettingsHandler)
            (restore, key, model, steps, seconds, callback) -> pending[0] = callback);
        activity.onStop();
        activity.onResume();
        assertEquals(true, get(activity, "mRestoringSettings"));
        activity.onPause();
        activity.onStop();
        pending[0].onComplete("sk-late-key", "gpt-custom", 4, 60, null);
        assertEquals("", get(activity, "mApiKey"));
        assertEquals(false, get(activity, "mRestoringSettings"));
        activity.onDestroy();
    }

    @Test public void failedRestoreAllowsSettingsToBeEdited() throws Exception {
        VitlaneBrowserActivity activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        set(activity, "mSettingsHandler", (VitlaneBrowserActivity.SettingsHandler)
            (restore, key, model, steps, seconds, callback) -> callback.onComplete("", "", 20, 300, "failed"));
        activity.onStop();
        activity.onResume();
        assertEquals("", get(activity, "mApiKey"));
        assertEquals(false, get(activity, "mRestoringSettings"));
        activity.onDestroy();
    }
    @Test public void earlierRestoreCannotOverwriteSettingsFromLaterResume() throws Exception {
        VitlaneBrowserActivity activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        java.util.List<VitlaneBrowserActivity.SettingsCallback> pending = new java.util.ArrayList<>();
        set(activity, "mSettingsHandler", (VitlaneBrowserActivity.SettingsHandler)
            (restore, key, model, steps, seconds, callback) -> pending.add(callback));
        activity.onStop();
        activity.onResume();
        activity.onPause();
        activity.onStop();
        activity.onResume();
        pending.get(1).onComplete("sk-latest", "gpt-latest", 5, 90, null);
        pending.get(0).onComplete("sk-stale", "gpt-stale", 3, 60, null);
        assertEquals("sk-latest", get(activity, "mApiKey"));
        assertEquals("gpt-latest", get(activity, "mModel"));
        assertEquals(5, get(activity, "mMaxSteps"));
        assertEquals(90, get(activity, "mTimeoutSeconds"));
        activity.onDestroy();
    }

    @Test public void dialogAppliesNewKeyAndModelOnlyAfterSaveAcknowledgement() throws Exception {
        VitlaneBrowserActivity activity = Robolectric.buildActivity(VitlaneBrowserActivity.class).get();
        VitlaneBrowserActivity.SettingsCallback[] pending = new VitlaneBrowserActivity.SettingsCallback[1];
        set(activity, "mApiKey", "sk-old");
        set(activity, "mSettingsHandler", (VitlaneBrowserActivity.SettingsHandler)
            (restore, key, model, steps, seconds, callback) -> {
                assertFalse(restore);
                assertEquals("sk-new", key);
                assertEquals("gpt-custom", model);
                assertEquals(5, steps);
                assertEquals(60, seconds);
                pending[0] = callback;
            });
        activity.onResume();
        java.lang.reflect.Method show = VitlaneBrowserActivity.class.getDeclaredMethod("showSettings");
        show.setAccessible(true);
        show.invoke(activity);
        android.app.AlertDialog dialog = (android.app.AlertDialog) get(activity, "mSettingsDialog");
        java.util.List<android.widget.EditText> fields = new java.util.ArrayList<>();
        collectFields(dialog.getWindow().getDecorView(), fields);
        fields.get(0).setText("sk-new");
        fields.get(1).setText("gpt-custom");
        fields.get(2).setText("5");
        fields.get(3).setText("60");
        dialog.getButton(android.app.AlertDialog.BUTTON_POSITIVE).performClick();
        assertEquals("sk-old", get(activity, "mApiKey"));
        assertTrue(dialog.isShowing());
        pending[0].onComplete("sk-new", "gpt-custom", 5, 60, null);
        assertEquals("sk-new", get(activity, "mApiKey"));
        assertEquals("gpt-custom", get(activity, "mModel"));
        assertEquals(5, get(activity, "mMaxSteps"));
        assertEquals(60, get(activity, "mTimeoutSeconds"));
        assertFalse(dialog.isShowing());
        activity.onDestroy();
    }

    private void collectFields(android.view.View view, java.util.List<android.widget.EditText> fields) {
        if (view instanceof android.widget.EditText) fields.add((android.widget.EditText) view);
        if (view instanceof android.view.ViewGroup) {
            android.view.ViewGroup group = (android.view.ViewGroup) view;
            for (int i = 0; i < group.getChildCount(); i++) collectFields(group.getChildAt(i), fields);
        }
    }

}
