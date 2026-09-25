import { ProcessRequestPending, ProcessRequestRejected, getProcessReceipt, type ProcessReceipt } from "../infra/orderProcessApi";
import { useCallback, useEffect, useState } from "react";
import { APIError } from "../../../shared/api/client";
import { useLocale } from "../../../shared/i18n";
import {
  listOperatorWorkItems,
  type OperatorExpectedUnit,
  type PaymentReconciliationItem,
  type PayPalResourceAdoptionItem,
  type OperatorRefundRequest,
  type OperatorReturn,
  type OrderAccountingProjection,
  type ProcessInterventionItem,
  type ProcurementQueueItem,
} from "../infra/agencyOrderOperatorApi";

// 통합 work surface 관찰 훅(ADR-0055 §5) — 주문 처리·예외 처리 두 페이지가
// 같은 관찰을 공유한다. 부분 실패로 절반 진실을 보이지 않는다(실패는 화면
// 전체의 오류). 상호 존재 경고(ADR-0057 2차 갭2)를 위해 한 응답의 전 kind를
// 그대로 유지한다 — 권위 게이트는 서버 actions·endpoint 409가 가진다.
export type OperatorSurface = {
  paymentReconciliations: PaymentReconciliationItem[];
  paypalResourceAdoptions: PayPalResourceAdoptionItem[];
  procurement: ProcurementQueueItem[];
  refunds: OperatorRefundRequest[];
  exceptions: OperatorExpectedUnit[];
  openReturns: OperatorReturn[];
  interventions: ProcessInterventionItem[];
  actionsByKey: Record<string, string[]>;
  // 주문별 이벤트 기반 회계 사영. 행동 게이트가 아니라 현재 카드의 자금 근거다.
  accountingByOrder: Record<string, OrderAccountingProjection>;
  error?: string;
};

const emptySurface: OperatorSurface = {
  paymentReconciliations: [], procurement: [], refunds: [], exceptions: [], openReturns: [],
  paypalResourceAdoptions: [], interventions: [], actionsByKey: {}, accountingByOrder: {},
};

function isPayPalResourceAdoption(
  item: PaymentReconciliationItem | PayPalResourceAdoptionItem,
): item is PayPalResourceAdoptionItem {
  return "reconciliationKind" in item;
}

export function useOperatorSurface() {
  const { l } = useLocale();
  const [surface, setSurface] = useState<OperatorSurface>(emptySurface);
  const [working, setWorking] = useState<string>();
  const [actionError, setActionError] = useState<string>();
  const [actionReceipt, setActionReceipt] = useState<ProcessReceipt>();
  const [receiptRefreshFailed,setReceiptRefreshFailed]=useState(false);

  const load = useCallback(async () => {
    try {
      const result = await listOperatorWorkItems();
      const actionsByKey: Record<string, string[]> = {};
      const accountingByOrder: Record<string, OrderAccountingProjection> = {};
      for (const item of result.items) {
        actionsByKey[`${item.kind}:${item.id}`] = item.actions;
        if (item.accounting) accountingByOrder[item.agencyOrderId] = item.accounting;
      }
      setSurface({
        paymentReconciliations: result.items
          .filter((item) => item.kind === "PAYMENT_RECONCILIATION")
          .map((item) => item.detail)
          .filter((item): item is PaymentReconciliationItem => !isPayPalResourceAdoption(item)),
        paypalResourceAdoptions: result.items
          .filter((item) => item.kind === "PAYMENT_RECONCILIATION")
          .map((item) => item.detail)
          .filter(isPayPalResourceAdoption),
        procurement: result.items
          .filter((item) => item.kind === "PROCUREMENT_EXECUTION")
          .map((item) => ({
            ...item.detail,
            operational: item.operational,
            assignmentState: item.assignmentState,
          })),
        refunds: result.items
          .filter((item) => item.kind === "REFUND_REVIEW")
          .map((item) => item.detail),
        exceptions: result.items
          .filter((item) => item.kind === "DELIVERY_RESOLUTION")
          .map((item) => item.detail),
        openReturns: result.items
          .filter((item) => item.kind === "RETURN_PROGRESS")
          .map((item) => item.detail),
        interventions: result.items
          .filter((item) => item.kind === "PROCESS_INTERVENTION")
          .map((item) => item.detail),
        actionsByKey,
        accountingByOrder,
      });
    } catch {
      setSurface((current) => ({
        ...current,
        error: l("We couldn't load operator work items.", "운영 작업 목록을 불러오지 못했습니다."),
      }));
    }
  }, [l]);

  useEffect(() => {
    void load();
    const timer = window.setInterval(() => void load(), 4000);
    return () => window.clearInterval(timer);
  }, [load]);

  useEffect(() => {
    if (!actionReceipt || ["COMPLETED", "REJECTED"].includes(actionReceipt.outcome)) return;
    let disposed=false;
    const timer=window.setInterval(async()=>{
      try {
        const next=await getProcessReceipt(actionReceipt,true);
        if(disposed)return;
        setActionReceipt(next);setReceiptRefreshFailed(false);
        if(["COMPLETED","REJECTED"].includes(next.outcome))await load();
      } catch {if(!disposed)setReceiptRefreshFailed(true);}
    },2000);
    return()=>{disposed=true;window.clearInterval(timer);};
  },[actionReceipt,load]);

  const run = useCallback(async (key: string, action: () => Promise<void>) => {
    setWorking(key);
    setActionError(undefined);
    setActionReceipt(undefined);setReceiptRefreshFailed(false);
    try {
      await action();
      await load();
    } catch (caught) {
      if (caught instanceof ProcessRequestPending || caught instanceof ProcessRequestRejected) {
        setActionReceipt(caught.receipt);
        await load();
        return;
      }
      const code = caught instanceof APIError
        ? caught.reasonCode || caught.code
        : undefined;
      setActionError(code
        ? l("We couldn't complete the action. ({code})", "처리를 완료하지 못했습니다. ({code})", { code })
        : caught instanceof Error
          ? caught.message
          : l("We couldn't complete the action.", "처리를 완료하지 못했습니다."));
    } finally {
      setWorking(undefined);
    }
  }, [l, load]);

  const can = useCallback((kind: string, id: string, action: string) =>
    (surface.actionsByKey[`${kind}:${id}`] ?? []).includes(action), [surface.actionsByKey]);

  return { surface, load, run, can, working, actionError, actionReceipt, receiptRefreshFailed };
}
