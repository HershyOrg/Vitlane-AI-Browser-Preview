package com.vitlane.browser;

import android.content.Context;
import android.location.Address;
import android.location.Geocoder;
import android.location.Location;

import org.json.JSONObject;

import java.util.Iterator;
import java.util.List;
import java.util.Locale;
import java.util.TimeZone;

/** A task-scoped, deliberately approximate location passed only when the request needs it. */
public final class BrowserLocationContext {
    private BrowserLocationContext() {}

    @SuppressWarnings("deprecation")
    public static JSONObject fromLocation(Context context, Location location) {
        if (location == null) return unavailable("unavailable");
        String city = "", region = "", country = "";
        try {
            if (context != null && Geocoder.isPresent()) {
                List<Address> values = new Geocoder(context, Locale.getDefault())
                        .getFromLocation(location.getLatitude(), location.getLongitude(), 1);
                Address address = values == null || values.isEmpty() ? null : values.get(0);
                if (address != null) {
                    city = first(address.getLocality(), address.getSubAdminArea());
                    region = first(address.getAdminArea(), address.getSubAdminArea());
                    country = cleanCountry(address.getCountryCode());
                }
            }
        } catch (Exception ignored) { /* Coordinates and timezone still make the task location-aware. */ }
        try {
            return sanitize(new JSONObject().put("available", true).put("reason", "")
                    // Three decimals is roughly a neighborhood, not a precise device trace.
                    .put("latitude", round(location.getLatitude(), 3)).put("longitude", round(location.getLongitude(), 3))
                    .put("accuracyMeters", Math.max(0, Math.min(100000, Math.round(location.getAccuracy()))))
                    .put("approximate", true).put("city", clean(city, 120)).put("region", clean(region, 120))
                    .put("country", country).put("timezone", cleanTimezone(TimeZone.getDefault().getID())));
        } catch (Exception invalid) { return unavailable("unavailable"); }
    }

    public static JSONObject unavailable(String reason) {
        try {
            String value = reason == null ? "unavailable" : reason;
            if (!value.matches("not_requested|permission_denied|unavailable|timeout")) value = "unavailable";
            return new JSONObject().put("available", false).put("reason", value);
        } catch (Exception impossible) { return new JSONObject(); }
    }

    public static JSONObject sanitize(JSONObject raw) throws Exception {
        if (raw == null || raw.length() == 0) return unavailable("not_requested");
        only(raw, "available", "reason", "latitude", "longitude", "accuracyMeters", "approximate",
                "city", "region", "country", "timezone");
        if (!(raw.opt("available") instanceof Boolean)) throw new IllegalArgumentException("Invalid location state");
        if (!raw.getBoolean("available")) return unavailable(raw.optString("reason", "unavailable"));
        if (!(raw.opt("latitude") instanceof Number) || !(raw.opt("longitude") instanceof Number)
                || !(raw.opt("accuracyMeters") instanceof Number) || !(raw.opt("approximate") instanceof Boolean))
            throw new IllegalArgumentException("Invalid location coordinates");
        double latitude = raw.getDouble("latitude"), longitude = raw.getDouble("longitude");
        int accuracy = raw.getInt("accuracyMeters");
        if (!Double.isFinite(latitude) || !Double.isFinite(longitude) || latitude < -90 || latitude > 90
                || longitude < -180 || longitude > 180 || accuracy < 0 || accuracy > 100000
                || !raw.getBoolean("approximate")) throw new IllegalArgumentException("Invalid location bounds");
        String country = cleanCountry(raw.optString("country"));
        return new JSONObject().put("available", true).put("reason", "")
                .put("latitude", round(latitude, 3)).put("longitude", round(longitude, 3))
                .put("accuracyMeters", accuracy).put("approximate", true)
                .put("city", clean(raw.optString("city"), 120)).put("region", clean(raw.optString("region"), 120))
                .put("country", country).put("timezone", cleanTimezone(raw.optString("timezone")));
    }

    /** Official Responses web_search user_location accepts approximate place names, country and timezone. */
    public static JSONObject webSearchUserLocation(JSONObject raw) {
        try {
            JSONObject safe = sanitize(raw), result = new JSONObject().put("type", "approximate");
            if (!safe.optBoolean("available")) return result;
            for (String key : new String[] {"country", "city", "region", "timezone"})
                if (!safe.optString(key).isEmpty()) result.put(key, safe.getString(key));
            return result;
        } catch (Exception ignored) {
            try { return new JSONObject().put("type", "approximate"); }
            catch (Exception impossible) { return new JSONObject(); }
        }
    }

    private static String first(String first, String second) {
        return first != null && !first.trim().isEmpty() ? first : second == null ? "" : second;
    }

    private static String clean(String value, int max) {
        if (value == null) return "";
        String result = value.replaceAll("[\\p{Cc}\\p{Cf}]", " ").replaceAll("\\s+", " ").trim();
        return result.length() > max ? result.substring(0, max) : result;
    }

    private static String cleanCountry(String value) {
        String result = value == null ? "" : value.trim().toUpperCase(Locale.ROOT);
        return result.matches("[A-Z]{2}") ? result : "";
    }

    private static String cleanTimezone(String value) {
        String result = clean(value, 80);
        return result.matches("[A-Za-z0-9_+.-]+(?:/[A-Za-z0-9_+.-]+)*") ? result : "";
    }

    private static double round(double value, int places) {
        double scale = Math.pow(10, places);
        return Math.round(value * scale) / scale;
    }

    private static void only(JSONObject raw, String... keys) {
        java.util.Set<String> allowed = new java.util.HashSet<>(java.util.Arrays.asList(keys));
        Iterator<String> actual = raw.keys();
        while (actual.hasNext()) if (!allowed.contains(actual.next())) throw new IllegalArgumentException("Unsupported location property");
    }
}
