package com.vitlane.browser;

import android.graphics.Bitmap;
import android.graphics.BitmapFactory;

import java.io.ByteArrayOutputStream;
import java.io.InputStream;
import java.net.HttpURLConnection;
import java.net.URI;
import java.net.URL;

/** Bounded local-only loader for a public image already referenced by the current WebView page. */
final class BrowserImageLoader {
    private static final int MAX_BYTES = 5 * 1024 * 1024;
    private static final int MAX_EDGE = 1440;
    private static final int MAX_REDIRECTS = 3;

    private BrowserImageLoader() {}

    static Bitmap load(String rawUrl, String initialCookie) {
        String initial;
        try { initial = BrowserUrlPolicy.requirePublicHttps(rawUrl); }
        catch (RuntimeException invalid) { return null; }
        String current = initial;
        for (int redirects = 0; redirects <= MAX_REDIRECTS; redirects++) {
            HttpURLConnection connection = null;
            try {
                if (!BrowserUrlPolicy.isPublicNetworkUrl(current)) return null;
                connection = (HttpURLConnection) new URL(current).openConnection();
                connection.setInstanceFollowRedirects(false);
                connection.setConnectTimeout(10000);
                connection.setReadTimeout(15000);
                connection.setUseCaches(true);
                connection.setRequestProperty("Accept", "image/avif,image/webp,image/png,image/jpeg,image/gif,image/*;q=0.8");
                connection.setRequestProperty("User-Agent", "Mozilla/5.0 (Linux; Android) AppleWebKit/537.36 Chrome Mobile Safari/537.36");
                if (sameOrigin(initial, current) && safeCookie(initialCookie)) connection.setRequestProperty("Cookie", initialCookie);
                int status = connection.getResponseCode();
                if (status >= 300 && status <= 399) {
                    if (redirects == MAX_REDIRECTS) return null;
                    String location = connection.getHeaderField("Location");
                    if (location == null || location.length() > 4096) return null;
                    current = BrowserUrlPolicy.requirePublicHttps(new URI(current).resolve(location).toString());
                    continue;
                }
                if (status != 200) return null;
                String type = connection.getContentType();
                if (type == null || !type.toLowerCase(java.util.Locale.ROOT).startsWith("image/")
                        || type.toLowerCase(java.util.Locale.ROOT).contains("svg")) return null;
                int declared = connection.getContentLength();
                if (declared > MAX_BYTES) return null;
                try (InputStream input = connection.getInputStream(); ByteArrayOutputStream output = new ByteArrayOutputStream()) {
                    byte[] buffer = new byte[8192];
                    int count;
                    while ((count = input.read(buffer)) != -1) {
                        if (Thread.currentThread().isInterrupted() || output.size() + count > MAX_BYTES) return null;
                        output.write(buffer, 0, count);
                    }
                    return decode(output.toByteArray());
                }
            } catch (Exception | OutOfMemoryError ignored) {
                return null;
            } finally {
                if (connection != null) connection.disconnect();
            }
        }
        return null;
    }

    static Bitmap decode(byte[] bytes) {
        if (bytes == null || bytes.length == 0 || bytes.length > MAX_BYTES || !recognizedImage(bytes)) return null;
        try {
            BitmapFactory.Options bounds = new BitmapFactory.Options();
            bounds.inJustDecodeBounds = true;
            BitmapFactory.decodeByteArray(bytes, 0, bytes.length, bounds);
            if (bounds.outWidth < 1 || bounds.outHeight < 1 || bounds.outWidth > 20000 || bounds.outHeight > 20000) return null;
            int sample = 1;
            while (bounds.outWidth / sample > MAX_EDGE || bounds.outHeight / sample > MAX_EDGE) sample *= 2;
            BitmapFactory.Options options = new BitmapFactory.Options();
            options.inSampleSize = sample;
            options.inPreferredConfig = Bitmap.Config.ARGB_8888;
            return BitmapFactory.decodeByteArray(bytes, 0, bytes.length, options);
        } catch (RuntimeException | OutOfMemoryError ignored) { return null; }
    }

    private static boolean recognizedImage(byte[] value) {
        if (value.length >= 8 && (value[0] & 255) == 0x89 && value[1] == 'P' && value[2] == 'N' && value[3] == 'G') return true;
        if (value.length >= 3 && (value[0] & 255) == 0xff && (value[1] & 255) == 0xd8 && (value[2] & 255) == 0xff) return true;
        if (value.length >= 6 && value[0] == 'G' && value[1] == 'I' && value[2] == 'F'
                && value[3] == '8' && (value[4] == '7' || value[4] == '9') && value[5] == 'a') return true;
        if (value.length >= 12 && value[0] == 'R' && value[1] == 'I' && value[2] == 'F' && value[3] == 'F'
                && value[8] == 'W' && value[9] == 'E' && value[10] == 'B' && value[11] == 'P') return true;
        if (value.length >= 12 && value[4] == 'f' && value[5] == 't' && value[6] == 'y' && value[7] == 'p') {
            String brand = new String(value, 8, 4, java.nio.charset.StandardCharsets.US_ASCII).toLowerCase(java.util.Locale.ROOT);
            return "avif".equals(brand) || "avis".equals(brand) || "heic".equals(brand) || "heix".equals(brand);
        }
        return false;
    }

    private static boolean sameOrigin(String left, String right) {
        try {
            URI a = new URI(left), b = new URI(right);
            return a.getScheme().equalsIgnoreCase(b.getScheme()) && a.getHost().equalsIgnoreCase(b.getHost())
                    && effectivePort(a) == effectivePort(b);
        } catch (Exception ignored) { return false; }
    }

    private static int effectivePort(URI uri) { return uri.getPort() < 0 ? 443 : uri.getPort(); }

    private static boolean safeCookie(String value) {
        return value != null && !value.isEmpty() && value.length() <= 8192 && !value.matches("(?s).*[\r\n].*");
    }
}
