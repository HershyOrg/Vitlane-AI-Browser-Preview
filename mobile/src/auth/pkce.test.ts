import { createPKCEPair } from "./pkce";

describe("createPKCEPair", () => {
  it("creates unpadded base64url verifier and S256 challenge values", async () => {
    const digest = jest.fn(async () => `${"A".repeat(43)}=`);
    const pair = await createPKCEPair({
      randomBytes: async () => Uint8Array.from({ length: 32 }, (_, index) => index),
      sha256Base64: digest,
    });

    expect(pair.verifier).toBe("AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8");
    expect(pair.challenge).toBe("A".repeat(43));
    expect(digest).toHaveBeenCalledWith(pair.verifier);
    expect(pair.verifier).not.toMatch(/[+/=]/);
    expect(pair.challenge).not.toMatch(/[+/=]/);
  });
});
