import { track } from "../../../shared/analytics/analytics";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { useNavigate, useParams, useSearchParams } from "react-router";
import { ArrowLeft, CircleAlert, CreditCard, MapPin, PackageCheck, ShieldCheck } from "lucide-react";
import { APIError } from "../../../shared/api/client";
import { randomUUID } from "../../../shared/browser/randomUUID";
import { Button, Checkbox, Field, Input, Notice, RadioGroup, RadioGroupItem, Textarea } from "../../../shared/ui";
import { useLocale, type Localize } from "../../../shared/i18n";
import {
  saveDefaultShippingProfile,
} from "../../account/infra/accountApi";
import {
  formatUSPhoneInput,
  shippingAddressErrorSummary,
  validateShippingAddress,
  type ShippingAddressErrors,
  type ShippingAddressField,
} from "../../account/domain/shippingAddress";
import {
  createOrderSheet,
  getAgencyOrderCapability,
  getOrderSheet,
  issueAgencyOrder,
  preflightOrderSheet,
  selectDeliveryOptions,
  setOrderSheetShippingAddress,
  type AgencyOrderCapability,
  type OrderSheet,
  type OrderSheetShippingAddress,
} from "../infra/agencyOrderApi";
import "./agency-order.css";

type PaymentMethod = "TVITUSD" | "PAYPAL_SANDBOX" | "PAYPAL_LIVE";

const emptyShippingAddress: OrderSheetShippingAddress = {
  recipientName: "", addressLine1: "", addressLine2: "", city: "", region: "",
  postalCode: "", country: "US", phone: "",
};

export function OrderSheetPage() {
  const { l, locale } = useLocale();
  const { curationId = "" } = useParams();
  const [searchParams] = useSearchParams();
  const navigate = useNavigate();
  const expectedCartVersion = Number(searchParams.get("cartVersion") ?? "-1");
  const [sheet, setSheet] = useState<OrderSheet>();
  useEffect(() => { if (sheet && sheet.state !== "CONSUMED") track({ name: "order_sheet_viewed" }, "sheet:" + sheet.id); }, [sheet?.id]);
  const [paymentMethod, setPaymentMethod] = useState<PaymentMethod | "">("");
  const [working, setWorking] = useState(false);
  const [error, setError] = useState<string>();
  const [confirmation, setConfirmation] = useState<string>();
  const [capability, setCapability] = useState<AgencyOrderCapability>();
  const [liveAcknowledged, setLiveAcknowledged] = useState(false);
  const [orderMessage, setOrderMessage] = useState("");
  const [deliveryMessage, setDeliveryMessage] = useState("");
  const [agencyConsent, setAgencyConsent] = useState(false);
  const [privacyConsent, setPrivacyConsent] = useState(false);

  useEffect(() => {
    let active = true;
    void getAgencyOrderCapability()
      .then((result) => {
        if (!active) return;
        setCapability(result.capability);
      })
      .catch(() => {
        if (active) setCapability(undefined);
      });
    return () => { active = false; };
  }, []);

  const sandboxAvailable = capability?.paymentRails.paypalSandbox.state === "READY";
  const liveAvailable = capability?.paymentRails.paypalLive.state === "READY";
  const paypalAvailable = paymentMethod === "PAYPAL_LIVE" ? liveAvailable :
    paymentMethod === "PAYPAL_SANDBOX" ? sandboxAvailable : false;
  const paypalLive = paymentMethod === "PAYPAL_LIVE";
  const [deliverySelections, setDeliverySelections] = useState<Record<string, string>>({});
  const [shippingAddress, setShippingAddress] = useState<OrderSheetShippingAddress>(emptyShippingAddress);
  const [shippingErrors, setShippingErrors] = useState<ShippingAddressErrors>({});
  const [saveAsDefault, setSaveAsDefault] = useState(false);
  const [shippingFeedback, setShippingFeedback] = useState<{ tone: "neutral" | "danger"; message: string }>();
  // 세션 생성 시 서버가 복호화해 내려준 계정 기본 배송지 초기값. 사용자가 폼을
  // 바꾼 뒤에도 이 버튼 한 번으로 되돌릴 수 있게 보관한다.
  const [accountDefaultAddress, setAccountDefaultAddress] = useState<OrderSheetShippingAddress>();
  // 주문서는 20분 TTL로 만료된다. 만료를 화면이 먼저 감지해 raw 오류 대신
  // "새 주문서 만들기" 복구 경로를 제공한다. 재생성은 같은 creation key로
  // 다시 요청하면 서버가 만료 세션의 key를 회수하고 새 주문서를 발급한다.
  const [expired, setExpired] = useState(false);
  const [reloadVersion, setReloadVersion] = useState(0);

  const createKey = useMemo(
    () => stableKey(`vitlane:order-sheet:${curationId}:${expectedCartVersion}`),
    [curationId, expectedCartVersion],
  );

  useEffect(() => {
    let active = true;
    if (!curationId || expectedCartVersion < 0) {
      setError(l("We couldn't verify the cart version. Start checkout again from the cart.", "장바구니 버전을 확인할 수 없습니다. 장바구니에서 주문하기를 다시 눌러 주세요."));
      return;
    }
    setWorking(true);
    setError(undefined);
    setConfirmation(undefined);
    setExpired(false);
    void createOrderSheet(curationId, expectedCartVersion, createKey)
      .then(({ orderSheet }) => {
        if (!active) return;
        if (orderSheet.state === "CONSUMED" && orderSheet.issuedAgencyOrderId) {
          navigate(`/agencyOrder/${orderSheet.issuedAgencyOrderId}/payment`, { replace: true });
          return;
        }
        setSheet(orderSheet);
        if (orderSheet.paymentSelection) setPaymentMethod(orderSheet.paymentSelection);
        setShippingAddress(normalizeShippingInput(orderSheet.shippingAddressInput));
        setDeliverySelections(initialDeliverySelections(orderSheet));
        if (orderSheet.shippingAddressSource === "ACCOUNT_DEFAULT") {
          setAccountDefaultAddress(normalizeShippingInput(orderSheet.shippingAddressInput));
        }
      })
      .catch((caught) => {
        if (!active) return;
        if (navigateConsumedOrder(caught, navigate)) return;
        setError(orderSheetError(caught, l));
      })
      .finally(() => { if (active) setWorking(false); });
    return () => { active = false; };
  }, [createKey, curationId, expectedCartVersion, l, navigate, reloadVersion]);

  useEffect(() => {
    if (!sheet?.expiresAt || sheet.state === "CONSUMED") return;
    const remaining = new Date(sheet.expiresAt).getTime() - Date.now();
    if (remaining <= 0) {
      setExpired(true);
      return;
    }
    const timer = window.setTimeout(() => setExpired(true), remaining);
    return () => window.clearTimeout(timer);
  }, [sheet?.expiresAt, sheet?.state]);

  async function submitShippingAddress(event: FormEvent) {
    event.preventDefault();
    if (!sheet || working) return;
    const formattedShippingAddress = {
      ...shippingAddress,
      phone: formatUSPhoneInput(shippingAddress.phone ?? ""),
    };
    setShippingAddress(formattedShippingAddress);
    const fieldErrors = validateShippingAddress(formattedShippingAddress, { requireUS: true });
    const summary = shippingAddressErrorSummary(fieldErrors);
    setShippingErrors(fieldErrors);
    setShippingFeedback(undefined);
    if (summary) {
      setShippingFeedback({ tone: "danger", message: summary });
      return;
    }
    setWorking(true);
    setError(undefined);
    try {
      const result = await setOrderSheetShippingAddress(sheet.id, sheet.version, formattedShippingAddress);
      setSheet(result.orderSheet);
      setShippingAddress(normalizeShippingInput(result.orderSheet.shippingAddressInput));
      setDeliverySelections(initialDeliverySelections(result.orderSheet));
      setShippingErrors({});
      if (saveAsDefault) {
        try {
          await saveDefaultShippingProfile({
            label: l("Default shipping address", "기본 배송지"),
            recipientName: formattedShippingAddress.recipientName,
            addressLine1: formattedShippingAddress.addressLine1,
            addressLine2: formattedShippingAddress.addressLine2 ?? "",
            city: formattedShippingAddress.city,
            region: formattedShippingAddress.region,
            postalCode: formattedShippingAddress.postalCode,
            country: formattedShippingAddress.country,
            phone: formattedShippingAddress.phone ?? "",
          });
          setShippingFeedback({ tone: "neutral", message: l("Shipping address confirmed for this order and saved as your account default.", "이 주문의 배송지를 확인했고 계정 기본 배송지에도 저장했습니다.") });
        } catch (caught) {
          setShippingFeedback({
            tone: "danger",
            message: l("The order-sheet address was saved, but the account default was not updated. {reason}", "주문서 배송지는 저장됐지만 계정 기본 배송지는 갱신하지 못했습니다. {reason}", { reason: messageOf(caught, l) }),
          });
        }
      } else {
        setShippingFeedback({ tone: "neutral", message: l("Shipping address confirmed for this order only. Your account default is unchanged.", "이 주문에만 사용할 배송지를 확인했습니다. 계정 기본 배송지는 변경하지 않았습니다.") });
      }
    } catch (caught) {
      if (navigateConsumedOrder(caught, navigate)) return;
      if (caught instanceof APIError) {
        const fieldReason = shippingFieldReasons(caught.reasonCode ?? "", l);
        if (fieldReason) {
          setShippingErrors({ [fieldReason.field]: fieldReason.message });
        }
      }
      setShippingFeedback({ tone: "danger", message: shippingAddressMessage(caught, l) });
    } finally {
      setWorking(false);
    }
  }

  function validateShippingField(field: ShippingAddressField) {
    const fieldErrors = validateShippingAddress(shippingAddress, { requireUS: true });
    setShippingErrors((current) => ({ ...current, [field]: fieldErrors[field] }));
  }

  function formatAndValidateShippingPhone() {
    const nextAddress = {
      ...shippingAddress,
      phone: formatUSPhoneInput(shippingAddress.phone ?? ""),
    };
    setShippingAddress(nextAddress);
    const fieldErrors = validateShippingAddress(nextAddress, { requireUS: true });
    setShippingErrors((current) => ({ ...current, phone: fieldErrors.phone }));
  }

  async function submit() {
    if (!sheet || working) return;
    if (!paymentMethod) {
      setError(l("Choose a payment method before placing the order.", "주문 전에 결제수단을 선택해 주세요."));
      return;
    }
    if (!sheet.shippingAddress?.snapshotRef) {
      setError(l("Enter a shipping address and confirm delivery options first.", "먼저 이 주문의 배송지를 입력하고 배송 옵션을 확인해 주세요."));
      return;
    }
    if (isPayPalMethod(paymentMethod) && !paypalAvailable) {
      setError(l("That payment method is temporarily unavailable. Choose another method.", "해당 결제수단은 현재 일시 중단되었습니다. 다른 결제수단을 선택해 주세요."));
      return;
    }
    if (paymentMethod === "PAYPAL_LIVE" && !liveAcknowledged) {
      setError(l("Confirm that PayPal Live uses a real USD authorization before continuing.", "계속하기 전에 PayPal Live가 실제 USD 승인을 사용한다는 점을 확인해 주세요."));
      return;
    }
    if (!agencyConsent || !privacyConsent) {
      setError(l("Confirm both procurement authorization items before placing the order.", "주문 전에 구매대행 승인 항목 두 가지를 모두 확인해 주세요."));
      return;
    }
    setWorking(true);
    setError(undefined);
    try {
      let current = sheet;
      const needsDeliverySave = deliverySelectionNeedsSave(current, deliverySelections);
      if (needsDeliverySave) {
        const selected = await selectDeliveryOptions(
          current.id,
          current.version,
          current.merchantCheckouts.flatMap((checkout) => checkout.deliveryGroups.map((group) => ({
            shopDomain: checkout.shopDomain,
            groupId: group.id,
            optionId: deliverySelections[deliveryKey(checkout.shopDomain, group.id)] ?? "",
          }))),
        );
        current = selected.orderSheet;
        setSheet(current);
        setShippingAddress(normalizeShippingInput(current.shippingAddressInput));
      }
      if (current.state !== "READY" || needsDeliverySave ||
        current.paymentSelection !== paymentMethod) {
        // 공정 표시(운영정합 5차 C1): 클릭 시점 화면이 확정 총액을 보여준
        // 경우(READY)에만 그 숫자를 기억한다. 검증 결과가 같으면 같은 클릭에서
        // 이어 발행하고, 다르거나 처음 확정되면 보여준 뒤 재확인을 받는다 —
        // 종전의 무언 중단은 "첫 클릭 실패"로 읽혔다.
        const shownPayableMinor = sheet.state === "READY"
          ? selectedPayable(sheet, paymentMethod).amountMinor
          : undefined;
        const preflight = await preflightOrderSheet(current.id, current.version, paymentMethod);
        current = preflight.orderSheet;
        setSheet(current);
        setShippingAddress(normalizeShippingInput(current.shippingAddressInput));
        if (current.state !== "READY" || !current.displayedSnapshotHash) {
          setError(blockedMessage(current, l));
          return;
        }
        if (current.customerPayableTotal.amountMinor !== shownPayableMinor) {
          setConfirmation(confirmedTotalMessage(shownPayableMinor, current.customerPayableTotal, l));
          return;
        }
      }
      const issued = await issueAgencyOrder(
        current.id,
        current.version,
        current.displayedSnapshotHash ?? "",
        stableKey(`vitlane:agency-order:${current.id}`),
        capability?.capabilityRevision ?? 0,
        {
          orderMessage,
          deliveryMessage,
          agencyConsent,
          privacyConsent,
          locale,
          copyVersion: "procurement-authorization.v1",
        },
      );
      navigate(`/agencyOrder/${issued.agencyOrder.id}/payment`);
    } catch (caught) {
      if (navigateConsumedOrder(caught, navigate)) return;
      if (caught instanceof APIError && caught.reasonCode === "ORDER_SHEET_EXPIRED") {
        setExpired(true);
      }
      if (caught instanceof APIError && caught.reasonCode === "FINAL_CHECKOUT_CHANGED" && sheet) {
        try {
          const latest = await getOrderSheet(sheet.id);
          setSheet(latest.orderSheet);
          setShippingAddress(normalizeShippingInput(latest.orderSheet.shippingAddressInput));
          setDeliverySelections(initialDeliverySelections(latest.orderSheet));
          setConfirmation(undefined);
          setError(latest.orderSheet.state === "BLOCKED"
            ? blockedMessage(latest.orderSheet, l)
            : l("The merchant changed the confirmed economic snapshot. Review the updated total and conditions, then confirm again.", "판매처가 확정된 금액 스냅샷을 변경했습니다. 갱신된 총액과 조건을 확인한 뒤 다시 확정해 주세요."));
          return;
        } catch {
          // Fall through to the stable generic error below.
        }
      }
      if (caught instanceof APIError && caught.reasonCode === "ORDER_SHEET_VERSION_CONFLICT" && sheet) {
        try {
          const latest = await getOrderSheet(sheet.id);
          setSheet(latest.orderSheet);
          setShippingAddress(normalizeShippingInput(latest.orderSheet.shippingAddressInput));
          setDeliverySelections(initialDeliverySelections(latest.orderSheet));
        } catch {
          // 최신화 실패 시 아래 안내만 남긴다. 다음 시도에서 다시 불러온다.
        }
      }
      setError(orderSheetError(caught, l));
    } finally {
      setWorking(false);
    }
  }

  return (
    <main className="agency-order-page">
      <Button className="agency-order-back" emphasis="quiet" type="button" onClick={() => navigate(-1)}>
        <ArrowLeft size={16} aria-hidden="true" /> {l("Back to cart", "장바구니로 돌아가기")}
      </Button>
      <header className="agency-order-hero">
        <span>{l("Order sheet", "주문서")}</span>
        <h1>{l("Review your order one last time.", "주문 내용을 마지막으로 확인해 주세요.")}</h1>
        <p>{l("Before payment, merchants revalidate the address, shipping, tax, and total. tVITUSD does not move until the order is issued.", "결제 전에 판매처에서 배송지·배송비·세금·총액을 다시 검증합니다. 주문이 발행되기 전에는 tVITUSD가 이동하지 않습니다.")}</p>
      </header>

      {working && !sheet ? <section className="agency-order-card">{l("Preparing the order sheet…", "주문서를 준비하고 있습니다…")}</section> : null}
      {error && !sheet ? (
        <section className="agency-order-card agency-order-error" role="alert">
          <CircleAlert aria-hidden="true" />
          <div><strong>{l("We couldn't prepare the order sheet.", "주문서를 준비하지 못했습니다.")}</strong><p>{error}</p></div>
          <Button emphasis="quiet" type="button" onClick={() => navigate(-1)}>{l("Back to cart", "장바구니로 돌아가기")}</Button>
        </section>
      ) : null}
      {sheet && expired ? (
        <section className="agency-order-card agency-order-expired" role="alert">
          <CircleAlert aria-hidden="true" />
          <div>
            <strong>{l("The order sheet expired.", "주문서가 만료되었습니다.")}</strong>
            <p>{l("Order sheets remain valid for 20 minutes to keep prices and shipping current. You can create a new one from the same cart; payment has not started.", "가격·배송 정보의 신선도를 위해 주문서는 20분 동안만 유효합니다. 같은 장바구니 내용으로 새 주문서를 만들 수 있고, 결제는 시작되지 않았습니다.")}</p>
          </div>
          <Button emphasis="primary" type="button" busy={working} onClick={() => setReloadVersion((current) => current + 1)}>{l("Create new order sheet", "새 주문서 만들기")}</Button>
        </section>
      ) : null}
      {sheet ? (
        <div className="agency-order-grid">
          <div className="agency-order-stack">
            {sheet.state === "BLOCKED" ? <section className="agency-order-card agency-order-error" role="alert">
              <CircleAlert aria-hidden="true" />
              <div>
                <strong>{sheet.blockReason === "CHECKOUT_CUSTOMER_CORRECTION_REQUIRED"
                  ? l("Update the order information to continue.", "주문 정보를 수정하면 계속할 수 있습니다.")
                  : l("This order cannot continue as currently configured.", "현재 구성으로는 주문을 계속할 수 없습니다.")}</strong>
                <p>{blockedMessage(sheet, l)}</p>
              </div>
              {sheet.blockReason !== "CHECKOUT_CUSTOMER_CORRECTION_REQUIRED" ? <Button emphasis="quiet" type="button" onClick={() => navigate(-1)}>{l("Back to cart", "장바구니로 돌아가기")}</Button> : null}
            </section> : null}
            <section className="agency-order-card">
              <div className="agency-order-card__title"><PackageCheck aria-hidden="true" /><div><span>{l("Products", "상품")}</span><h2>{l("{count} order items", "{count}개 주문 항목", { count: sheet.lines.length })}</h2></div></div>
              <ul className="agency-order-lines">
                {sheet.lines.map((line) => (
                  <li key={line.lineId}>
                    <div><strong>{line.productTitle}</strong><span>{line.variantTitle} · {line.selectedOptions.join(" · ") || l("Default option", "기본 옵션")}</span><small>{line.shopDomain} · {l("Quantity {count}", "수량 {count}", { count: line.quantity })}</small></div>
                    <strong>{formatMoney(line.lineSubtotal)}</strong>
                  </li>
                ))}
              </ul>
            </section>

            <section className="agency-order-card">
              <div className="agency-order-card__title"><MapPin aria-hidden="true" /><div><span>{l("Shipping address", "배송지")}</span><h2>{l("Shipping address for this order", "이 주문의 배송지")}</h2></div></div>
              {accountDefaultAddress && shippingAddressEditable(sheet) ? (
                <Button
                  className="is-wide"
                  emphasis="secondary"
                  size="compact"
                  type="button"
                  disabled={working}
                  onClick={() => {
                    setShippingAddress(accountDefaultAddress);
                    setShippingErrors({});
                    setShippingFeedback({ tone: "neutral", message: l("Your default shipping address was restored to the form. Review and save it below.", "계정 기본 배송지를 폼에 다시 적용했습니다. 아래에서 확인 후 저장해 주세요.") });
                  }}
                >
                  {l("Use default shipping address", "기본 배송지 사용하기")}
                </Button>
              ) : null}
              <form className="agency-order-shipping-form" onSubmit={(event) => void submitShippingAddress(event)} noValidate>
                {shippingFeedback ? <Notice className="is-wide" tone={shippingFeedback.tone} title={shippingFeedback.tone === "danger" ? l("Review the shipping address", "배송지를 확인해 주세요") : l("Shipping address applied", "배송지 반영 완료")}>{shippingFeedback.message}</Notice> : null}
                <Field id="order-shipping-recipient" label={l("Recipient", "수령인")} required error={shippingErrors.recipientName}><Input required autoComplete="name" disabled={!shippingAddressEditable(sheet) || working} value={shippingAddress.recipientName} onChange={(event) => setShippingAddress({ ...shippingAddress, recipientName: event.target.value })} /></Field>
                <Field className="is-wide" id="order-shipping-line-1" label={l("Address line 1", "주소 1")} required error={shippingErrors.addressLine1}><Input required autoComplete="address-line1" disabled={!shippingAddressEditable(sheet) || working} value={shippingAddress.addressLine1} onChange={(event) => setShippingAddress({ ...shippingAddress, addressLine1: event.target.value })} /></Field>
                <Field className="is-wide" id="order-shipping-line-2" label={l("Address line 2", "주소 2")} hint={l("Optional", "선택")} error={shippingErrors.addressLine2}><Input autoComplete="address-line2" disabled={!shippingAddressEditable(sheet) || working} value={shippingAddress.addressLine2 ?? ""} onChange={(event) => setShippingAddress({ ...shippingAddress, addressLine2: event.target.value })} /></Field>
                <Field id="order-shipping-city" label={l("City", "도시")} required error={shippingErrors.city}><Input required autoComplete="address-level2" disabled={!shippingAddressEditable(sheet) || working} value={shippingAddress.city} onChange={(event) => setShippingAddress({ ...shippingAddress, city: event.target.value })} /></Field>
                <Field id="order-shipping-region" label={l("State", "주(State)")} hint={l("US state code (example: NY, CA)", "미국 주 코드 (예: NY, CA)")} required error={shippingErrors.region}><Input required autoComplete="address-level1" disabled={!shippingAddressEditable(sheet) || working} value={shippingAddress.region} onChange={(event) => setShippingAddress({ ...shippingAddress, region: event.target.value })} onBlur={() => validateShippingField("region")} /></Field>
                <Field id="order-shipping-postal" label={l("ZIP code", "우편번호")} hint={l("5-digit ZIP (example: 10012)", "ZIP 5자리 (예: 10012)")} required error={shippingErrors.postalCode}><Input required autoComplete="postal-code" disabled={!shippingAddressEditable(sheet) || working} value={shippingAddress.postalCode} onChange={(event) => setShippingAddress({ ...shippingAddress, postalCode: event.target.value })} onBlur={() => validateShippingField("postalCode")} /></Field>
                <Field id="order-shipping-country" label={l("Country", "국가")} hint={l("US shipping only", "현재 미국 배송만 지원")} required error={shippingErrors.country}><Input required disabled value="US" /></Field>
                <Field className="is-wide" id="order-shipping-phone" label={l("Phone number", "전화번호")} hint={l("Digits, spaces, hyphens, and parentheses are accepted. We format them as +1 202 555 0123.", "숫자만 입력하거나 공백·하이픈·괄호를 섞어도 됩니다. +1 202 555 0123 형식으로 정리합니다.")} required error={shippingErrors.phone}><Input required autoComplete="tel" inputMode="tel" placeholder="+1 202 555 0123" disabled={!shippingAddressEditable(sheet) || working} value={shippingAddress.phone ?? ""} onChange={(event) => setShippingAddress({ ...shippingAddress, phone: event.target.value })} onBlur={formatAndValidateShippingPhone} /></Field>
                {shippingAddressEditable(sheet) ? <>
                  <label className="agency-order-save-default is-wide"><Checkbox id="order-shipping-save-default" checked={saveAsDefault} onCheckedChange={(value) => setSaveAsDefault(value === true)} /><span>{l("Also save this as my account's default shipping address", "이 배송지를 계정의 기본 배송지로도 저장")}</span></label>
                  <Button className="is-wide" emphasis="primary" type="submit" busy={working}>{sheet.shippingAddress?.snapshotRef ? l("Update address and review delivery options", "배송지 변경하고 배송 옵션 다시 확인") : l("Review delivery options for this address", "이 배송지로 배송 옵션 확인")}</Button>
                </> : null}
              </form>
              {sheet.shippingAddress?.maskedSummary ? <p className="agency-order-address">{l("Order snapshot", "주문 snapshot")} · {sheet.shippingAddress.maskedSummary}</p> : null}
              <small>{l("Your full address is used only for this OrderSheet's encrypted snapshot and the merchant's shipping and tax calculations.", "입력한 전체 주소는 이 OrderSheet의 암호화 snapshot과 판매처 배송비·세금 계산에만 사용됩니다.")}</small>
            </section>

            {sheet.shippingAddress?.snapshotRef && sheet.merchantCheckouts.length > 0 ? <section className="agency-order-card">
              <div className="agency-order-card__title"><PackageCheck aria-hidden="true" /><div><span>{l("Delivery method", "배송 방법")}</span><h2>{l("Delivery options by shop", "Shop별 배송 옵션")}</h2></div></div>
              <div className="agency-order-delivery-groups">
                {sheet.merchantCheckouts.flatMap((checkout) => checkout.deliveryGroups.map((group, index) => (
                  <div key={`${checkout.shopDomain}:${group.id}`}>
                    <strong>{checkout.shopDomain}{checkout.deliveryGroups.length > 1 ? l(" · Delivery group {number}", " · 배송 그룹 {number}", { number: index + 1 }) : ""}</strong>
                    <RadioGroup value={deliverySelections[deliveryKey(checkout.shopDomain, group.id)] ?? ""} onValueChange={(value) => setDeliverySelections((current) => ({ ...current, [deliveryKey(checkout.shopDomain, group.id)]: value }))}>
                      {group.options.map((option) => <label key={option.id} className={deliverySelections[deliveryKey(checkout.shopDomain, group.id)] === option.id ? "is-selected" : ""}><RadioGroupItem value={option.id} aria-label={option.title} /><span><strong>{option.title}</strong><small>{formatMoney({ amountMinor: option.amountMinor, currency: option.currency })}</small></span></label>)}
                    </RadioGroup>
                  </div>
                )))}
              </div>
              <small>{l("Your selections are saved and used as-is for the final merchant quote before payment.", "선택은 저장되어 결제 직전 판매처 견적 확인에 그대로 사용됩니다.")}</small>
            </section> : null}

            {sheet.shippingAddress?.snapshotRef ? <section className="agency-order-card">
              <div className="agency-order-card__title"><CreditCard aria-hidden="true" /><div><span>{l("Payment method", "결제수단")}</span><h2>{l("Choose how you want to pay.", "결제 방법을 먼저 선택합니다.")}</h2></div></div>
              <RadioGroup className="agency-order-payment-options" value={paymentMethod} onValueChange={(value) => { setPaymentMethod(value as PaymentMethod); setLiveAcknowledged(false); }}>
                <label className={paymentMethod === "PAYPAL_LIVE" ? "is-selected" : ""}>
                  <RadioGroupItem value="PAYPAL_LIVE" disabled={!liveAvailable} aria-label={l("PayPal Live", "PayPal Live")} />
                  <span><strong>{l("PayPal Live", "PayPal Live")}</strong><small>{liveAvailable
                    ? l("PayPal Live · Real USD authorization. Each Shop amount is captured when its purchase begins.", "PayPal Live · 실제 USD 승인. 각 Shop 구매를 시작할 때 해당 금액만 청구됩니다.")
                    : l("Pilot temporarily paused · No new Live order or charge can start", "Pilot 일시 중단 · 새 Live 주문이나 청구를 시작할 수 없습니다")}</small></span>
                </label>
                <label className={paymentMethod === "PAYPAL_SANDBOX" ? "is-selected" : ""}>
                  <RadioGroupItem value="PAYPAL_SANDBOX" disabled={!sandboxAvailable} aria-label={l("PayPal Sandbox", "PayPal Sandbox")} />
                  <span><strong>{l("PayPal Sandbox", "PayPal Sandbox")}</strong><small>{l("PayPal Sandbox · Test only · No real charge or merchant order.", "PayPal Sandbox · 테스트 전용 · 실제 청구나 판매처 주문이 발생하지 않습니다.")}</small></span>
                </label>
                <label className={paymentMethod === "TVITUSD" ? "is-selected" : ""}>
                  <RadioGroupItem value="TVITUSD" aria-label={l("tVITUSD", "tVITUSD")} />
                  <span><strong>{l("tVITUSD", "tVITUSD")}</strong><small>{l("GIWA TEST · Valueless test asset · 1% fee", "GIWA TEST · 가치 없는 테스트 자산 · 수수료 1%")}</small></span>
                </label>
              </RadioGroup>
              {isPayPalMethod(paymentMethod) ? <Notice tone="neutral" title={l("One PayPal authorization · Shop-by-Shop capture", "PayPal 승인 1건 · Shop별 수납")}>{l(
                "At PayPal, you approve one authorization for the full displayed order total. Vitlane captures only the amount allocated to a Shop when Procurement for that Merchant Order starts. If you cancel an uncaptured Merchant Order, it is not captured; PayPal may still show its unused authorized balance temporarily until Vitlane closes the authorization or the hold expires.",
                "PayPal에서는 표시된 주문 총액 전체에 대해 승인 1건을 진행합니다. Vitlane은 각 Merchant Order의 조달이 시작될 때 해당 Shop에 배분된 금액만 수납합니다. 아직 수납되지 않은 Merchant Order를 취소하면 그 금액은 수납하지 않지만, Vitlane이 승인을 종료하거나 승인 보류가 만료될 때까지 PayPal에 미사용 승인 잔액이 일시적으로 표시될 수 있습니다.",
              )}</Notice> : null}
              {isPayPalMethod(paymentMethod) && paypalLive ? <Notice tone="danger" title={l("PayPal LIVE · real money", "PayPal LIVE · 실제 금액")}>{l("This order uses PayPal LIVE. Approval places a real-money authorization for the displayed USD total, and each Shop allocation can be captured when its Procurement starts.", "이 주문은 PayPal LIVE를 사용합니다. 승인하면 표시된 USD 총액에 실제 금액 승인이 설정되고, 각 Shop 배분액은 해당 조달 시작 시 수납될 수 있습니다.")}</Notice> : null}
              {paymentMethod === "PAYPAL_LIVE" ? <label className="agency-order-procurement-authorization__consent"><Checkbox className="agency-order-procurement-authorization__checkbox" id="paypal-live-acknowledgement" checked={liveAcknowledged} onCheckedChange={(value) => setLiveAcknowledged(value === true)} /><span>{l("I understand that PayPal Live creates a real USD authorization and Shop-level charges.", "PayPal Live에서 실제 USD 승인과 Shop별 청구가 발생함을 이해했습니다.")}</span></label> : null}
            </section> : null}

            {sheet.shippingAddress?.snapshotRef ? <section className="agency-order-card agency-order-procurement-authorization">
              <div className="agency-order-card__title"><ShieldCheck aria-hidden="true" /><div><span>{l("Procurement authorization", "구매대행 승인")}</span><h2>{l("Approve the exact scope for manual purchasing.", "수동 구매의 정확한 범위를 승인해 주세요.")}</h2></div></div>
              {sheet.merchantCheckouts.map((checkout) => <div className="agency-order-procurement-authorization__shop" key={checkout.shopDomain}>
                <h3>{checkout.shopDomain}</h3>
                {(checkout.providerNotices ?? []).filter((notice) => notice.audience === "CUSTOMER_AND_OPERATOR").map((notice, index) => <Notice
                  key={`${notice.code ?? "notice"}:${index}`}
                  tone={notice.presentation === "DISCLOSURE" ? "warning" : "neutral"}
                  title={notice.presentation === "DISCLOSURE" ? l("Merchant disclosure", "판매처 고지") : l("Merchant notice", "판매처 안내")}
                >{notice.text || l("The merchant supplied an informational notice for this checkout.", "판매처가 이 checkout에 관한 안내를 제공했습니다.")}</Notice>)}
                {(checkout.policyLinks ?? []).length > 0 ? <p>{(checkout.policyLinks ?? []).map((link, index) => <span key={`${link.url}:${index}`}><a href={link.url} rel="noreferrer" target="_blank">{link.label || link.kind}</a>{index < (checkout.policyLinks?.length ?? 0) - 1 ? " · " : ""}</span>)}</p> : null}
              </div>)}
              <label>{l("Order note for the operator (optional)", "운영자 주문 메모(선택)")}<Textarea maxLength={500} value={orderMessage} onChange={(event) => setOrderMessage(event.target.value)} /></label>
              <label>{l("Delivery note for the operator (optional)", "운영자 배송 메모(선택)")}<Textarea maxLength={500} value={deliveryMessage} onChange={(event) => setDeliveryMessage(event.target.value)} /></label>
              <label className="agency-order-procurement-authorization__consent"><Checkbox className="agency-order-procurement-authorization__checkbox" checked={agencyConsent} onCheckedChange={(checked) => setAgencyConsent(checked === true)} /> <span><strong>{l("Required approval", "필수 승인")}</strong>{l("I authorize Vitlane's operator to purchase the listed products within the displayed amount and conditions.", "표시된 금액과 조건 범위에서 Vitlane 운영자가 해당 상품을 구매하도록 승인합니다.")}</span></label>
              <label className="agency-order-procurement-authorization__consent"><Checkbox className="agency-order-procurement-authorization__checkbox" checked={privacyConsent} onCheckedChange={(checked) => setPrivacyConsent(checked === true)} /> <span><strong>{l("Required approval", "필수 승인")}</strong>{l("I authorize sharing the shipping details needed to place these merchant orders. Vitlane will never ask for a merchant password or MFA code.", "merchant 주문에 필요한 배송정보 전달을 승인합니다. Vitlane은 merchant 비밀번호나 MFA 코드를 요구하지 않습니다.")}</span></label>
            </section> : null}
          </div>

          <aside className="agency-order-summary">
            <div><span>{l("Estimated product total", "상품 예상액")}</span><strong>{previewSubtotal(sheet)}</strong></div>
            <div><span>{l("Shipping and tax", "배송비·세금")}</span><strong>{sheet.state === "READY" ? l("Verified", "검증됨") : l("Calculated before payment", "결제 직전 계산")}</strong></div>
            {sheet.state === "READY" ? <div><span>{l("Shop total", "Shop 합계")}</span><strong>{formatMoney(sheet.passThroughTotal)}</strong></div> : null}
            <div><span>{isPayPalMethod(paymentMethod) ? l("Vitlane fee for PayPal (5.4% + USD 0.30 per Merchant Order)", "PayPal용 Vitlane 수수료 (Merchant Order마다 5.4% + USD 0.30)") : l("tVITUSD fee (1%)", "tVITUSD 수수료 (1%)")}</span><strong>{formatMoney(selectedFee(sheet, paymentMethod, deliverySelections))}</strong></div>
            <div className="agency-order-summary__total"><span>{isPayPalMethod(paymentMethod) && !paypalAvailable ? l("Estimated payment (coming soon)", "예상 결제액 (준비 중)") : l("Payment total", "결제 예정액")}</span><strong>{sheet.state === "READY" ? formatMoney(selectedPayable(sheet, paymentMethod)) : l("Confirmed after merchant validation", "판매처 검증 후 확정")}</strong></div>
            {sheet.state !== "READY" ? <small>{l("The fee is estimated from the current products and selected delivery. It is confirmed after the merchant validates tax and total.", "수수료는 현재 상품·선택 배송비 기준이며, 판매처 세금·총액 검증 후 확정됩니다.")}</small> : null}
            <p><ShieldCheck size={16} aria-hidden="true" /> {l("The order is issued only when the final merchant check confirms stable tax and totals with no positive duty, import, or customs charge.", "최종 판매처 확인에서 세금·총액이 안정되고 관세·수입·통관 비용이 양수로 부과되지 않을 때만 주문이 발행됩니다.")}</p>
            <p className="agency-order-refund-policy">
              {l("Cancellation and refund scope: The current Live-readiness release handles only a whole Merchant Order (one Shop group). Products, units, or lines inside the same Merchant Order cannot be cancelled or refunded separately. Before its merchant purchase starts, you may cancel the whole Merchant Order and receive its allocated product, shipping, tax, and Vitlane fee in full. After purchasing starts, only eligible reasons such as non-delivery, an incorrect or defective item, or procurement failure receive the same full Merchant Order amount; change-of-mind returns are not supported. Other Merchant Orders in the same AgencyOrder continue independently. Placing the order means you agree to this scope.", "취소·환불 단위: 현재 Live 직전 버전에서는 Merchant Order 전체(Shop 그룹 하나)만 처리합니다. 같은 Merchant Order 안의 상품·unit·line을 따로 취소하거나 환불할 수 없습니다. merchant 구매 시작 전에는 Merchant Order 전체를 취소할 수 있고, 그 Merchant Order에 배분된 상품·배송비·세금·Vitlane 수수료를 모두 반환합니다. 구매 시작 후에는 미도착·오배송·하자·조달 실패 등 정당한 사유에 한해 같은 Merchant Order 전액을 환불하며, 단순 변심 반품은 지원하지 않습니다. 같은 AgencyOrder의 다른 Merchant Order는 독립적으로 계속됩니다. 주문 버튼을 누르면 이 범위에 동의한 것으로 처리됩니다.")}
            </p>
            {hasOperatorLaterQuote(sheet) ? (
              <p className="agency-order-review-note"><CircleAlert size={16} aria-hidden="true" /> {l("The amount below is confirmed. One or more Shops also returned read-only follow-up information that the operator will inspect during Procurement.", "아래 금액은 확정되었습니다. 한 곳 이상의 Shop이 읽기 전용 후속 정보도 반환했으며, 운영자가 Procurement 중 확인합니다.")}</p>
            ) : null}
            {confirmation ? <Notice tone="neutral" title={l("Merchant total confirmed", "판매처 금액 확인 완료")}>{confirmation}</Notice> : null}
            {error ? <div className="agency-order-error" role="alert"><CircleAlert aria-hidden="true" /><span>{error}</span></div> : null}
            <Button emphasis={sheet.shippingAddress?.snapshotRef && !expired ? "primary" : "quiet"} busy={working} disabled={expired || working || !paymentMethod || !sheet.shippingAddress?.snapshotRef || sheet.state === "BLOCKED" || !allDeliverySelected(sheet, deliverySelections) || !agencyConsent || !privacyConsent || (paymentMethod === "PAYPAL_LIVE" && !liveAcknowledged)} onClick={() => void submit()}>
              {!sheet.shippingAddress?.snapshotRef ? l("Confirm the shipping address first", "배송지를 먼저 확인해 주세요") : confirmation ? l("Confirm order at {amount}", "{amount}로 주문 확정", { amount: formatMoney(selectedPayable(sheet, paymentMethod)) }) : isPayPalMethod(paymentMethod) ? (paypalAvailable ? (sheet.state === "READY" && sheet.paymentSelection === paymentMethod ? l("Create final order and pay with PayPal", "확정 주문서 작성하고 PayPal로 결제") : l("Place order", "주문하기")) : l("Order with PayPal", "PayPal로 주문하기")) : sheet.state === "READY" && sheet.paymentSelection === "TVITUSD" ? l("Create final order and pay with tVITUSD", "확정 주문서 작성하고 tVITUSD로 결제") : l("Place order", "주문하기")}
            </Button>
            <small>{sheet.state === "READY" ? l("When clicked, we compare the merchant's final amount once more, then atomically create the AgencyOrder and payment instruction.", "클릭 시 판매처 최종 금액을 한 번 더 비교한 뒤 AgencyOrder와 기존 결제 지시서를 원자적으로 생성합니다.") : l("First, the merchant confirms shipping, tax, total, and whether manual-purchase handoff is available.", "먼저 판매처에서 배송비·세금·총액과 수동 구매 인계 가능성을 확인합니다.")}</small>
          </aside>
        </div>
      ) : null}

    </main>
  );
}

function stableKey(storageKey: string) {
  const existing = window.sessionStorage.getItem(storageKey);
  if (existing) return existing;
  const created = randomUUID();
  window.sessionStorage.setItem(storageKey, created);
  return created;
}
function isPayPalMethod(method: PaymentMethod | ""): method is "PAYPAL_SANDBOX" | "PAYPAL_LIVE" {
  return method === "PAYPAL_SANDBOX" || method === "PAYPAL_LIVE";
}
function formatMoney(money?: { amountMinor: number; currency: string }) { return money ? new Intl.NumberFormat("en-US", { style: "currency", currency: money.currency }).format(money.amountMinor / 100) : "$0.00"; }
function previewSubtotal(sheet: OrderSheet) { return formatMoney({ amountMinor: sheet.lines.reduce((sum, line) => sum + line.lineSubtotal.amountMinor, 0), currency: "USD" }); }
function selectedFee(sheet: OrderSheet, paymentMethod: PaymentMethod | "", selections: Record<string, string>) {
  // 선택한 rail로 preflight를 마쳤으면 서버 계산 수수료가 권위다.
  if (sheet.state === "READY" && sheet.paymentSelection === paymentMethod) {
    return sheet.agencyFee;
  }
  const amountMinor = isPayPalMethod(paymentMethod)
    ? paypalMerchantOrderFeeMinor(sheet, selections, sheet.state === "READY")
    : percentageFee(sheet.state === "READY" ? sheet.passThroughTotal.amountMinor : previewPassThroughMinor(sheet, selections), 1);
  return { amountMinor, currency: "USD" };
}
function selectedPayable(sheet: OrderSheet, paymentMethod: PaymentMethod | "") {
  if (sheet.paymentSelection === paymentMethod) {
    return sheet.customerPayableTotal;
  }
  const fee = isPayPalMethod(paymentMethod)
    ? paypalMerchantOrderFeeMinor(sheet, {}, true)
    : percentageFee(sheet.passThroughTotal.amountMinor, 1);
  return { amountMinor: sheet.passThroughTotal.amountMinor + fee, currency: "USD" };
}
function percentageFee(amountMinor: number, percent: number) { return Math.floor((amountMinor * percent + 99) / 100); }
function paypalMerchantOrderFeeMinor(sheet: OrderSheet, selections: Record<string, string>, authoritative: boolean) {
  if (sheet.merchantCheckouts.length === 0) {
    const passThroughMinor = authoritative
      ? sheet.passThroughTotal.amountMinor
      : previewPassThroughMinor(sheet, selections);
    return paypalSingleMerchantOrderFeeMinor(passThroughMinor);
  }
  return sheet.merchantCheckouts.reduce((sum, checkout) => {
    const passThroughMinor = authoritative
      ? checkout.authoritativeTotal.amountMinor
      : previewCheckoutPassThroughMinor(sheet, checkout.shopDomain, checkout.deliveryGroups, selections);
    return sum + paypalSingleMerchantOrderFeeMinor(passThroughMinor);
  }, 0);
}
function paypalSingleMerchantOrderFeeMinor(passThroughMinor: number) {
  return Math.floor((passThroughMinor * 540 + 9_999) / 10_000) + 30;
}
function previewCheckoutPassThroughMinor(
  sheet: OrderSheet,
  shopDomain: string,
  deliveryGroups: OrderSheet["merchantCheckouts"][number]["deliveryGroups"],
  selections: Record<string, string>,
) {
  const groupLineRefs = new Set(deliveryGroups.flatMap((group) => group.lineRefs));
  const merchandise = sheet.lines.reduce((sum, line) => {
    const belongsToCheckout = groupLineRefs.size > 0
      ? groupLineRefs.has(line.lineId)
      : line.shopDomain === shopDomain;
    return belongsToCheckout ? sum + line.lineSubtotal.amountMinor : sum;
  }, 0);
  const shipping = deliveryGroups.reduce((sum, group) => {
    const selected = selections[deliveryKey(shopDomain, group.id)] ?? group.selectedOptionRef;
    return sum + (group.options.find((option) => option.id === selected)?.amountMinor ?? 0);
  }, 0);
  return merchandise + shipping;
}
function normalizeShippingInput(address?: OrderSheetShippingAddress): OrderSheetShippingAddress {
  return {
    ...emptyShippingAddress,
    ...(address ?? {}),
    country: "US",
  };
}
function shippingAddressEditable(sheet: OrderSheet) {
  return (!sheet.paymentSelection && (sheet.state === "EDITING" || sheet.state === "DELIVERY_SELECTION_REQUIRED")) ||
    (sheet.state === "BLOCKED" && sheet.blockReason === "CHECKOUT_CUSTOMER_CORRECTION_REQUIRED");
}
function shippingFieldReasons(code: string, l: Localize): { field: ShippingAddressField; message: string } | undefined {
  const reasons: Record<string, { field: ShippingAddressField; message: string }> = {
    SHIPPING_PHONE_US_FORMAT: { field: "phone", message: l("Enter a US phone number in +1 format (for example, +1 202 555 0123).", "미국 전화번호를 +1 형식으로 입력해 주세요. (예: +1 202 555 0123)") },
    SHIPPING_POSTAL_US_FORMAT: { field: "postalCode", message: l("Enter a 5-digit US ZIP or ZIP+4 (for example, 10012).", "미국 ZIP 5자리 또는 ZIP+4 형식으로 입력해 주세요. (예: 10012)") },
    SHIPPING_REGION_US_FORMAT: { field: "region", message: l("Enter a 2-letter US state code (for example, NY or CA).", "미국 주 코드 두 글자로 입력해 주세요. (예: NY, CA)") },
  };
  return reasons[code];
}

function shippingAddressMessage(caught: unknown, l: Localize) {
  if (caught instanceof APIError) {
    const fieldReason = shippingFieldReasons(caught.reasonCode ?? "", l);
    if (fieldReason) return fieldReason.message;
    if (caught.reasonCode === "SHIPPING_ADDRESS_US_ONLY" ||
      caught.reasonCode === "SHIPPING_ADDRESS_FIELDS_INVALID" ||
      caught.code === "SHIPPING_ADDRESS_INVALID") {
      return l("Review the recipient, address, city, state, ZIP code, and phone number required for US shipping.", "미국 배송에 필요한 수령인·주소·도시·주·우편번호·전화번호를 확인해 주세요.");
    }
    if (caught.reasonCode === "ORDER_SHEET_VERSION_CONFLICT") {
      return l("The order sheet changed in another window. Refresh this page, then review the address again.", "다른 화면에서 주문서가 변경되었습니다. 페이지를 새로고침한 뒤 주소를 다시 확인해 주세요.");
    }
  }
  return messageOf(caught, l);
}
function messageOf(caught: unknown, l: Localize) {
  if (caught instanceof APIError) {
    return l("We couldn't process the request. ({code})", "요청을 처리하지 못했습니다. ({code})", { code: caught.reasonCode ?? caught.code });
  }
  return l("We couldn't process the request.", "요청을 처리하지 못했습니다.");
}
function previewPassThroughMinor(sheet: OrderSheet, selections: Record<string, string>) {
  const merchandise = sheet.lines.reduce((sum, line) => sum + line.lineSubtotal.amountMinor, 0);
  const shipping = sheet.merchantCheckouts.reduce((shopSum, checkout) => shopSum + checkout.deliveryGroups.reduce((groupSum, group) => {
    const selected = selections[deliveryKey(checkout.shopDomain, group.id)];
    return groupSum + (group.options.find((option) => option.id === selected)?.amountMinor ?? 0);
  }, 0), 0);
  return merchandise + shipping;
}
function deliveryKey(shopDomain: string, groupID: string) { return `${shopDomain}\u0000${groupID}`; }
function initialDeliverySelections(sheet: OrderSheet) { const result: Record<string, string> = {}; for (const checkout of sheet.merchantCheckouts) for (const group of checkout.deliveryGroups) if (group.selectedOptionRef) result[deliveryKey(checkout.shopDomain, group.id)] = group.selectedOptionRef; return result; }
function allDeliverySelected(sheet: OrderSheet, selections: Record<string, string>) { return sheet.merchantCheckouts.length > 0 && sheet.merchantCheckouts.every((checkout) => checkout.deliveryGroups.length > 0 && checkout.deliveryGroups.every((group) => group.options.some((option) => option.id === selections[deliveryKey(checkout.shopDomain, group.id)]))); }
function deliverySelectionNeedsSave(sheet: OrderSheet, selections: Record<string, string>) { return sheet.state === "DELIVERY_SELECTION_REQUIRED" || sheet.merchantCheckouts.some((checkout) => checkout.deliveryGroups.some((group) => group.selectedOptionRef !== selections[deliveryKey(checkout.shopDomain, group.id)])); }
// 검증은 성공했지만 확정 총액이 화면이 보여주던 것과 다르면(또는 처음
// 확정되면) 발행을 멈추고 이유를 말한다 — 발행은 사용자가 본 금액으로만
// 이뤄진다(공정 표시). 총액이 이미 화면과 같으면 이 안내 없이 같은 클릭에서
// 발행된다.
function confirmedTotalMessage(shownMinor: number | undefined, payable: { amountMinor: number; currency: string }, l: Localize) {
  if (shownMinor === undefined) {
    return l("Merchant validation is complete — the payment total is {total}. Review the amount, then click the button once more to confirm your order.", "판매처 검증이 끝났습니다 — 결제 예정액이 {total}로 확정되었습니다. 금액을 확인한 뒤 버튼을 한 번 더 눌러 주문을 확정해 주세요.", { total: formatMoney(payable) });
  }
  return l("Merchant revalidation changed the payment total from {previous} to {next}. Review the amount, then click the button once more to confirm your order.", "판매처 재검증으로 결제 예정액이 {previous}에서 {next}로 바뀌었습니다. 금액을 확인한 뒤 버튼을 한 번 더 눌러 주문을 확정해 주세요.", {
    previous: formatMoney({ amountMinor: shownMinor, currency: payable.currency }),
    next: formatMoney(payable),
  });
}

function blockedMessage(sheet: OrderSheet, l: Localize) {
  const messages: Record<string, string> = {
    CHECKOUT_CUSTOMER_CORRECTION_REQUIRED: l("The merchant could not use the current shipping information. Correct the highlighted shipping fields and apply the address again. The order and payment have not started.", "판매처가 현재 배송정보를 사용할 수 없습니다. 강조된 배송정보를 수정한 뒤 주소를 다시 적용해 주세요. 주문과 결제는 시작되지 않았습니다."),
    CHECKOUT_IMPOSSIBLE: l("A merchant reported that this order cannot be fulfilled, such as an unavailable or out-of-stock item. Return to the cart and remove or replace the affected Shop's product. Payment has not started.", "판매처가 품절·판매 불가 등으로 이 주문을 이행할 수 없다고 알렸습니다. 장바구니로 돌아가 해당 Shop 상품을 제거하거나 교체해 주세요. 결제는 시작되지 않았습니다."),
    CHECKOUT_QUOTE_UNSAFE: l("We couldn't confirm the merchant's shipping, tax, and total as a safe final quote. The order and payment have not started. Try again later or choose another product.", "판매처의 배송비·세금·총액을 안전한 최종 견적으로 확인하지 못했습니다. 주문과 결제는 시작되지 않았습니다. 잠시 후 다시 시도하거나 다른 상품을 선택해 주세요."),
    CROSS_BORDER_OR_DUTIES: l("The merchant included a positive duty, import, or customs charge. Remove the affected product or choose another. Payment has not started.", "판매처가 관세·수입·통관 비용을 양수로 부과했습니다. 문제 상품을 빼거나 다른 상품을 선택해 주세요. 결제는 시작되지 않았습니다."),
    CHECKOUT_NOT_READY: l("The merchant couldn't confirm stable tax and totals. Try again later or choose another product.", "판매처가 안정된 세금·총액을 확인하지 못했습니다. 잠시 후 다시 시도하거나 다른 상품을 선택해 주세요."),
    FINAL_SNAPSHOT_CHANGED: l("The price or tax changed immediately before payment. Review the updated order sheet.", "결제 직전 가격 또는 세금이 바뀌었습니다. 변경된 주문서를 다시 확인해 주세요."),
  };
  const base = messages[sheet.blockReason ?? ""] ?? l("We couldn't confirm the order conditions. Payment has not started.", "주문 조건을 확정하지 못했습니다. 결제는 시작되지 않았습니다.");
  return `${base}${blockedShopSummary(sheet, l)}`;
}
// 차단 시 도메인이 보존한 checkout 관찰(ADR-0049)에서 문제 Shop과 typed code를
// 골라 원인 위치를 알려준다. free-form provider 문구는 표시하지 않는다.
function blockedShopSummary(sheet: OrderSheet, l: Localize) {
  const offending = sheet.merchantCheckouts.filter((checkout) => {
    switch (sheet.blockReason) {
      case "CHECKOUT_CUSTOMER_CORRECTION_REQUIRED":
        return checkout.procurementHandling === "CUSTOMER_CORRECTION";
      case "CHECKOUT_IMPOSSIBLE":
        return checkout.procurementHandling === "IMPOSSIBLE";
      case "CHECKOUT_QUOTE_UNSAFE":
      case "CHECKOUT_NOT_READY":
        return checkout.quoteReadiness !== "CONFIRMED";
      case "CROSS_BORDER_OR_DUTIES":
        return checkout.dutiesDisposition !== "NO_DUTY_OR_CROSS_BORDER_SIGNAL_AT_PREFLIGHT";
      default:
        return false;
    }
  });
  const labels = offending.map((checkout) => checkout.shopDomain);
  return labels.length > 0 ? l(" Affected merchant: {shops}", " 해당 판매처: {shops}", { shops: labels.join(", ") }) : "";
}
function hasOperatorLaterQuote(sheet: OrderSheet) { return sheet.merchantCheckouts.some((checkout) => checkout.procurementHandling === "OPERATOR_LATER"); }
function orderSheetError(caught: unknown, l: Localize) {
  if (!(caught instanceof APIError)) {
    return l("We couldn't prepare the order sheet. Your cart is unchanged.", "주문서를 준비하지 못했습니다. 장바구니는 그대로 유지됩니다.");
  }
  const itemTitle = caught.itemTitle?.trim() || l("The selected product", "선택한 상품");
  switch (caught.reasonCode ?? caught.code) {
    case "VARIANT_UNAVAILABLE":
      return l(
        "{itemTitle} is currently unavailable. Remove it from your cart or choose another option before trying again.",
        "현재 구매할 수 없는 상품입니다: {itemTitle}. 장바구니에서 제거하거나 다른 옵션으로 바꾼 뒤 다시 시도해 주세요.",
        { itemTitle },
      );
    case "VARIANT_NOT_RESOLVED":
      return l(
        "We couldn't confirm that {itemTitle} is still available, so it cannot be ordered right now. Remove it from your cart or choose another option.",
        "구매 가능 여부를 확인할 수 없는 상품입니다: {itemTitle}. 지금은 주문할 수 없으니 장바구니에서 제거하거나 다른 옵션으로 바꿔 주세요.",
        { itemTitle },
      );
    case "ORDER_SHEET_EXPIRED":
      return l("The order sheet expired. Use 'Create new order sheet' above to restart from the same cart.", "주문서가 만료되었습니다. 위의 '새 주문서 만들기'로 같은 장바구니에서 다시 시작해 주세요.");
    case "EXTERNAL_EFFECT_UNKNOWN":
      return l("The shop response is delayed. The order and payment have not started. Try again shortly.", "Shop 응답 확인이 지연되고 있습니다. 주문과 결제는 시작되지 않았습니다. 잠시 후 다시 시도해 주세요.");
    case "ORDER_SHEET_VERSION_CONFLICT":
      return l("The order sheet was updated. Review the latest details and try again.", "주문서 정보가 갱신되었습니다. 최신 내용을 확인한 뒤 다시 시도해 주세요.");
    case "SHOPIFY_CHECKOUT_BUYER_IDENTITY_CONTACT_METHOD_REQUIRED":
      // 판매처가 buyer 연락 수단을 요구 — 서버가 운영 연락 이메일을 싣는
      // 구성(AGENCY_ORDER_BUYER_CONTACT_EMAIL)이 비어 있을 때만 발생한다.
      return l("The merchant requires buyer contact information, so we couldn't confirm the order. Payment has not started. Try again shortly, and contact support if it continues.", "판매처가 구매자 연락 정보를 요구해 주문을 확정하지 못했습니다. 결제는 시작되지 않았습니다. 잠시 후 다시 시도하고, 계속되면 문의로 알려주세요.");
    case "SHOPIFY_UCP_RESPONSE_INVALID":
    case "SHOPIFY_CHECKOUT_RESPONSE_INVALID":
    case "PROVIDER_REJECTED":
      return l("The shop couldn't confirm the order details. Payment has not started. Review the shipping address and contact format, then try again or choose another product.", "Shop이 주문 정보를 확정하지 못했습니다. 결제는 시작되지 않았습니다. 배송지·연락처 형식을 확인한 뒤 다시 시도하거나 다른 상품을 선택해 주세요.");
    default:
      return l("We couldn't prepare the order sheet. ({code})", "주문서를 준비하지 못했습니다. ({code})", { code: caught.reasonCode ?? caught.code });
  }
}
function navigateConsumedOrder(caught: unknown, navigate: ReturnType<typeof useNavigate>) {
  if (!(caught instanceof APIError) || caught.code !== "ORDER_SHEET_ALREADY_CONSUMED" || !caught.agencyOrderId) return false;
  navigate(`/agencyOrder/${caught.agencyOrderId}/payment`, { replace: true });
  return true;
}
