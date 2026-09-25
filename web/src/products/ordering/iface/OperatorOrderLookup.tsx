import { useState, type FormEvent } from "react";
import { ChevronDown, ChevronUp, Search, SlidersHorizontal } from "lucide-react";
import { useNavigate } from "react-router";
import { APIError } from "../../../shared/api/client";
import { invariantContent, useLocale, type Localize } from "../../../shared/i18n";
import {
  Button,
  Field,
  Input,
  NativeSelect,
  NativeSelectOption,
  Notice,
} from "../../../shared/ui";
import {
  lookupOperatorOrder,
  type OrderLookupEnvironment,
  type OrderLookupIdentifierType,
} from "../infra/agencyOrderOperatorApi";

const identifierTypes: OrderLookupIdentifierType[] = [
  "AUTO",
  "AGENCY_ORDER_ID",
  "PAYMENT_ID",
  "PAYPAL_ORDER_ID",
  "PAYPAL_CAPTURE_ID",
  "PAYPAL_REFUND_ID",
  "MERCHANT_ORDER_ID",
  "MERCHANT_ORDER_REF",
  "SHIPMENT_ID",
  "TRACKING_REF",
  "GIWA_TX_HASH",
];

export function OperatorOrderLookup() {
  const { l } = useLocale();
  const navigate = useNavigate();
  const [expanded, setExpanded] = useState(false);
  const [identifierType, setIdentifierType] = useState<OrderLookupIdentifierType>("AUTO");
  const [environment, setEnvironment] = useState<OrderLookupEnvironment>("ANY");
  const [value, setValue] = useState("");
  const [shopDomain, setShopDomain] = useState("");
  const [carrier, setCarrier] = useState("");
  const [working, setWorking] = useState(false);
  const [error, setError] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!value.trim() || working) return;
    setWorking(true);
    setError("");
    try {
      const response = await lookupOperatorOrder({
        identifierType: expanded ? identifierType : "AUTO",
        environment: expanded ? environment : "ANY",
        value: value.trim(),
        shopDomain: expanded ? shopDomain.trim() || undefined : undefined,
        carrier: expanded ? carrier.trim() || undefined : undefined,
      });
      navigate(`/admin/agencyOrder/${encodeURIComponent(response.match.agencyOrderId)}`, {
        state: { matchedBy: response.match.matchedBy },
      });
    } catch (cause) {
      setError(lookupError(cause, l));
    } finally {
      setWorking(false);
    }
  }

  return (
    <section className={`operator-order-lookup${expanded ? " is-expanded" : ""}`}>
      <form aria-label={l("Exact order lookup", "정확 주문 조회")} onSubmit={submit} role="search">
        <div className="operator-order-lookup__bar">
          <Search aria-hidden="true" />
          <label className="vt-visually-hidden" htmlFor="order-lookup-value">
            {l("Exact order identifier", "정확한 주문 식별자")}
          </label>
          <Input
            autoComplete="off"
            id="order-lookup-value"
            placeholder={l("Order, PayPal, merchant, or tracking ID", "주문·PayPal·판매처·운송장 ID")}
            value={value}
            onChange={(event) => setValue(event.target.value)}
          />
          <Button
            aria-label={working ? l("Looking up…", "조회 중…") : l("Lookup", "조회")}
            disabled={!value.trim() || working}
            emphasis="primary"
            size="compact"
            type="submit"
          >
            <Search aria-hidden="true" />
            <span className="operator-order-lookup__submit-label">
              {working ? l("Looking up…", "조회 중…") : l("Lookup", "조회")}
            </span>
          </Button>
          <Button
            aria-controls="operator-order-lookup-options"
            aria-expanded={expanded}
            className="operator-order-lookup__toggle"
            emphasis="quiet"
            onClick={() => setExpanded((current) => !current)}
            size="compact"
            type="button"
          >
            <SlidersHorizontal aria-hidden="true" />
            <span className="operator-order-lookup__toggle-label">
              {expanded ? l("Collapse", "접기") : l("Detailed lookup", "상세 조회")}
            </span>
            {expanded ? <ChevronUp aria-hidden="true" /> : <ChevronDown aria-hidden="true" />}
          </Button>
        </div>

        {expanded ? <div className="operator-order-lookup__options" id="operator-order-lookup-options">
          <header>
            <div>
              <p>{l("Order investigation", "주문 조사")}</p>
              <h2>{l("Exact match controls", "정확 일치 조건")}</h2>
            </div>
            <span>{l(
              "Narrow an exact identifier by its system or stored order environment.",
              "정확한 식별자를 시스템 종류나 주문에 저장된 환경으로 한정합니다.",
            )}</span>
          </header>
          <div className="operator-order-lookup__fields">
            <Field className="operator-order-lookup__kind" id="order-lookup-kind" label={l("Identifier", "식별자 종류")}>
              <NativeSelect
                value={identifierType}
                onChange={(event) => setIdentifierType(event.target.value as OrderLookupIdentifierType)}
              >
                {identifierTypes.map((kind) => (
                  <NativeSelectOption key={kind} value={kind}>{identifierLabel(kind, l)}</NativeSelectOption>
                ))}
              </NativeSelect>
            </Field>
            <Field className="operator-order-lookup__environment" id="order-lookup-environment" label={l("Environment", "환경") }>
              <NativeSelect
                value={environment}
                onChange={(event) => setEnvironment(event.target.value as OrderLookupEnvironment)}
              >
                <NativeSelectOption value="ANY">{l("Any environment", "모든 환경")}</NativeSelectOption>
                <NativeSelectOption value="SANDBOX">{invariantContent("SANDBOX")}</NativeSelectOption>
                <NativeSelectOption value="TESTNET">{invariantContent("TESTNET")}</NativeSelectOption>
                <NativeSelectOption value="LIVE">{invariantContent("LIVE")}</NativeSelectOption>
              </NativeSelect>
            </Field>
            {(identifierType === "MERCHANT_ORDER_REF" || identifierType === "AUTO") ? (
              <Field className="operator-order-lookup__shop" id="order-lookup-shop" label={l("Shop domain (optional)", "Shop domain (선택)")}>
                <Input autoComplete="off" placeholder={invariantContent("shop.example")} value={shopDomain} onChange={(event) => setShopDomain(event.target.value)} />
              </Field>
            ) : null}
            {(identifierType === "TRACKING_REF" || identifierType === "AUTO") ? (
              <Field className="operator-order-lookup__carrier" id="order-lookup-carrier" label={l("Carrier (optional)", "운송사 (선택)")}>
                <Input autoComplete="off" placeholder={invariantContent("UPS")} value={carrier} onChange={(event) => setCarrier(event.target.value)} />
              </Field>
            ) : null}
          </div>
          <footer>{l(
            "Exact only — whitespace is trimmed; partial and fuzzy matches are never used. Lookup and detail access are audited without storing raw query values.",
            "정확 일치만 사용합니다. 앞뒤 공백만 제거하며 부분·유사 일치는 사용하지 않습니다. 조회 원문을 저장하지 않고 조회와 상세 열람을 감사합니다.",
          )}</footer>
        </div> : null}
      </form>
      {error ? <Notice announce tone="danger">{error}</Notice> : null}
    </section>
  );
}

function identifierLabel(kind: OrderLookupIdentifierType, l: Localize) {
  switch (kind) {
    case "AUTO": return l("Auto-detect exact ID", "정확 ID 자동 판별");
    case "AGENCY_ORDER_ID": return l("AgencyOrder ID", "AgencyOrder ID");
    case "PAYMENT_ID": return l("Payment ID", "Payment ID");
    case "PAYPAL_ORDER_ID": return l("PayPal Order ID", "PayPal Order ID");
    case "PAYPAL_CAPTURE_ID": return l("PayPal Capture ID", "PayPal Capture ID");
    case "PAYPAL_REFUND_ID": return l("PayPal Refund ID", "PayPal Refund ID");
    case "MERCHANT_ORDER_ID": return l("MerchantOrder ID", "MerchantOrder ID");
    case "MERCHANT_ORDER_REF": return l("Merchant order reference", "판매처 주문번호");
    case "SHIPMENT_ID": return l("Shipment ID", "Shipment ID");
    case "TRACKING_REF": return l("Tracking number", "운송장 번호");
    case "GIWA_TX_HASH": return l("GIWA transaction hash", "GIWA transaction hash");
  }
}

function lookupError(cause: unknown, l: Localize) {
  if (cause instanceof APIError) {
    if (cause.code === "ORDERING_OPERATOR_LOOKUP_NOT_FOUND") {
      return l("No order matched that exact value and environment.", "해당 값과 환경에 정확히 일치하는 주문이 없습니다.");
    }
    if (cause.code === "ORDERING_OPERATOR_LOOKUP_AMBIGUOUS") {
      return l("More than one order matched. Choose an identifier type or add the shop/carrier qualifier.", "둘 이상의 주문이 일치합니다. 식별자 종류를 고르거나 Shop·운송사 한정자를 추가하세요.");
    }
    if (cause.code === "ORDERING_OPERATOR_LOOKUP_INVALID") {
      return l("Check the identifier value and lookup options.", "식별자 값과 조회 조건을 확인하세요.");
    }
  }
  return l("The lookup could not be completed. Try again.", "조회를 완료하지 못했습니다. 다시 시도하세요.");
}
