import type { Currency, Locale, Money } from "./models";

export function formatMoney(value: Money, locale: Locale): string {
  if (value.currency === "KRW") {
    const amount = new Intl.NumberFormat(locale === "ko-KR" ? "ko-KR" : "en-US", {
      maximumFractionDigits: 0,
    }).format(value.amount);
    return locale === "ko-KR" ? `${amount}원` : `KRW ${amount}`;
  }

  const amount = new Intl.NumberFormat(locale === "ko-KR" ? "ko-KR" : "en-US", {
    minimumFractionDigits: 2,
    maximumFractionDigits: 2,
  }).format(value.amount);
  return `USD ${amount}`;
}

export function moneyFromMinor(currency: Currency, amountMinor: number): Money | null {
  if (!Number.isSafeInteger(amountMinor) || amountMinor < 0) return null;
  return {
    currency,
    amount: currency === "USD" ? amountMinor / 100 : amountMinor,
  };
}

export function currencyStep(currency: Currency): number {
  return currency === "KRW" ? 10_000 : 10;
}

export function normalizeMoneyAmount(amount: number, currency: Currency): number {
  if (!Number.isFinite(amount)) return 0;
  return currency === "KRW"
    ? Math.max(0, Math.round(amount))
    : Math.max(0, Math.round(amount * 100) / 100);
}
