import { processProgressLabel } from "../app/processPresentation";
import { useEffect, useState } from "react";
import { useLocale } from "../../../shared/i18n";
import { Notice } from "../../../shared/ui";
import { listProcessRequests, type ProcessReceipt } from "../infra/orderProcessApi";

export function ProcessReceiptNotice({ receipt, operator = false }: { receipt: ProcessReceipt; operator?: boolean }) {
  const { l } = useLocale();
  return <Notice announce tone={receipt.outcome === "REJECTED" ? "warning" : "neutral"}>{processProgressLabel(receipt, l, operator)}</Notice>;
}
export function OrderProcessProgress({ orderId, shops }: { orderId: string; shops: Array<{ id: string; shopDomain: string }> }) {
  const { l } = useLocale();
  const [receipts, setReceipts] = useState<ProcessReceipt[]>([]);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    let disposed = false;
    async function load() {
      try {
        const result = await listProcessRequests(orderId);
        if (!disposed) { setReceipts(result.requests); setFailed(false); }
      } catch { if (!disposed) setFailed(true); }
    }
    void load();
    const timer = window.setInterval(() => void load(), 2000);
    return () => { disposed = true; window.clearInterval(timer); };
  }, [orderId]);
  const seen = new Set<string>();
  const visible = receipts.filter((r) => {
    if (seen.has(r.flowId) || !["PURCHASE", "CANCEL", "REFUND_REQUEST", "REFUND_DECISION"].includes(r.kind)) return false;
    seen.add(r.flowId); return true;
  });
  if (!visible.length && !failed) return null;
  return <section aria-label={l("Request progress", "요청 진행 상황")}>
    <h3>{l("Request progress", "요청 진행 상황")}</h3>
    {failed ? <Notice tone="warning">{l("We couldn't refresh request progress. Processing continues; refresh this page to check again.", "요청 진행 상황을 갱신하지 못했습니다. 처리는 계속되며 페이지를 새로고침해 다시 확인할 수 있습니다.")}</Notice> : null}
    <ul>{visible.map((receipt) => <li key={receipt.requestId}>
      <strong>{shops.find((shop) => shop.id === receipt.merchantOrderId)?.shopDomain ?? l("Shop", "Shop")}</strong>
      <ProcessReceiptNotice receipt={receipt} />
    </li>)}</ul>
  </section>;
}
