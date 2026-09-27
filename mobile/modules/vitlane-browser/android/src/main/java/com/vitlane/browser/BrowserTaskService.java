package com.vitlane.browser;

import android.app.Activity;
import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

import java.lang.ref.WeakReference;

/** Keeps an explicitly started task alive and routes notification replies back to its Activity. */
public final class BrowserTaskService extends Service {
    public static final String ACTION_OPEN = "com.vitlane.browser.OPEN_TASK";
    private static final String ACTION_REPLY = "com.vitlane.browser.REPLY";
    private static final String ACTION_STOP = "com.vitlane.browser.STOP";
    private static final String REPLY_KEY = "vitlane_reply";
    private static final String CHANNEL = "vitlane_tasks";
    private static final int NOTIFICATION_ID = 7201;
    private static WeakReference<VitlaneBrowserActivity> sActivity = new WeakReference<>(null);
    private static volatile String sPendingReply = "";
    private boolean mDetachedNotification;

    public static void attach(VitlaneBrowserActivity activity) {
        sActivity = new WeakReference<>(activity);
        String pending = sPendingReply;
        if (!pending.isEmpty()) {
            sPendingReply = "";
            activity.onNotificationReply(pending);
        }
    }

    public static void detach(VitlaneBrowserActivity activity) {
        if (sActivity.get() == activity) sActivity.clear();
    }

    public static void start(Activity activity, String message) {
        Intent intent = new Intent(activity, BrowserTaskService.class).putExtra("message", bounded(message, 240));
        if (Build.VERSION.SDK_INT >= 26) activity.startForegroundService(intent); else activity.startService(intent);
    }

    public static void update(Context context, String title, String message, boolean allowReply) {
        Intent intent = new Intent(context, BrowserTaskService.class).putExtra("title", bounded(title, 80))
                .putExtra("message", bounded(message, 600)).putExtra("reply", allowReply);
        if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(intent); else context.startService(intent);
    }

    public static void complete(Context context, String title, String message) {
        Intent intent = new Intent(context, BrowserTaskService.class).putExtra("title", bounded(title, 80))
                .putExtra("message", bounded(message, 600)).putExtra("complete", true);
        if (Build.VERSION.SDK_INT >= 26) context.startForegroundService(intent); else context.startService(intent);
    }

    public static void stop(Context context) {
        context.stopService(new Intent(context, BrowserTaskService.class));
    }

    @Override public void onCreate() {
        super.onCreate();
        NotificationManager manager = (NotificationManager)getSystemService(NOTIFICATION_SERVICE);
        if (Build.VERSION.SDK_INT >= 26 && manager != null) {
            NotificationChannel channel = new NotificationChannel(CHANNEL, "Vitlane AI 작업", NotificationManager.IMPORTANCE_DEFAULT);
            channel.setDescription("진행 중인 브라우저 작업, 질문 및 완료 알림");
            manager.createNotificationChannel(channel);
        }
    }

    @Override public int onStartCommand(Intent intent, int flags, int startId) {
        if (intent != null && ACTION_REPLY.equals(intent.getAction())) {
            android.os.Bundle results = android.app.RemoteInput.getResultsFromIntent(intent);
            CharSequence reply = results == null ? null : results.getCharSequence(REPLY_KEY);
            String text = bounded(reply == null ? "" : reply.toString(), 2000).trim();
            if (!text.isEmpty()) deliver(text);
            return START_NOT_STICKY;
        }
        if (intent != null && ACTION_STOP.equals(intent.getAction())) {
            VitlaneBrowserActivity activity = sActivity.get();
            if (activity != null) activity.onNotificationStop();
            stopSelf();
            return START_NOT_STICKY;
        }
        String title = intent == null ? "Vitlane AI가 작업 중입니다" : intent.getStringExtra("title");
        String message = intent == null ? "작업을 계속하고 있어요." : intent.getStringExtra("message");
        if (title == null || title.isEmpty()) title = "Vitlane AI가 작업 중입니다";
        if (message == null || message.isEmpty()) message = "작업을 계속하고 있어요.";
        boolean reply = intent != null && intent.getBooleanExtra("reply", false);
        boolean complete = intent != null && intent.getBooleanExtra("complete", false);
        Notification notification = notification(title, message, reply, complete);
        if (Build.VERSION.SDK_INT >= 29) startForeground(NOTIFICATION_ID, notification, ServiceInfo.FOREGROUND_SERVICE_TYPE_DATA_SYNC);
        else startForeground(NOTIFICATION_ID, notification);
        if (complete) {
            mDetachedNotification = true;
            if (Build.VERSION.SDK_INT >= 24) stopForeground(STOP_FOREGROUND_DETACH);
            else stopForeground(false);
            stopSelf();
        }
        return START_NOT_STICKY;
    }

    @Override public IBinder onBind(Intent intent) { return null; }

    @Override public void onDestroy() {
        if (!mDetachedNotification) stopForeground(true);
        super.onDestroy();
    }

    private Notification notification(String title, String message, boolean allowReply) {
        return notification(title, message, allowReply, false);
    }

    private Notification notification(String title, String message, boolean allowReply, boolean complete) {
        int icon = getApplicationInfo().icon == 0 ? android.R.drawable.ic_dialog_info : getApplicationInfo().icon;
        Intent open = new Intent(this, VitlaneBrowserActivity.class).setAction(ACTION_OPEN)
                .addFlags(Intent.FLAG_ACTIVITY_REORDER_TO_FRONT | Intent.FLAG_ACTIVITY_SINGLE_TOP);
        PendingIntent openPending = PendingIntent.getActivity(this, 1, open, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
        Notification.Builder builder = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(this, CHANNEL) : new Notification.Builder(this);
        builder.setSmallIcon(icon).setContentTitle(title).setContentText(message)
                .setStyle(new Notification.BigTextStyle().bigText(message)).setContentIntent(openPending)
                .setOnlyAlertOnce(!allowReply && !complete).setOngoing(!allowReply && !complete)
                .setCategory(complete ? Notification.CATEGORY_STATUS : Notification.CATEGORY_PROGRESS)
                .setVisibility(Notification.VISIBILITY_PRIVATE);
        if (allowReply) {
            android.app.RemoteInput input = new android.app.RemoteInput.Builder(REPLY_KEY).setLabel("답변 입력").build();
            Intent replyIntent = new Intent(this, BrowserTaskService.class).setAction(ACTION_REPLY);
            int mutable = Build.VERSION.SDK_INT >= 31 ? PendingIntent.FLAG_MUTABLE : 0;
            PendingIntent pending = PendingIntent.getService(this, 2, replyIntent, PendingIntent.FLAG_UPDATE_CURRENT | mutable);
            builder.addAction(new Notification.Action.Builder(0, "답변", pending).addRemoteInput(input).build());
        } else if (!complete) {
            Intent stopIntent = new Intent(this, BrowserTaskService.class).setAction(ACTION_STOP);
            PendingIntent stop = PendingIntent.getService(this, 3, stopIntent, PendingIntent.FLAG_UPDATE_CURRENT | PendingIntent.FLAG_IMMUTABLE);
            builder.addAction(new Notification.Action.Builder(0, "중지", stop).build());
        }
        return builder.build();
    }

    private void deliver(String text) {
        VitlaneBrowserActivity activity = sActivity.get();
        if (activity == null) sPendingReply = text;
        else activity.onNotificationReply(text);
        NotificationManager manager = (NotificationManager)getSystemService(NOTIFICATION_SERVICE);
        if (manager != null) manager.notify(NOTIFICATION_ID,
                notification("답변을 받았습니다", "AI가 작업을 이어가고 있어요.", false));
    }

    private static String bounded(String value, int max) {
        if (value == null) return "";
        String clean = value.replaceAll("[\\p{Cc}\\p{Cf}]", " ").replaceAll("\\s+", " ").trim();
        return clean.length() > max ? clean.substring(0, max) : clean;
    }
}
