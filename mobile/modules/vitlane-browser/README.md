# Vitlane iOS browser adapter

This Expo local module is the iOS half of Vitlane's device browser boundary. It embeds `WKWebView` below a native origin, security, status, stop, and takeover bar. Web content cannot draw over or update that bar.

The adapter uses the app-owned persistent `WKWebsiteDataStore.default()` profile. A user can therefore sign in during a handoff and continue to product review and checkout in the same mounted browser session. WebKit data remains inside Vitlane's app container: cookies, passwords, storage, and form values are never exported to React Native, the server, or the model. `profileRef` binds permits and commands to this single app-scoped browser profile; multiple isolated merchant profiles require a later reviewed data-store mapping.

The module intentionally exposes a small interface:

- `beginRun`, `observe`, `execute`, `stopAgent`, `takeOver`, and `resumeAgent` are methods on the mounted view ref.
- `observe` returns a structured DOM projection. Input values are never returned. Password, OTP, payment, nonempty-form, account/order/cart, and other sensitive surfaces return an empty handoff observation. A signed-in public product page remains observable after its account/navigation chrome is removed; final-action controls and cross-origin frames are also omitted and reported as excluded boundaries.
- `execute` verifies the shared v1 command envelope, action hash, short expiry, monotonic sequence, device/profile/lease binding, and a device-bound HMAC permit from Keychain. It does not accept selectors, JavaScript, cookies, raw HTML, screenshots, shell commands, or low-level clicks.
- Final purchase, payment, booking, subscription, and cancellation remain user actions. Native control generation, run, sequence, expiry, foreground, document, observation, origin, URL, and recipe bindings are checked before execution.
- Backgrounding, navigation, native Stop, takeover, and direct page gestures invalidate observation references. Stop and takeover resolve a pending mutation as `outcome_unknown`. Starting or resuming AI control waits for the isolated page agent to acknowledge the new control generation before browser work is enabled.

The bundled `VitlanePageAgent.js` runs in a named `WKContentWorld`, separate from page JavaScript. Its generic v1 mutation surface contains scrolling and `builtin.public-search/1/prepare_query`. It also contains one versioned, fixed Coupang purchase-preparation executor: exact approved options, quantity, `바로구매`, `장바구니 담기`, and exact-cart `구매하기`. Every merchant step is bound to the approved offer identities, quantities, price ceilings, native origin/path/query-presence metadata, recipe version, and signed command. Checkout navigation hands control to the user before final order confirmation or payment. There is no caller-supplied selector, generic click/select/input, cookie access, or arbitrary JavaScript entry point. Search preparation uses the input prototype setter, verifies the value, and deliberately fires no focus, input, change, or submit event. `open_candidate` validates the observed candidate but returns `NAVIGATION_REQUIRES_USER`; general automatic link navigation remains disabled until redirect destinations and resolved network addresses can be checked. Any unapproved redirect or script-driven main-frame navigation while AI controls the page is cancelled and handed to the user.

The permit key is not accepted from React Native. Trusted native enrollment must store the exact key bytes used by the permit signer in the iOS Keychain generic-password service `com.vitlane.browser.command-permit`. The Keychain account is `v1:` followed by the lowercase hexadecimal SHA-256 digest of the UTF-8 bytes of this exact canonical JSON, with the actual identifier values substituted: `{"deviceId":"<deviceId>","profileRef":"<profileRef>","purpose":"vitlane-browser-command-permit-v1"}`. Both identifiers use the module's ASCII identifier grammar, so this representation is unambiguous and prevents separator collisions. For the current development Node bridge, the stored key bytes are the UTF-8 bytes of the `BRIDGE_TOKEN` string, not a decoded or independently generated value. A production server may provision a binary per-device/profile key only when it signs with those identical bytes. The production enrollment flow and Go-server key channel are not implemented, and a global `BRIDGE_TOKEN` must not be reused as a production per-device key. Commands fail closed when trusted enrollment is absent or the key is shorter than 32 bytes.

```tsx
const browserRef = useRef<VitlaneBrowserRef>(null);

<VitlaneBrowserView
  ref={browserRef}
  url="https://shop.example.com/catalog"
  onHandoff={({ nativeEvent }) => showHandoff(nativeEvent)}
  style={{ flex: 1 }}
/>
```

This module is Apple-only and requires an Expo development build or native iOS build; Expo Go cannot load it. The TypeScript contract, resources, and platform-neutral Swift policy code can be checked on this Mac, but a complete module compile and XCTest run require Xcode with an iOS Simulator SDK. Android uses the separate Chromium fork adapter and must not silently fall back to Android System WebView.

Two release gates remain explicit. URL syntax and private-address literals are rejected locally, while DNS resolution/rebinding, every redirect hop, and resource-level private-network enforcement are not yet owned by a native network policy; this is why automatic link navigation is disabled. The replay journal is memory-only, so command outcomes do not survive an app restart. Production release requires a durable journal plus the production permit enrollment/channel described above.
