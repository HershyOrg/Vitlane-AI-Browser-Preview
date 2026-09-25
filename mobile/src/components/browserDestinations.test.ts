import {
  BROWSER_DESTINATION_QUERY_MAX_CODE_POINTS,
  BrowserDestinationQueryError,
  buildBrowserDestinationTarget,
  orderedBrowserDestinations,
  sanitizeBrowserDestinationQuery,
} from "./browserDestinations";

describe("browser destinations", () => {
  it("returns reviewed destinations in a stable order with exact hosts", () => {
    const destinations = orderedBrowserDestinations("발볼 넓은 러닝화");

    expect(destinations).toHaveLength(15);
    expect(destinations.map(({ id }) => id)).toEqual([
      "NAVER_SHOPPING",
      "COUPANG",
      "ELEVENST",
      "GMARKET",
      "AUCTION",
      "SSG",
      "MUSINSA",
      "TWENTYNINECM",
      "ZIGZAG",
      "WCONCEPT",
      "OLIVEYOUNG",
      "KURLY",
      "OHOUSE",
      "LOTTEON",
      "NAVER_BOOKING",
    ]);
    for (const destination of destinations) {
      const url = new URL(destination.url);
      expect(url.protocol).toBe("https:");
      expect(url.hostname).toBe(destination.host);
      expect(url.username).toBe("");
      expect(url.password).toBe("");
      expect(url.hash).toBe("");
    }
  });

  it("builds the current Coupang, Naver Shopping, and Naver Map search routes", () => {
    const coupang = buildBrowserDestinationTarget("COUPANG", "  러닝화\n270mm  ");
    expect(new URL(coupang.url).searchParams.get("q")).toBe("러닝화 270mm");
    expect(coupang.host).toBe("www.coupang.com");

    const shopping = buildBrowserDestinationTarget("NAVER_SHOPPING", "러닝화", "en-US");
    expect(new URL(shopping.url).searchParams.get("where")).toBe("shopping");
    expect(new URL(shopping.url).searchParams.get("query")).toBe("러닝화");
    expect(shopping.label).toBe("Naver Shopping");

    const booking = buildBrowserDestinationTarget("NAVER_BOOKING", "성수동 미용실");
    expect(booking.kind).toBe("reservation");
    expect(booking.host).toBe("map.naver.com");
    expect(decodeURIComponent(new URL(booking.url).pathname)).toBe("/p/search/성수동 미용실 예약");
  });

  it("puts Naver Booking first for a reservation-shaped intent", () => {
    expect(orderedBrowserDestinations("성수동 미용실")[0]).toEqual(
      expect.objectContaining({ id: "NAVER_BOOKING", label: "네이버 예약" }),
    );
    expect(orderedBrowserDestinations("발볼 넓은 러닝화")[0]?.id).toBe("NAVER_SHOPPING");
  });

  it("normalizes harmless text before it enters a third-party URL", () => {
    expect(sanitizeBrowserDestinationQuery("  ＡＢＣ\u200b   러닝화 \n  ")).toBe("ABC 러닝화");
  });

  it.each([
    "",
    "name@example.com 러닝화",
    "비밀번호 hunter2",
    "OTP 123456",
    "https://private.example/path",
    "주민등록번호 9001011234567",
    "1".repeat(11),
    "가".repeat(BROWSER_DESTINATION_QUERY_MAX_CODE_POINTS + 1),
  ])("rejects an empty, unbounded, or sensitive query: %s", (query) => {
    expect(sanitizeBrowserDestinationQuery(query)).toBeNull();
    expect(() => buildBrowserDestinationTarget("COUPANG", query)).toThrow(BrowserDestinationQueryError);
  });
});
