package com.vitlane.browser;

import java.net.InetAddress;
import org.junit.Test;
import static org.junit.Assert.*;

public class BrowserUrlPolicyTest {
    @Test public void acceptsPublicHttpsSearchAndProductUrls() {
        for (String url : new String[] {
                "https://www.google.com/search?q=wireless%20mouse",
                "https://www.coupang.com/vp/products/123?itemId=456&vendorItemId=789",
                "https://example.com:443/product#details"}) {
            assertEquals(url, BrowserUrlPolicy.requirePublicHttps(url));
        }
    }

    @Test public void rejectsLocalSchemesCredentialsPortsAndAmbiguousHosts() {
        for (String url : new String[] {
                "http://example.com", "file:///etc/passwd", "javascript:alert(1)",
                "intent://example.com", "data:text/html,hello", "https://localhost",
                "https://127.0.0.1", "https://[::1]", "https://2130706433", "https://0x7f000001",
                "https://127.1", "https://127.0.0.1.nip.io:8443", "https://account.local",
                "https://account.internal", "https://foo.localhost", "https://example.com:80",
                "https://user:password@example.com", "https://example.com@localhost",
                "https://example.com\\@evil.com", "https://example.com\n", " https://example.com",
                "https://example.com.", "https://example.com:65536", "https://example.com/%xy"}) {
            try {
                BrowserUrlPolicy.requirePublicHttps(url);
                fail("Accepted unsafe URL: " + url);
            } catch (IllegalArgumentException expected) { }
        }
    }

    @Test public void rejectsPrivateAndSpecialNetworkAddressesWithoutDns() throws Exception {
        int[][] denied = {{0,0,0,0},{10,1,2,3},{127,0,0,1},{169,254,169,254},
                {172,16,0,1},{192,168,1,1},{100,64,0,1},{198,18,0,1},{192,0,2,1},
                {198,51,100,1},{203,0,113,1},{224,0,0,1},{255,255,255,255}};
        for (int[] address : denied) {
            assertFalse(BrowserUrlPolicy.isPublicAddress(InetAddress.getByAddress(new byte[] {
                    (byte) address[0], (byte) address[1], (byte) address[2], (byte) address[3]})));
        }
        assertTrue(BrowserUrlPolicy.isPublicAddress(InetAddress.getByAddress(new byte[] {8,8,8,8})));
        assertFalse(BrowserUrlPolicy.isPublicAddress(InetAddress.getByName("fc00::1")));
        assertFalse(BrowserUrlPolicy.isPublicAddress(InetAddress.getByName("2001:db8::1")));
        assertTrue(BrowserUrlPolicy.isPublicAddress(InetAddress.getByName("2606:4700:4700::1111")));
    }
}
