package com.vitlane.browser;

import java.net.IDN;
import java.net.InetAddress;
import java.net.URI;
import java.net.URLDecoder;
import java.util.Locale;

/** Navigation policy shared by the native browser and planner. No network on the UI thread. */
public final class BrowserUrlPolicy {
    private BrowserUrlPolicy() {}

    public static String requirePublicHttps(String value) {
        return normalize(value, false);
    }

    /** User-entered and server-redirected HTTP links are upgraded before WebView sees them. */
    public static String requirePublicHttpsNavigation(String value) {
        return normalize(value, true);
    }

    /**
     * Extracts the public web fallback carried by an Android intent URI. The app never launches
     * the external intent itself: a verified HTTPS fallback keeps the current browser task in the
     * WebView, while a missing/unsafe fallback is treated as a recoverable navigation failure.
     */
    public static String publicHttpsFallback(String value) {
        if (value == null || value.length() > 4096 || !value.regionMatches(true, 0, "intent://", 0, 9)) return "";
        try {
            int fragment = value.indexOf("#Intent;");
            if (fragment < 9 || !value.endsWith(";end")) return "";
            String parameters = value.substring(fragment + 8, value.length() - 4);
            for (String parameter : parameters.split(";")) {
                if (!parameter.startsWith("S.browser_fallback_url=")) continue;
                String encoded = parameter.substring("S.browser_fallback_url=".length());
                return requirePublicHttpsNavigation(URLDecoder.decode(encoded, "UTF-8"));
            }
            String scheme = "";
            for (String parameter : parameters.split(";")) {
                if (parameter.startsWith("scheme=")) scheme = parameter.substring("scheme=".length());
            }
            if (!"http".equalsIgnoreCase(scheme) && !"https".equalsIgnoreCase(scheme)) return "";
            return requirePublicHttpsNavigation(scheme + "://" + value.substring(9, fragment));
        } catch (Exception ignored) {
            return "";
        }
    }

    public static boolean isExternalAppNavigation(String value) {
        try {
            String scheme = new URI(value).getScheme();
            if (scheme == null) return false;
            scheme = scheme.toLowerCase(Locale.ROOT);
            return !("http".equals(scheme) || "https".equals(scheme) || "about".equals(scheme)
                    || "javascript".equals(scheme) || "data".equals(scheme) || "blob".equals(scheme)
                    || "file".equals(scheme));
        } catch (Exception ignored) {
            return false;
        }
    }

    private static String normalize(String value, boolean upgradeHttp) {
        try {
            if (value == null || value.length() > 4096 || !value.equals(value.trim())
                    || value.matches("(?s).*[\\p{Cntrl}\\\\].*")) throw new IllegalArgumentException();
            URI uri = new URI(value);
            String scheme = uri.getScheme();
            boolean http = "http".equalsIgnoreCase(scheme);
            if (!"https".equalsIgnoreCase(scheme) && !(upgradeHttp && http)) throw new IllegalArgumentException();
            if (uri.getRawUserInfo() != null || uri.getRawAuthority() == null) throw new IllegalArgumentException();
            int port = uri.getPort();
            String host = uri.getHost(), rawAuthority = uri.getRawAuthority();
            if (host == null) {
                String candidate = rawAuthority;
                int separator = candidate.lastIndexOf(':');
                if (separator >= 0 && candidate.indexOf(':') == separator
                        && candidate.substring(separator + 1).matches("[0-9]{1,5}")) {
                    port = Integer.parseInt(candidate.substring(separator + 1));
                    candidate = candidate.substring(0, separator);
                }
                if (candidate.isEmpty() || candidate.contains(":") || candidate.contains("[") || candidate.contains("]")
                        || candidate.contains("@") || candidate.contains("%")) throw new IllegalArgumentException();
                host = candidate;
            }
            if (port != -1 && port != (http ? 80 : 443)) throw new IllegalArgumentException();
            host = IDN.toASCII(host, IDN.USE_STD3_ASCII_RULES).toLowerCase(Locale.ROOT);
            // Only conventional DNS names: exclude literal IPs, legacy numeric IP encodings,
            // local names and special-use suffixes before any DNS/network request.
            if (host.length() > 253 || !host.matches("(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}")
                    || host.endsWith(".localhost") || host.endsWith(".local")
                    || host.endsWith(".internal") || host.endsWith(".home")
                    || host.endsWith(".lan") || host.endsWith(".test")
                    || host.endsWith(".invalid") || host.endsWith(".example")
                    || host.endsWith(".onion")) throw new IllegalArgumentException();
            StringBuilder normalized = new StringBuilder("https://").append(host);
            if (!http && port == 443) normalized.append(":443");
            if (uri.getRawPath() != null) normalized.append(uri.getRawPath());
            if (uri.getRawQuery() != null) normalized.append('?').append(uri.getRawQuery());
            if (uri.getRawFragment() != null) normalized.append('#').append(uri.getRawFragment());
            return new URI(normalized.toString()).toASCIIString();
        } catch (Exception ignored) {
            throw new IllegalArgumentException("공개 HTTPS 웹 주소만 열 수 있습니다.");
        }
    }

    /** Run off the main thread. Treat resolution failures and any private result as blocked. */
    public static boolean isPublicNetworkUrl(String value) {
        try {
            String host = new URI(requirePublicHttps(value)).getHost();
            InetAddress[] addresses = InetAddress.getAllByName(host);
            if (addresses.length == 0) return false;
            for (InetAddress address : addresses) {
                if (!isPublicAddress(address)) return false;
            }
            return true;
        } catch (Exception ignored) {
            return false;
        }
    }

    static boolean isPublicAddress(InetAddress address) {
        if (address.isAnyLocalAddress() || address.isLoopbackAddress() || address.isLinkLocalAddress()
                || address.isSiteLocalAddress() || address.isMulticastAddress()) return false;
        byte[] bytes = address.getAddress();
        if (bytes.length == 4) return isPublicIpv4(bytes, 0);
        if (bytes.length != 16) return false;
        // IPv6-only mobile networks synthesize this well-known NAT64 prefix for public IPv4 sites.
        boolean nat64 = (bytes[0] & 255) == 0 && (bytes[1] & 255) == 0x64
                && (bytes[2] & 255) == 0xff && (bytes[3] & 255) == 0x9b;
        for (int i = 4; nat64 && i < 12; i++) nat64 = bytes[i] == 0;
        if (nat64) return isPublicIpv4(bytes, 12);
        boolean mapped = true;
        for (int i = 0; i < 10; i++) mapped &= bytes[i] == 0;
        mapped &= (bytes[10] & 255) == 255 && (bytes[11] & 255) == 255;
        if (mapped) return isPublicIpv4(bytes, 12);
        // Public global-unicast only; block documentation and IPv4 transition prefixes.
        return (bytes[0] & 0xe0) == 0x20
                && !((bytes[0] & 255) == 0x20 && (bytes[1] & 255) == 0x02)
                && !((bytes[0] & 255) == 0x20 && (bytes[1] & 255) == 0x01
                    && (((bytes[2] & 255) == 0x0d && (bytes[3] & 255) == 0xb8)
                        || ((bytes[2] & 255) == 0 && (bytes[3] & 255) == 0)));
    }

    private static boolean isPublicIpv4(byte[] bytes, int offset) {
        int a = bytes[offset] & 255, b = bytes[offset + 1] & 255, c = bytes[offset + 2] & 255;
        return a != 0 && a != 10 && a != 127 && a < 224
                && !(a == 100 && b >= 64 && b <= 127)
                && !(a == 169 && b == 254) && !(a == 172 && b >= 16 && b <= 31)
                && !(a == 192 && (b == 168 || (b == 0 && (c == 0 || c == 2))))
                && !(a == 198 && (b == 18 || b == 19 || (b == 51 && c == 100)))
                && !(a == 203 && b == 0 && c == 113);
    }
}
