import type { Locale } from "../i18n/messages";

export type BrowserDestinationKind = "shopping" | "reservation";

export type BrowserDestinationId =
  | "NAVER_SHOPPING"
  | "COUPANG"
  | "ELEVENST"
  | "GMARKET"
  | "AUCTION"
  | "SSG"
  | "MUSINSA"
  | "TWENTYNINECM"
  | "ZIGZAG"
  | "WCONCEPT"
  | "OLIVEYOUNG"
  | "KURLY"
  | "OHOUSE"
  | "LOTTEON"
  | "NAVER_BOOKING";

export type BrowserDestinationTarget = {
  readonly id: BrowserDestinationId;
  readonly label: string;
  readonly url: string;
  readonly kind: BrowserDestinationKind;
  readonly host: string;
};

export const BROWSER_DESTINATION_QUERY_MAX_CODE_POINTS = 80;
export const BROWSER_DESTINATION_QUERY_MAX_UTF8_BYTES = 240;

type DestinationDefinition = {
  readonly id: BrowserDestinationId;
  readonly label: Readonly<Record<Locale, string>>;
  readonly host: string;
  readonly kind: BrowserDestinationKind;
  readonly buildURL: (query: string) => URL;
};

const searchURL = (
  base: string,
  parameter: string,
  query: string,
  fixedParameters: Readonly<Record<string, string>> = {},
): URL => {
  const url = new URL(base);
  for (const [key, value] of Object.entries(fixedParameters)) url.searchParams.set(key, value);
  url.searchParams.set(parameter, query);
  return url;
};

const definitions = [
  {
    id: "NAVER_SHOPPING",
    label: { "ko-KR": "네이버쇼핑", "en-US": "Naver Shopping" },
    host: "search.naver.com",
    kind: "shopping",
    buildURL: (query) => searchURL(
      "https://search.naver.com/search.naver",
      "query",
      query,
      { where: "shopping" },
    ),
  },
  {
    id: "COUPANG",
    label: { "ko-KR": "쿠팡", "en-US": "Coupang" },
    host: "www.coupang.com",
    kind: "shopping",
    buildURL: (query) => searchURL("https://www.coupang.com/np/search", "q", query),
  },
  {
    id: "ELEVENST",
    label: { "ko-KR": "11번가", "en-US": "11st" },
    host: "search.11st.co.kr",
    kind: "shopping",
    buildURL: (query) => searchURL("https://search.11st.co.kr/Search.tmall", "kwd", query),
  },
  {
    id: "GMARKET",
    label: { "ko-KR": "G마켓", "en-US": "Gmarket" },
    host: "www.gmarket.co.kr",
    kind: "shopping",
    buildURL: (query) => searchURL("https://www.gmarket.co.kr/n/search", "keyword", query),
  },
  {
    id: "AUCTION",
    label: { "ko-KR": "옥션", "en-US": "Auction" },
    host: "www.auction.co.kr",
    kind: "shopping",
    buildURL: (query) => searchURL("https://www.auction.co.kr/n/search", "keyword", query),
  },
  {
    id: "SSG",
    label: { "ko-KR": "SSG.COM", "en-US": "SSG.COM" },
    host: "www.ssg.com",
    kind: "shopping",
    buildURL: (query) => searchURL(
      "https://www.ssg.com/search.ssg",
      "query",
      query,
      { target: "all" },
    ),
  },
  {
    id: "MUSINSA",
    label: { "ko-KR": "무신사", "en-US": "MUSINSA" },
    host: "www.musinsa.com",
    kind: "shopping",
    buildURL: (query) => searchURL("https://www.musinsa.com/search/goods", "keyword", query),
  },
  {
    id: "TWENTYNINECM",
    label: { "ko-KR": "29CM", "en-US": "29CM" },
    host: "www.29cm.co.kr",
    kind: "shopping",
    buildURL: (query) => searchURL("https://www.29cm.co.kr/store/search", "keyword", query),
  },
  {
    id: "ZIGZAG",
    label: { "ko-KR": "지그재그", "en-US": "Zigzag" },
    host: "zigzag.kr",
    kind: "shopping",
    buildURL: (query) => searchURL("https://zigzag.kr/search", "keyword", query),
  },
  {
    id: "WCONCEPT",
    label: { "ko-KR": "W컨셉", "en-US": "W Concept" },
    host: "www.wconcept.co.kr",
    kind: "shopping",
    buildURL: (query) => searchURL("https://www.wconcept.co.kr/Search", "keyword", query),
  },
  {
    id: "OLIVEYOUNG",
    label: { "ko-KR": "올리브영", "en-US": "Olive Young" },
    host: "www.oliveyoung.co.kr",
    kind: "shopping",
    buildURL: (query) => searchURL(
      "https://www.oliveyoung.co.kr/store/search/getSearchMain.do",
      "query",
      query,
    ),
  },
  {
    id: "KURLY",
    label: { "ko-KR": "컬리", "en-US": "Kurly" },
    host: "www.kurly.com",
    kind: "shopping",
    buildURL: (query) => searchURL("https://www.kurly.com/search", "sword", query),
  },
  {
    id: "OHOUSE",
    label: { "ko-KR": "오늘의집", "en-US": "Ohouse" },
    host: "ohou.se",
    kind: "shopping",
    buildURL: (query) => searchURL("https://ohou.se/search/index", "query", query),
  },
  {
    id: "LOTTEON",
    label: { "ko-KR": "롯데ON", "en-US": "Lotte ON" },
    host: "www.lotteon.com",
    kind: "shopping",
    buildURL: (query) => searchURL(
      "https://www.lotteon.com/csearch/search/search",
      "q",
      query,
      { render: "search", platform: "pc" },
    ),
  },
  {
    id: "NAVER_BOOKING",
    label: { "ko-KR": "네이버 예약", "en-US": "Naver Booking" },
    host: "map.naver.com",
    kind: "reservation",
    buildURL: (query) => {
      const searchQuery = /예약/u.test(query) ? query : `${query} 예약`;
      return new URL(`https://map.naver.com/p/search/${encodeURIComponent(searchQuery)}`);
    },
  },
] as const satisfies readonly DestinationDefinition[];

const definitionById = new Map<BrowserDestinationId, DestinationDefinition>(
  definitions.map((definition) => [definition.id, definition]),
);

export class BrowserDestinationQueryError extends Error {
  readonly code = "BROWSER_DESTINATION_QUERY_INVALID";

  constructor() {
    super("Browser destination query is empty, too long, or contains sensitive data.");
    this.name = "BrowserDestinationQueryError";
  }
}

/**
 * Cleans ordinary search text and rejects values that look like credentials,
 * personal identifiers, raw URLs, or other data that should not be placed in
 * a third-party search URL.
 */
export function sanitizeBrowserDestinationQuery(value: string): string | null {
  const normalized = value
    .normalize("NFKC")
    .replace(/[\u0000-\u001f\u007f-\u009f\u200b-\u200f\u202a-\u202e\u2060-\u206f\ufeff]/gu, " ")
    .replace(/\s+/gu, " ")
    .trim();

  if (!normalized) return null;
  if ([...normalized].length > BROWSER_DESTINATION_QUERY_MAX_CODE_POINTS) return null;
  if (utf8ByteLength(normalized) > BROWSER_DESTINATION_QUERY_MAX_UTF8_BYTES) return null;
  if (looksSensitive(normalized)) return null;
  return normalized;
}

export function buildBrowserDestinationTarget(
  id: BrowserDestinationId,
  query: string,
  locale: Locale = "ko-KR",
): BrowserDestinationTarget {
  const safeQuery = sanitizeBrowserDestinationQuery(query);
  if (!safeQuery) throw new BrowserDestinationQueryError();

  const definition = definitionById.get(id);
  if (!definition) throw new Error("Unknown browser destination.");
  const url = definition.buildURL(safeQuery);
  assertReviewedURL(url, definition.host);

  return {
    id: definition.id,
    label: definition.label[locale],
    url: url.toString(),
    kind: definition.kind,
    host: definition.host,
  };
}

export function orderedBrowserDestinations(
  query: string,
  locale: Locale = "ko-KR",
): readonly BrowserDestinationTarget[] {
  const safeQuery = sanitizeBrowserDestinationQuery(query);
  if (!safeQuery) throw new BrowserDestinationQueryError();
  const ordered = reservationIntent.test(safeQuery)
    ? [
        definitions.find(({ id }) => id === "NAVER_BOOKING")!,
        ...definitions.filter(({ id }) => id !== "NAVER_BOOKING"),
      ]
    : definitions;
  return ordered.map(({ id }) => buildBrowserDestinationTarget(id, safeQuery, locale));
}

const reservationIntent = /(?:예약|미용실|식당|레스토랑|숙소|호텔|병원|클리닉|마사지|네일|restaurant|hotel|clinic|massage|salon|reservation|booking)/iu;

function assertReviewedURL(url: URL, expectedHost: string): void {
  if (
    url.protocol !== "https:"
    || url.hostname !== expectedHost
    || url.port !== ""
    || url.username !== ""
    || url.password !== ""
    || url.hash !== ""
  ) {
    throw new Error("Browser destination URL is outside the reviewed route.");
  }
}

function looksSensitive(value: string): boolean {
  const lowered = value.toLocaleLowerCase("en-US");
  if (/(?:password|passwd|passcode|one[ -]?time(?: password| code)?|otp|cvv|cvc|card[ -]?number|api[ -]?key|access[ -]?token|bearer|authorization|client[ -]?secret)/u.test(lowered)) return true;
  if (/(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록(?:번호)?)/u.test(value)) return true;
  if (/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/iu.test(value)) return true;
  if (/\b(?:https?|ftp):\/\//iu.test(value)) return true;
  return (value.match(/\d/gu) ?? []).length >= 11;
}

function utf8ByteLength(value: string): number {
  let bytes = 0;
  for (const character of value) {
    const point = character.codePointAt(0) ?? 0;
    bytes += point <= 0x7f ? 1 : point <= 0x7ff ? 2 : point <= 0xffff ? 3 : 4;
  }
  return bytes;
}
