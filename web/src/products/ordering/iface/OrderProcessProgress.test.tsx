// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import { LocaleProvider } from "../../../shared/i18n";
import { listProcessRequests, type ProcessReceipt } from "../infra/orderProcessApi";
import { OrderProcessProgress } from "./OrderProcessProgress";
import { processProgressLabel } from "../app/processPresentation";
vi.mock("../infra/orderProcessApi", () => ({ listProcessRequests: vi.fn() }));
const operation: ProcessReceipt = { schemaVersion:"vitlane.order-process-receipt.v1",agencyOrderId:"order",merchantOrderId:"mo",requestId:"request",flowId:"flow",kind:"PURCHASE",outcome:"WAITING",guidance:{reasonCode:"PAYMENT_OUTCOME_UNKNOWN",customerAction:"WAIT",operatorAction:"RECONCILE_PAYMENT"} };
afterEach(() => { vi.useRealTimers(); vi.clearAllMocks(); localStorage.clear(); document.cookie = "vt_locale_choice=; Path=/; Max-Age=0"; });
it.each(["en-US", "ko-KR"])("shows unknown and polls to completion in %s", async (locale) => {
 vi.useFakeTimers(); document.cookie = `vt_locale_choice=${locale}; Path=/`;
 vi.mocked(listProcessRequests).mockResolvedValueOnce({schemaVersion:"vitlane.order-process-requests.v1",requests:[operation]}).mockResolvedValue({schemaVersion:"vitlane.order-process-requests.v1",requests:[{...operation,outcome:"COMPLETED",guidance:{reasonCode:"MERCHANT_PLACED",customerAction:"VIEW_RESULT",operatorAction:"VIEW_RESULT"}}]});
 const container = document.createElement("div"); document.body.append(container); const root=createRoot(container);
 try {
  await act(async()=>root.render(<LocaleProvider><OrderProcessProgress orderId="order" shops={[{id:"mo",shopDomain:"example.test"}]} /></LocaleProvider>));
  expect(container.textContent).toContain(locale==="en-US"?"The payment result is being confirmed":"결제 결과를 확인하고 있습니다");
  expect(container.textContent).not.toContain("ACTIVATING_FUNDING");
  await act(async()=>vi.advanceTimersByTimeAsync(2000));
  expect(container.textContent).toContain(locale==="en-US"?"The merchant purchase result is confirmed":"판매처 구매 결과가 확인");
  expect(listProcessRequests).toHaveBeenCalledTimes(2);
 } finally {await act(async()=>root.unmount());container.remove();}
});
it.each(["REJECTED","RECEIVED","ACCEPTED","DEFERRED","WAITING"] as const)("does not label %s as completed",outcome=>{
 expect(processProgressLabel({...operation,outcome,guidance:{customerAction:"WAIT",operatorAction:"WAIT"}},(en)=>en)).not.toContain("The requested action is recorded");
});
it("gives different merchant-result instructions to the customer and operator",()=>{
 const receipt:ProcessReceipt={...operation,guidance:{reasonCode:"MERCHANT_RESULT_REQUIRED",customerAction:"WAIT",operatorAction:"RECORD_MERCHANT_RESULT"}};
 expect(processProgressLabel(receipt,en=>en)).toContain("operator is confirming");
 expect(processProgressLabel(receipt,en=>en,true)).toContain("Record the merchant's actual result");
});
it("never calls a cancelled purchase refunded before the payment result",()=>{
 const receipt:ProcessReceipt={...operation,kind:"CANCEL",guidance:{reasonCode:"CANCELLATION_CONFIRMED_REFUND_PENDING",customerAction:"WAIT",operatorAction:"WAIT"}};
 expect(processProgressLabel(receipt,en=>en)).toContain("Payment return is still pending");
});

it("distinguishes a paused payment gate from an unknown payment result",()=>{
 const receipt:ProcessReceipt={...operation,guidance:{reasonCode:"MONEY_GATE_CLOSED",customerAction:"WAIT",operatorAction:"CHECK_PAYMENT_GATE"}};
 expect(processProgressLabel(receipt,en=>en)).toContain("do not submit another payment");
 expect(processProgressLabel(receipt,en=>en,true)).toContain("Check the payment controls");
});
