import * as Crypto from "expo-crypto";

export type PKCEPair = {
  verifier: string;
  challenge: string;
};

export type PKCECrypto = {
  randomBytes(byteCount: number): Promise<Uint8Array>;
  sha256Base64(value: string): Promise<string>;
};

const expoPKCECrypto: PKCECrypto = {
  randomBytes: Crypto.getRandomBytesAsync,
  sha256Base64: (value) => Crypto.digestStringAsync(
    Crypto.CryptoDigestAlgorithm.SHA256,
    value,
    { encoding: Crypto.CryptoEncoding.BASE64 },
  ),
};

/** Creates an RFC 7636 S256 pair with a 256-bit verifier. */
export async function createPKCEPair(
  crypto: PKCECrypto = expoPKCECrypto,
): Promise<PKCEPair> {
  const verifier = base64UrlEncode(await crypto.randomBytes(32));
  const challenge = base64ToBase64Url(await crypto.sha256Base64(verifier));
  if (verifier.length !== 43 || challenge.length !== 43) {
    throw new Error("Unable to create a valid PKCE S256 pair");
  }
  return { verifier, challenge };
}

export function base64UrlEncode(bytes: Uint8Array): string {
  const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/";
  let encoded = "";
  for (let index = 0; index < bytes.length; index += 3) {
    const first = bytes[index] ?? 0;
    const hasSecond = index + 1 < bytes.length;
    const hasThird = index + 2 < bytes.length;
    const second = bytes[index + 1] ?? 0;
    const third = bytes[index + 2] ?? 0;
    const block = (first << 16) | (second << 8) | third;
    encoded += alphabet[(block >>> 18) & 63];
    encoded += alphabet[(block >>> 12) & 63];
    encoded += hasSecond ? alphabet[(block >>> 6) & 63] : "=";
    encoded += hasThird ? alphabet[block & 63] : "=";
  }
  return base64ToBase64Url(encoded);
}

function base64ToBase64Url(value: string): string {
  return value.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}
