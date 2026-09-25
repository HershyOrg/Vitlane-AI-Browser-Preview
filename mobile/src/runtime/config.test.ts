import { readRuntimeConfig } from "./config";

describe("readRuntimeConfig", () => {
  it("defaults production-shaped server mode to live Korean research", () => {
    expect(readRuntimeConfig({ apiOrigin: "https://api.example.test/" }, {
      platform: "android",
      development: false,
    })).toEqual(expect.objectContaining({
      dataMode: "server",
      apiOrigin: "https://api.example.test",
      country: "KR",
      currency: "KRW",
      executionMode: "LIVE",
      developmentLoginEnabled: false,
      galleryEnabled: false,
      allowInsecureLocalhost: false,
      localWebReviewEnabled: false,
    }));
  });

  it("allows fixture and development bearer login only in development", () => {
    expect(readRuntimeConfig({ dataMode: "fixture" }, {
      platform: "ios",
      development: true,
    })).toEqual({ dataMode: "fixture", galleryEnabled: true });
    expect(() => readRuntimeConfig({ dataMode: "fixture" }, {
      platform: "ios",
      development: false,
    })).toThrow("release builds");

    expect(readRuntimeConfig({
      apiOrigin: "https://api.example.test",
      developmentLogin: "true",
    }, { platform: "ios", development: true })).toEqual(expect.objectContaining({
      developmentLoginEnabled: true,
    }));
  });

  it("fails closed when native server mode has no API origin", () => {
    expect(() => readRuntimeConfig({}, {
      platform: "android",
      development: true,
    })).toThrow("API_ORIGIN is required");
  });

  it("requires HTTPS in release and permits HTTP only for local development", () => {
    expect(() => readRuntimeConfig({ apiOrigin: "http://api.example.test" }, {
      platform: "android",
      development: false,
    })).toThrow("must use HTTPS");
    expect(() => readRuntimeConfig({ apiOrigin: "http://api.example.test" }, {
      platform: "android",
      development: true,
    })).toThrow("must use HTTPS");
    expect(readRuntimeConfig({ apiOrigin: "http://127.0.0.1:8080" }, {
      platform: "android",
      development: true,
    })).toEqual(expect.objectContaining({
      apiOrigin: "http://127.0.0.1:8080",
      allowInsecureLocalhost: true,
    }));
  });

  it("opens Web server review only for an explicit development login on loopback", () => {
    expect(readRuntimeConfig({
      apiOrigin: "http://127.0.0.1:18080",
      developmentLogin: "true",
    }, { platform: "web", development: true })).toEqual(expect.objectContaining({
      localWebReviewEnabled: true,
    }));
    expect(readRuntimeConfig({
      apiOrigin: "https://api.example.test",
      developmentLogin: "true",
    }, { platform: "web", development: true })).toEqual(expect.objectContaining({
      localWebReviewEnabled: false,
    }));
    expect(readRuntimeConfig({
      apiOrigin: "https://127.0.0.1:18080",
      developmentLogin: "true",
    }, { platform: "web", development: false })).toEqual(expect.objectContaining({
      localWebReviewEnabled: false,
    }));
  });
});
