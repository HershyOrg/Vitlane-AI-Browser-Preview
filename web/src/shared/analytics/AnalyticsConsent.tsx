import { useEffect, useState, useRef } from "react";
import { X } from "lucide-react";
import { Button } from "../ui";
import { analyticsChanged, analyticsConfig, analyticsSettings, consent, consentNoticeDelay, deferConsentNotice, setConsent } from "./analytics";
import "./analytics.css";

export function AnalyticsConsent({ l, showSettings = false }: { l: (en: string, ko: string) => string; showSettings?: boolean }) {
  const notice = useRef<HTMLElement>(null);
  const [, update] = useState(0);
  const [opened, setOpened] = useState(false);
  const [automatic, setAutomatic] = useState(false);
  useEffect(() => { if (opened) notice.current?.focus(); }, [opened]);
  useEffect(() => {
    let timer: number | undefined;
    let previousConsent = consent();
    const change = () => {
      window.clearTimeout(timer);
      update(n => n + 1);
      const choice = consent();
      if (choice !== previousConsent || choice === "allowed") setAutomatic(false);
      previousConsent = choice;
      if (!analyticsConfig() || analyticsConfig()?.mode === "disabled" || choice === "allowed" || document.visibilityState === "hidden") return;
      if (consentNoticeDelay() === 0) {
        if (choice === "denied") deferConsentNotice();
        setAutomatic(true);
        if (choice === "unknown") return;
      }
      timer = window.setTimeout(change, consentNoticeDelay() + 1);
    };
    const open = () => setOpened(true);
    window.addEventListener(analyticsChanged, change);
    window.addEventListener(analyticsSettings, open);
    for (const event of ["focus", "pageshow", "storage"]) window.addEventListener(event, change);
    document.addEventListener("visibilitychange", change);
    change();
    return () => {
      window.clearTimeout(timer);
      window.removeEventListener(analyticsChanged, change);
      window.removeEventListener(analyticsSettings, open);
      for (const event of ["focus", "pageshow", "storage"]) window.removeEventListener(event, change);
      document.removeEventListener("visibilitychange", change);
    };
  }, []);
  if (!analyticsConfig() || analyticsConfig()?.mode === "disabled") return null;
  const visible = opened || automatic;
  const choose = (value: "allowed" | "denied") => { setConsent(value); setOpened(false); setAutomatic(false); };
  const dismiss = () => {
    if (consent() !== "allowed") deferConsentNotice();
    setOpened(false); setAutomatic(false);
    window.dispatchEvent(new Event(analyticsChanged));
  };
  return <>{visible ? <section ref={notice} tabIndex={-1} className="analytics-notice" aria-label={l("Usage information consent", "이용 정보 제공 동의")}>
    <div className="analytics-notice__copy">
    <p className="analytics-notice__title">{l("Usage information consent", "이용 정보 제공 동의")}</p>
    <p>{l("May we use your usage information to improve Vitlane? You can use the service without agreeing. Essential service and transaction records and permitted internal statistics remain.", "서비스 개선을 위해 이용 정보를 분석하는 데 동의하시겠어요? 동의하지 않아도 서비스를 이용할 수 있습니다. 필수 서비스·거래 기록과 허용된 내부 통계는 유지됩니다.")}
      {" "}<a href={l("https://vitlane.com/privacy/", "https://vitlane.com/ko/privacy/")} target="_blank" rel="noreferrer">{l("Privacy details", "개인정보 처리 안내")}</a></p>
    </div>
    <div className="analytics-notice__actions">
      <Button emphasis="secondary" onClick={() => choose("denied")}>{l("Decline", "동의하지 않기")}</Button>
      <Button emphasis="primary" onClick={() => choose("allowed")}>{l("Allow", "허용하기")}</Button>
      <Button size="compact" emphasis="quiet" onClick={dismiss} aria-label={l("Close", "닫기")} title={l("Close", "닫기")}><X aria-hidden="true" /></Button>
    </div>
  </section> : null}
    {showSettings && !visible ? <Button className="analytics-settings" size="compact" emphasis="quiet" onClick={() => setOpened(true)}>{l("Usage information preferences", "정보 제공 설정")}</Button> : null}
  </>;
}
