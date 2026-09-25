import {
  type FetchLike,
  VitlaneApiClient,
  VitlaneApiError,
} from "./VitlaneApiClient";

const response = (status: number, body: unknown): Response => ({
  status,
  ok: status >= 200 && status < 300,
  json: async () => body,
}) as Response;

describe("VitlaneApiClient", () => {
  it("injects the opaque bearer token without exposing it in the URL or body", async () => {
    const fetcher = jest.fn<ReturnType<FetchLike>, Parameters<FetchLike>>(async () => (
      response(200, { ok: true })
    ));
    const client = new VitlaneApiClient({
      baseUrl: "https://api.example.test/",
      fetch: fetcher,
      accessToken: async () => "opaque-session-token",
    });

    await expect(client.request("/api/v1/example", {
      method: "POST",
      body: { value: "safe" },
    })).resolves.toEqual({ ok: true });

    const [url, init] = fetcher.mock.calls[0] ?? [];
    expect(url).toBe("https://api.example.test/api/v1/example");
    expect(init?.credentials).toBe("omit");
    expect(init?.headers).toMatchObject({
      Accept: "application/json",
      Authorization: "Bearer opaque-session-token",
      "Content-Type": "application/json",
    });
    expect(init?.body).toBe('{"value":"safe"}');
    expect(String(url)).not.toContain("opaque-session-token");
    expect(String(init?.body)).not.toContain("opaque-session-token");
  });

  it("decodes the canonical error envelope", async () => {
    const client = new VitlaneApiClient({
      baseUrl: "https://api.example.test",
      fetch: async () => response(409, {
        error: {
          code: "BUDGET_VERSION_CONFLICT",
          message: "Budget changed",
          retryable: false,
          reasonCode: "VERSION_MISMATCH",
        },
      }),
    });

    await expect(client.request("/api/v1/example")).rejects.toEqual(
      expect.objectContaining<Partial<VitlaneApiError>>({
        name: "VitlaneApiError",
        code: "BUDGET_VERSION_CONFLICT",
        status: 409,
        metadata: expect.objectContaining({ reasonCode: "VERSION_MISMATCH" }),
      }),
    );
  });

  it("rejects base URLs containing credentials or a path", () => {
    expect(() => new VitlaneApiClient({
      baseUrl: "https://user:secret@example.test/api",
      fetch: async () => response(200, {}),
    })).toThrow("without credentials");
  });
});
