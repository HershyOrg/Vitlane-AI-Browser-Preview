import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { request } from "../../../../shared/api/client";
import { useLocale } from "../../../../shared/i18n";
import { NativeSelect, NativeSelectOption } from "../../../../shared/ui";
import { usePreferences } from "../../../account/app/usePreferences";
import type { CandidatePrice } from "../../domain/candidatePresentation";
import { convertResearchMinor, type ExchangeRateView } from "../domain/exchangeRate";

type CurrencyView = { currency: "KRW" | "USD"; exchange?: ExchangeRateView };
const CurrencyContext = createContext<CurrencyView>({ currency: "KRW" });
export const useResearchCurrency = () => useContext(CurrencyContext);

export function ResearchCurrencyProvider({ curationId, children }: { curationId: string; children: ReactNode }) {
  const { values } = usePreferences();
  const [state, setState] = useState<{ id: string; exchange: ExchangeRateView }>();
  useEffect(() => {
    let active = true;
    void request<ExchangeRateView>(`/api/v1/curations/${encodeURIComponent(curationId)}/exchange-rate`).then((exchange) => { if (active) setState({ id: curationId, exchange }); }).catch(() => { if (active) setState({ id: curationId, exchange: { schemaVersion: "vitlane.exchange-rate.v1", status: "UNAVAILABLE" } }); });
    return () => { active = false; };
  }, [curationId]);
  return <CurrencyContext.Provider value={{ currency: values.preferredCurrency, exchange: state?.id === curationId ? state.exchange : undefined }}>{children}</CurrencyContext.Provider>;
}

export function ResearchCurrencyControls() {
  const { l } = useLocale();
  const preferences = usePreferences();
  const { currency, exchange } = useContext(CurrencyContext);
  return <div>
    <label>{l("Display currency", "보기 통화")}<NativeSelect aria-label={l("Display currency", "보기 통화")} value={currency} disabled={!preferences.ready} onChange={(event) => void preferences.save({ preferredCurrency: event.target.value === "USD" ? "USD" : "KRW" }).catch(() => {})}>
      <NativeSelectOption value="KRW">{l("KRW", "KRW")}</NativeSelectOption><NativeSelectOption value="USD">{l("USD", "USD")}</NativeSelectOption>
    </NativeSelect></label>
    <small>{l("Estimates update daily. Your original prices and budget stay unchanged.", "환산은 일별 참고값입니다. 원가격과 예산은 그대로 유지됩니다.")}</small>
    {exchange?.rate ? <small>{l("Rate as of {date}: 1 USD = {rate} KRW", "{date} 기준 환율: 1 USD = {rate} KRW", { date: exchange.rate.asOf, rate: exchange.rate.rate })}{exchange.status === "STALE" ? l(" · Previous published rate", " · 이전 공시값") : ""} · <a href="https://frankfurter.dev/" target="_blank" rel="noopener noreferrer">{l("Frankfurter", "Frankfurter")}</a></small> : <small>{l("Conversion unavailable; original prices are shown.", "환산값을 확인할 수 없어 원가격을 표시합니다.")}</small>}
  </div>;
}

export function useConvertedPrice(price: CandidatePrice) {
  const { l } = useLocale();
  const { currency, exchange } = useContext(CurrencyContext);
  if (price.kind === "UNKNOWN" || price.currency === currency || !exchange?.rate || exchange.status === "UNAVAILABLE") return undefined;
  const amount = price.kind === "OBSERVED" ? price.amountMinor : price.minimumMinor;
  const min = convertResearchMinor(amount, price.currency, currency, exchange.rate);
  const max = price.kind === "RANGE" ? convertResearchMinor(price.maximumMinor, price.currency, currency, exchange.rate) : undefined;
  if (min === undefined || (price.kind === "RANGE" && max === undefined)) return undefined;
  const value = max !== undefined ? `${formatCandidateMinor(min, currency)} – ${formatCandidateMinor(max, currency)}` : formatCandidateMinor(min, currency);
  return l("Approx. {price}", "약 {price}", { price: value });
}

export function formatCandidateMinor(amount: number, currency: string) {
  if (currency !== "KRW" && currency !== "USD") {
    return `${new Intl.NumberFormat("en-US", { style: "currency", currency }).format(amount / (currency === "JPY" ? 1 : 100))} ${currency}`;
  }
  const divisor = currency === "KRW" ? 1 : 100;
  const value = new Intl.NumberFormat("en-US", {
    minimumFractionDigits: 0,
    maximumFractionDigits: currency === "USD" ? 2 : 0,
  }).format(amount / divisor);
  if (currency === "KRW") return `${value}₩`;
  return `${value}$`;
}
