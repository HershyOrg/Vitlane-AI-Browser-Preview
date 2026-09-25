import { useEffect, useRef, useState } from "react";
import { ArrowLeft, Clock, ChevronRight, ExternalLink, Package } from "lucide-react";
import { Button, ButtonLink } from "../../../shared/ui";
import { useLocale } from "../../../shared/i18n";
import { TargetSheet } from "./TargetSheet";
import { formatTimelineTime } from "./CurationConversationBubble";
import type { DiscoveryResponse } from "../app/useBackgroundResearch";
import { backgroundAction, type BackgroundView, type ResearchFinding } from "../infra/backgroundApi";
import "./curation-results.css";
import "./curation-background.css";

function discoveryTitle(title: string) {
  // Preserve the full source title in details; list entries lead with the product, not a deal banner.
  return title.replace(/^[☆❤️\s]*(?:초대박|특가)\s+[\d,]+원\s*\([^)]*\)\s*/u, "");
}

function DiscoveryImage({ url, className }: { url?: string; className?: string }) {
  const [failed, setFailed] = useState<string | undefined>();
  const { l } = useLocale();
  return <span className={`curation-background__thumbnail ${className ?? ""}`}>
    {url && failed !== url ? <img src={url} alt="" loading="lazy" onError={() => setFailed(url)} /> : <span className="curation-background__no-image"><Package size={22} aria-hidden="true" /><span>{l("No image", "이미지 없음")}</span></span>}
  </span>;
}

function discoveryPrice(amount: number | undefined, currency: string, locale: string, unknown: string) {
  return amount === undefined ? unknown : new Intl.NumberFormat(locale, { style: "currency", currency, maximumFractionDigits: currency === "KRW" ? 0 : 2 }).format(amount / (currency === "KRW" ? 1 : 100));
}

export function BackgroundDiscoveryResponse({ response, onOpen }: { response: DiscoveryResponse; onOpen: (finding: ResearchFinding) => void }) {
  const { l, locale } = useLocale();
  const count = response.findings.length;
  const lead = response.subject
    ? count === 1 ? l("I found a product matching the {subject} conditions you asked me to watch.", "지켜보던 {subject} 조건에 맞는 상품 1개를 발견했어요.", { subject: response.subject })
      : l("I found {count} products matching the {subject} conditions you asked me to watch.", "지켜보던 {subject} 조건에 맞는 상품 {count}개를 발견했어요.", { count, subject: response.subject })
    : count === 1 ? l("I found a product matching your saved conditions.", "지켜보던 조건에 맞는 상품 1개를 발견했어요.")
      : l("I found {count} products matching your saved conditions.", "지켜보던 조건에 맞는 상품 {count}개를 발견했어요.", { count });
  return <article className="curation-response" data-discovery-response={response.id} title={formatTimelineTime(response.createdAt, locale)}>
    <span className="vt-visually-hidden">{l("Vitlane", "Vitlane")}</span>
    <p className="curation-response__text">{lead}</p>
    <ul className="curation-discovery-cards" aria-label={l("Discovered products", "발견 상품")}>
      {response.findings.map(finding => <li key={finding.id}>
        <Button type="button" emphasis="quiet" className="curation-discovery-card" data-discovery-card={finding.id} aria-label={finding.product.title} onClick={() => onOpen(finding)}>
          <DiscoveryImage url={finding.product.imageUrl} />
          <strong>{discoveryTitle(finding.product.title)}</strong>
          <span className="curation-background__price">{discoveryPrice(finding.product.priceMinor, finding.product.currency, locale, l("Price not reported", "가격 미확인"))}</span>
        </Button>
      </li>)}
    </ul>
  </article>;
}

export function CurationBackground({ curationId, view, refresh, finding, onFindingChange }: {
  curationId: string; view: BackgroundView | null; refresh: () => Promise<void>;
  finding: ResearchFinding | null; onFindingChange: (finding: ResearchFinding | null) => void;
}) {
  const { l, locale } = useLocale();
  const [panel, setPanel] = useState<"subscriptions" | "findings" | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState("");
  const previousFinding = useRef<string | undefined>(undefined);
  useEffect(() => {
    if (finding) document.querySelector<HTMLElement>("[data-background-back]")?.focus();
    else if (previousFinding.current) document.querySelector<HTMLElement>(`[data-finding-id="${CSS.escape(previousFinding.current)}"]`)?.focus();
    previousFinding.current = finding?.id;
  }, [finding?.id]);
  const close = () => { onFindingChange(null); setPanel(null); setError(""); };
  const act = async (kind: "subscriptions" | "findings", id: string, action: "cancel" | "hide" | "candidates") => {
    setBusy(id); setError("");
    try {
      await backgroundAction(curationId, kind, id, action); await refresh();
      if (action === "hide" || action === "candidates") { onFindingChange(null); setPanel("findings"); }
    } catch { setError(l("This action could not finish. Try again after any ongoing research finishes.", "처리를 완료하지 못했어요. 진행 중인 조사가 있다면 끝난 뒤 다시 시도해 주세요.")); }
    finally { setBusy(null); }
  };
  if (!view || (!view.subscriptions.length && !view.findings.length)) return null;
  const active = view.subscriptions.filter(s => s.status === "ACTIVE");
  const findings = view.findings.filter(f => f.status !== "HIDDEN");
  const detail = finding ? view.findings.find(f => f.id === finding.id) ?? finding : null;
  const money = (amount: number | undefined, currency: string) => amount === undefined ? l("Price not reported", "가격 미확인") : new Intl.NumberFormat(locale, { style: "currency", currency, maximumFractionDigits: currency === "KRW" ? 0 : 2 }).format(amount / (currency === "KRW" ? 1 : 100));
  const date = (value: string) => new Intl.DateTimeFormat(locale, { month: "short", day: "numeric" }).format(new Date(value));
  const open = (kind: "subscriptions" | "findings") => { setError(""); onFindingChange(null); setPanel(kind); };
  return <>
    <div className="curation-background__controls" role="group" aria-label={l("Discoveries and saved conditions", "발견 상품과 지켜보는 조건")}>
      <Button type="button" emphasis="quiet" size="compact" onClick={() => open("findings")}>{l("View discoveries", "발견 상품 보기")}</Button>
      <Button type="button" emphasis="quiet" size="compact" onClick={() => open("subscriptions")}><Clock size={14} aria-hidden="true" />{active.length === 1 ? l("Watching 1 condition", "지켜보는 조건 1개") : l("Watching {count} conditions", "지켜보는 조건 {count}개", { count: active.length })}</Button>
    </div>
    {(panel || detail) && <TargetSheet tabs={[]} activeId="background" onSelect={() => {}} title={detail ? l("Discovered product", "발견한 상품") : panel === "subscriptions" ? l("Saved deal conditions", "지켜보는 조건") : l("Discovered products", "발견 상품")} onClose={close}>
      {detail ? <article className="curation-background__detail">
        <Button type="button" emphasis="quiet" size="compact" className="curation-background__back" data-background-back="true" onClick={() => { onFindingChange(null); setPanel("findings"); setError(""); }}><ArrowLeft size={16} aria-hidden="true" />{l("All discoveries", "발견 상품 목록")}</Button>
        {detail.product.imageUrl && <DiscoveryImage url={detail.product.imageUrl} className="curation-background__image" />}
        <h3>{detail.product.title}</h3>
        <p className="curation-background__price">{money(detail.product.priceMinor, detail.product.currency)}</p>
        <p>{detail.reason}</p>
        <p className="curation-background__meta">{detail.product.shippingMinor === undefined ? l("Displayed product price. Delivery cost was not reported.", "표시된 상품 가격이에요. 배송비 정보는 없어요.") : l("Delivery: {price}", "배송비: {price}", { price: money(detail.product.shippingMinor, detail.product.currency) })}</p>
        <p className="curation-background__meta">{l("Observed {date}", "{date} 확인", { date: date(detail.product.observedAt) })}</p>
        <div className="curation-background__actions">
          <ButtonLink emphasis="primary" href={detail.product.url} target="_blank" rel="noopener noreferrer">{l("Open link", "링크 가기")}<ExternalLink size={16} aria-hidden="true" /></ButtonLink>
          {detail.status === "NEW" && <>
            <Button type="button" emphasis="secondary" disabled={!!busy || !detail.product.productRef || Date.parse(detail.product.expiresAt) < Date.now()} onClick={() => void act("findings", detail.id, "candidates")}>{busy === detail.id ? l("Evaluating…", "평가 중…") : l("Add to candidates", "후보 추가")}</Button>
            <Button type="button" emphasis="quiet" disabled={!!busy} onClick={() => void act("findings", detail.id, "hide")}>{l("Hide", "숨기기")}</Button>
          </>}
        </div>
        {detail.status === "ADDED" && <p>{l("Added to candidates. You can compare it in the product group's candidate list.", "후보에 추가했어요. 기존 상품군의 후보 목록에서 함께 비교할 수 있어요.")}</p>}
        {!detail.product.productRef && <p>{l("The original product link has not been resolved. You can still open the source post.", "원 상품 링크가 아직 확인되지 않았어요. 원문에서 확인할 수 있어요.")}</p>}
      </article> : panel === "subscriptions" ? <div className="curation-background__subscriptions">{view.subscriptions.map(s => <article key={s.id}>
        <h3>{s.terms.criteria.subject.label}</h3>
        <p>{s.status === "ACTIVE" ? l("Watching until {date}", "{date}까지 지켜보는 중", { date: date(s.terms.expiresAt) }) : s.status === "EXPIRED" ? l("Expired", "기한 만료") : s.status === "CANCELLED" ? l("Stopped", "중단됨") : l("Ended", "종료됨")}</p>
        <p>{s.terms.criteria.axes.map(a => a.label).join(" · ")}</p>
        {s.terms.maximumMinor !== undefined && <p>{l("Approximate price limit: {price}", "대략적인 가격 상한: {price}", { price: money(s.terms.maximumMinor, s.terms.currency) })}</p>}
        {s.terms.criteria.exclusions.length > 0 && <p>{l("Exclude: {items}", "제외: {items}", { items: s.terms.criteria.exclusions.join(", ") })}</p>}
        {s.status === "ACTIVE" && <Button type="button" emphasis="quiet" disabled={!!busy} onClick={() => void act("subscriptions", s.id, "cancel")}>{l("Stop watching", "그만 지켜보기")}</Button>}
      </article>)}</div> : <div className="curation-background__products">
        {findings.length === 0 && <p>{l("I'll show products here when they match your saved conditions.", "지켜보는 조건에 맞는 상품이 발견되면 여기에 보여드릴게요.")}</p>}
        {findings.map(f => <Button type="button" emphasis="quiet" className="curation-background__product" data-finding-id={f.id} aria-label={f.product.title} key={f.id} onClick={() => { onFindingChange(f); setError(""); }}>
          {f.product.imageUrl ? <img src={f.product.imageUrl} alt="" loading="lazy" /> : <Package size={28} aria-hidden="true" />}
          <span><strong>{discoveryTitle(f.product.title)}</strong><span className="curation-background__price">{money(f.product.priceMinor, f.product.currency)}</span>{f.status === "ADDED" && <span className="curation-background__meta">{l("Added to candidates", "후보에 추가됨")}</span>}</span>
          <ChevronRight size={16} aria-hidden="true" />
        </Button>)}
      </div>}
      {error && <p role="alert">{error}</p>}
    </TargetSheet>}
  </>;
}
