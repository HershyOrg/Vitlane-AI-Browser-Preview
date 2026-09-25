import type { Localize } from "../../../shared/i18n";

/**
 * Customer-facing names for the platforms a candidate can come from. The
 * server owns the registry (research/domain/kr_mall.go); these labels mirror
 * it so a new mall renders by name. A code the web does not know yet is still
 * a Korean external platform and renders as its code.
 */
export function sourceLabel(source: string | undefined, l: Localize): string {
  switch (source) {
    case undefined:
    case "":
    case "SHOPIFY": return l("Shopify", "Shopify");
    case "AMAZON": return l("Amazon", "Amazon");
    case "COUPANG": return l("Coupang", "쿠팡");
    case "ELEVENST": return l("11st", "11번가");
    case "NAVER_SMARTSTORE": return l("Naver Smart Store", "네이버 스마트스토어");
    case "NAVER_BRANDSTORE": return l("Naver Brand Store", "네이버 브랜드스토어");
    case "MUSINSA": return l("Musinsa", "무신사");
    case "TWENTYNINECM": return l("29CM", "29CM");
    case "OLIVEYOUNG": return l("Olive Young", "올리브영");
    case "KURLY": return l("Kurly", "컬리");
    case "GMARKET": return l("Gmarket", "G마켓");
    case "AUCTION": return l("Auction", "옥션");
    case "SSG": return l("SSG", "SSG");
    case "ZIGZAG": return l("Zigzag", "지그재그");
    case "WCONCEPT": return l("W Concept", "W컨셉");
    case "OHOUSE": return l("Ohouse", "오늘의집");
    case "LOTTEON": return l("Lotte ON", "롯데온");
    case "DAISOMALL": return l("Daiso Mall", "다이소몰");
    default: return source;
  }
}

export function isKoreanExternalSource(source: string | undefined): boolean {
  return Boolean(source) && source !== "SHOPIFY" && source !== "AMAZON";
}
