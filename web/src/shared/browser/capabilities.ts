export type BrowserWalletCapability =
  | { kind: "INJECTED_READY"; mobile: boolean }
  | { kind: "UNSUPPORTED_DEVICE"; mobile: true }
  | { kind: "NO_PROVIDER"; mobile: false };

export function isLikelyMobileDevice(
  userAgent = typeof navigator === "undefined" ? "" : navigator.userAgent,
  maxTouchPoints =
    typeof navigator === "undefined" ? 0 : navigator.maxTouchPoints,
) {
  return (
    /Android|iPhone|iPad|iPod|Mobile|Tablet/i.test(userAgent)
    || (/Macintosh/i.test(userAgent) && maxTouchPoints > 1)
  );
}

export function browserWalletCapability(
  userAgent = typeof navigator === "undefined" ? "" : navigator.userAgent,
  providerAvailable =
    typeof window !== "undefined"
    && Boolean((window as Window & { ethereum?: unknown }).ethereum),
  maxTouchPoints =
    typeof navigator === "undefined" ? 0 : navigator.maxTouchPoints,
): BrowserWalletCapability {
  const mobile = isLikelyMobileDevice(userAgent, maxTouchPoints);
  if (providerAvailable) return { kind: "INJECTED_READY", mobile };
  return mobile
    ? { kind: "UNSUPPORTED_DEVICE", mobile: true }
    : { kind: "NO_PROVIDER", mobile: false };
}
