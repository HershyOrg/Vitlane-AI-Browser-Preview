import { SecureSessionStore } from "./SecureSessionStore";
import type { NativeAuthSession } from "./types";

const session: NativeAuthSession = {
  sessionToken: "t".repeat(43),
  expiresAt: "2030-01-01T00:00:00Z",
  user: {
    id: "4e334d35-f22b-4d95-bc9d-e26d9bd58431",
    email: "person@example.com",
    displayName: "Person",
    createdAt: "2026-01-01T00:00:00Z",
    marketingAdmin: false,
    phase5Operator: false,
  },
};

describe("SecureSessionStore", () => {
  it("round-trips native sessions through SecureStore", async () => {
    let stored: string | null = null;
    const driver = {
      isAvailableAsync: jest.fn(async () => true),
      getItemAsync: jest.fn(async () => stored),
      setItemAsync: jest.fn(async (_key: string, value: string) => { stored = value; }),
      deleteItemAsync: jest.fn(async () => { stored = null; }),
    };
    const store = new SecureSessionStore({ platform: "ios", driver });

    await store.save(session);
    await expect(store.load()).resolves.toEqual(session);

    expect(driver.setItemAsync).toHaveBeenCalledWith(
      "vitlane.auth.session.v1",
      JSON.stringify(session),
      expect.objectContaining({ keychainService: "com.vitlane.mobile.auth" }),
    );
  });

  it("does not persist tokens on web", async () => {
    const driver = {
      isAvailableAsync: jest.fn(async () => true),
      getItemAsync: jest.fn(async () => null),
      setItemAsync: jest.fn(async () => undefined),
      deleteItemAsync: jest.fn(async () => undefined),
    };
    const store = new SecureSessionStore({ platform: "web", driver });

    await expect(store.load()).resolves.toBeNull();
    await expect(store.save(session)).rejects.toMatchObject({
      code: "UNSUPPORTED_PLATFORM",
    });
    expect(driver.getItemAsync).not.toHaveBeenCalled();
    expect(driver.setItemAsync).not.toHaveBeenCalled();
  });

  it("deletes malformed native session records", async () => {
    const driver = {
      isAvailableAsync: jest.fn(async () => true),
      getItemAsync: jest.fn(async () => '{"sessionToken":"exposed-but-invalid"}'),
      setItemAsync: jest.fn(async () => undefined),
      deleteItemAsync: jest.fn(async () => undefined),
    };
    const store = new SecureSessionStore({ platform: "android", driver });

    await expect(store.load()).resolves.toBeNull();
    expect(driver.deleteItemAsync).toHaveBeenCalledWith(
      "vitlane.auth.session.v1",
      expect.any(Object),
    );
  });
});
