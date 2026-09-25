# Protocol v1 golden fixtures

These files are cross-language fixtures for the Vitlane native host and server.
The HMAC key used only to reproduce `golden-authorized-command.v1.json` is the
UTF-8 byte string `test-only-permit-key-32-bytes-minimum-value`. It is not a
runtime credential.

Canonical JSON has no whitespace. Arrays retain order. Object keys are sorted
lexicographically at every level. Null, booleans, finite numbers and strings
use JSON encoding. UTF-8 bytes of that canonical string are hashed.

`actionHash` is lowercase hexadecimal SHA-256 of the canonical `action` object.
The permit input is the canonical object containing, and only containing,
`protocolVersion`, `commandId`, `runId`, `deviceId`, `profileRef`, `sequence`,
`leaseEpoch`, `controlGeneration`, `expiresAt`, `actionHash`, and `action`.
`serverPermit` is excluded. The output is
`hmac-sha256:` plus unpadded base64url HMAC-SHA-256, keyed by the raw UTF-8
`BRIDGE_TOKEN` bytes. Verify the hash and permit in constant time before the
native host calls the isolated-world executor.

The bridge accepts `golden-step-request.v1.json` at `POST /v1/step` and wraps
its response as `{ "authorizedCommand": { ... } }`. The native executor returns
an `ActionResult` shaped like `golden-action-result.v1.json`; it never infers a
purchase or booking outcome from a navigation or DOM event.

The local server-side merchant lookup accepts `golden-discover-request.v1.json`
at `POST /v1/discover` and returns the deterministic candidate batch in
`golden-discover-response.v1.json`. These discovery fixtures describe a bridge
response only; they do not represent a signed native command, a device-browser
observation, or proof that a live merchant request succeeded.

`open_candidate` is limited to a public, query-free, credential-free link whose
origin exactly equals the observation's browser-verified `topOrigin`. Other
origins are omitted by the page projection and require human control. DNS and
redirect resolution still require native navigation/network policy and a fresh
post-navigation observation; these fixtures do not prove DNS-rebinding safety.
Until that policy exists, the v1 executor returns `NAVIGATION_REQUIRES_USER`
and never calls `location.assign`. Any different-document load or URL change
during a run ends that run before the new page can be observed.

The signed page identity also carries the browser-verified `pathname` and a
`queryOrFragmentPresent` bit. Merchant preparation steps compare those fields
with the current native URL; the exact Coupang cart entry additionally requires
`/cartView.pang` with no query or fragment before checkout preparation can run.
