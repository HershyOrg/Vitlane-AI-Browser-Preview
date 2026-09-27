package com.vitlane.browser;

import android.Manifest;
import android.app.Activity;
import android.content.pm.PackageManager;
import android.location.Location;
import android.location.LocationListener;
import android.location.LocationManager;
import android.os.Build;
import android.os.Bundle;
import android.os.CancellationSignal;
import android.os.Handler;

import java.util.List;

/** One-shot Android location lookup. It never persists a location or exposes it to page JavaScript. */
final class BrowserLocationProvider {
    interface Callback { void onLocation(Location location, String failure); }

    private final Activity activity;
    private final Handler ui;
    private final LocationManager manager;
    private int serial;
    private CancellationSignal cancellation;
    private LocationListener listener;

    BrowserLocationProvider(Activity activity, Handler ui) {
        this.activity = activity;
        this.ui = ui;
        this.manager = (LocationManager)activity.getSystemService(Activity.LOCATION_SERVICE);
    }

    static boolean hasPermission(Activity activity) {
        return activity.checkSelfPermission(Manifest.permission.ACCESS_COARSE_LOCATION) == PackageManager.PERMISSION_GRANTED
                || activity.checkSelfPermission(Manifest.permission.ACCESS_FINE_LOCATION) == PackageManager.PERMISSION_GRANTED;
    }

    @SuppressWarnings("deprecation")
    void resolve(Callback callback) {
        cancel();
        final int request = ++serial;
        if (!hasPermission(activity) || manager == null) { callback.onLocation(null, "permission_denied"); return; }
        final Location fallback = bestLastKnown();
        String provider = provider();
        if (provider == null) { callback.onLocation(fallback, fallback == null ? "unavailable" : ""); return; }
        Runnable timeout = () -> finish(request, fallback, fallback == null ? "timeout" : "", callback);
        ui.postDelayed(timeout, 12000);
        try {
            if (Build.VERSION.SDK_INT >= 30) {
                cancellation = new CancellationSignal();
                manager.getCurrentLocation(provider, cancellation, activity.getMainExecutor(),
                        location -> { ui.removeCallbacks(timeout); finish(request, location == null ? fallback : location,
                                location == null && fallback == null ? "unavailable" : "", callback); });
            } else {
                listener = new LocationListener() {
                    @Override public void onLocationChanged(Location location) {
                        ui.removeCallbacks(timeout); finish(request, location == null ? fallback : location,
                                location == null && fallback == null ? "unavailable" : "", callback);
                    }
                    @Override public void onStatusChanged(String provider, int status, Bundle extras) { }
                    @Override public void onProviderEnabled(String provider) { }
                    @Override public void onProviderDisabled(String provider) { }
                };
                manager.requestSingleUpdate(provider, listener, ui.getLooper());
            }
        } catch (SecurityException | IllegalArgumentException unavailable) {
            ui.removeCallbacks(timeout);
            finish(request, fallback, fallback == null ? "unavailable" : "", callback);
        }
    }

    void cancel() {
        serial++;
        if (cancellation != null) { cancellation.cancel(); cancellation = null; }
        if (listener != null && manager != null) {
            try { manager.removeUpdates(listener); } catch (SecurityException ignored) { }
            listener = null;
        }
    }

    private void finish(int request, Location location, String failure, Callback callback) {
        if (request != serial) return;
        if (cancellation != null) { cancellation.cancel(); cancellation = null; }
        if (listener != null && manager != null) {
            try { manager.removeUpdates(listener); } catch (SecurityException ignored) { }
            listener = null;
        }
        serial++;
        callback.onLocation(location, failure);
    }

    @SuppressWarnings("MissingPermission")
    private Location bestLastKnown() {
        Location best = null;
        try {
            List<String> providers = manager.getProviders(true);
            for (String provider : providers) {
                Location candidate = manager.getLastKnownLocation(provider);
                if (candidate == null) continue;
                if (best == null || candidate.getTime() > best.getTime()
                        || candidate.getTime() == best.getTime() && candidate.getAccuracy() < best.getAccuracy()) best = candidate;
            }
        } catch (SecurityException ignored) { }
        return best;
    }

    private String provider() {
        try {
            if (manager.isProviderEnabled(LocationManager.NETWORK_PROVIDER)) return LocationManager.NETWORK_PROVIDER;
            if (manager.isProviderEnabled(LocationManager.GPS_PROVIDER)) return LocationManager.GPS_PROVIDER;
            if (manager.isProviderEnabled(LocationManager.PASSIVE_PROVIDER)) return LocationManager.PASSIVE_PROVIDER;
        } catch (RuntimeException ignored) { }
        return null;
    }
}
