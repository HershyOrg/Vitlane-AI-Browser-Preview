package com.vitlane.browser;

import java.net.InetAddress;
import java.net.URI;
import java.util.Locale;

/** Navigation policy shared by the native browser and planner. No network on the UI thread. */
public final class BrowserUrlPolicy {
    private BrowserUrlPolicy() {}

    public static String requirePublicHttps(String value) {
        try {
            if (value == null || value.length() > 4096 || !value.equals(value.trim())
                    || value.matches("(?s).*[\\p{Cntrl}\\\\].*")) throw new IllegalArgumentException();
            URI uri = new URI(value);
            String host = uri.getHost();
            if (!"https".equalsIgnoreCase(uri.getScheme()) || host == null
                    || uri.getRawUserInfo() != null || (uri.getPort() != -1 && uri.getPort() != 443)) {
                throw new IllegalArgumentException();
            }
            host = host.toLowerCase(Locale.ROOT);
            // Only conventional DNS names: exclude literal IPs, legacy numeric IP encodings,
            // local names and special-use suffixes before any DNS/network request.
            if (host.length() > 253 || !host.matches("(?=.{1,253}$)(?:[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\\.)+[a-z]{2,63}")
                    || host.endsWith(".localhost") || host.endsWith(".local")
                    || host.endsWith(".internal") || host.endsWith(".home")
                    || host.endsWith(".lan") || host.endsWith(".test")
                    || host.endsWith(".invalid") || host.endsWith(".example")
                    || host.endsWith(".onion")) throw new IllegalArgumentException();
            return uri.toASCIIString();
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
        if (bytes.length == 4) {
            int a = bytes[0] & 255, b = bytes[1] & 255, c = bytes[2] & 255;
            return a != 0 && a != 10 && a != 127 && a < 224
                    && !(a == 100 && b >= 64 && b <= 127)
                    && !(a == 169 && b == 254) && !(a == 172 && b >= 16 && b <= 31)
                    && !(a == 192 && (b == 168 || (b == 0 && (c == 0 || c == 2))))
                    && !(a == 198 && (b == 18 || b == 19 || (b == 51 && c == 100)))
                    && !(a == 203 && b == 0 && c == 113);
        }
        if (bytes.length != 16) return false;
        // Public global-unicast only; block documentation and IPv4 transition prefixes.
        return (bytes[0] & 0xe0) == 0x20
                && !((bytes[0] & 255) == 0x20 && (bytes[1] & 255) == 0x02)
                && !((bytes[0] & 255) == 0x20 && (bytes[1] & 255) == 0x01
                    && (((bytes[2] & 255) == 0x0d && (bytes[3] & 255) == 0xb8)
                        || ((bytes[2] & 255) == 0 && (bytes[3] & 255) == 0)));
    }
}
