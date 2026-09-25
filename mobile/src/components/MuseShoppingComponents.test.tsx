import { fireEvent, render } from "@testing-library/react-native";
import { SafeAreaProvider } from "react-native-safe-area-context";

import { LocaleProvider } from "../i18n/LocaleProvider";
import { AgentActivitySheet } from "./AgentActivitySheet";
import { BackgroundResearchPanel } from "./BackgroundResearchPanel";
import { ExternalLinkApprovalSheet } from "./ExternalLinkApprovalSheet";
import { ShoppingGoalCard } from "./ShoppingGoalCard";
import { ShoppingMemorySheet } from "./ShoppingMemorySheet";
import { ShoppingProposalCard } from "./ShoppingProposalCard";

const metrics = {
  frame: { x: 0, y: 0, width: 390, height: 844 },
  insets: { top: 47, left: 0, right: 0, bottom: 34 },
};

function Providers({ children }: { children: React.ReactNode }) {
  return (
    <SafeAreaProvider initialMetrics={metrics}>
      <LocaleProvider>{children}</LocaleProvider>
    </SafeAreaProvider>
  );
}

describe("Muse-derived shopping presentation components", () => {
  it("presents a shopping goal as measurable progress with an activity affordance", async () => {
    const onOpenActivity = jest.fn();
    const view = await render(
      <ShoppingGoalCard
        goal={{
          id: "goal-1",
          title: "넓은 발볼 러닝화 찾기",
          state: "researching",
          progress: 0.62,
          progressDetail: "후보 3개 비교 중",
        }}
        onOpenActivity={onOpenActivity}
      />,
      { wrapper: Providers },
    );

    expect(view.getByLabelText("쇼핑 목표 진행률 62%").props.accessibilityValue).toEqual({ min: 0, max: 100, now: 62 });
    expect(view.getByText("조사 중")).toBeTruthy();
    await fireEvent.press(view.getByRole("button", { name: "진행 기록 보기" }));
    expect(onOpenActivity).toHaveBeenCalledWith("goal-1");
  });

  it("shows current work and a readable activity log without inventing a stop control", async () => {
    const onClose = jest.fn();
    const view = await render(
      <AgentActivitySheet
        items={[
          { id: "a1", title: "판매처 후보 확인", detail: "가격과 구매 가능 여부", state: "completed" },
          { id: "a2", title: "예산 기준으로 비교", state: "running" },
        ]}
        onClose={onClose}
        status="후보 가격을 비교하고 있어요"
        visible
      />,
      { wrapper: Providers },
    );

    expect(view.getByText("후보 가격을 비교하고 있어요")).toBeTruthy();
    expect(view.getByText("완료")).toBeTruthy();
    expect(view.queryByRole("button", { name: "현재 작업 중지" })).toBeNull();
    await fireEvent.press(view.getByRole("button", { name: "닫기" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("requires an explicit response before applying an agent proposal", async () => {
    const onAccept = jest.fn();
    const onDismiss = jest.fn();
    const view = await render(
      <ShoppingProposalCard
        onAccept={onAccept}
        onDismiss={onDismiss}
        proposal={{
          id: "proposal-1",
          title: "가격대를 넓혀 다시 찾아볼까요?",
          body: "현재 조건에서는 새 후보가 두 개뿐이에요.",
          reason: "더 다양한 후보를 비교할 수 있어요.",
          responseMode: "decision",
          status: "pending",
        }}
      />,
      { wrapper: Providers },
    );

    expect(onAccept).not.toHaveBeenCalled();
    await fireEvent.press(view.getByRole("button", { name: "제안 적용" }));
    expect(onAccept).toHaveBeenCalledWith("proposal-1");
    expect(onDismiss).not.toHaveBeenCalled();
  });

  it("connects background monitor and finding actions to their exact ids", async () => {
    const onCancelMonitor = jest.fn();
    const onAddFinding = jest.fn();
    const onHideFinding = jest.fn();
    const onOpenFinding = jest.fn();
    const view = await render(
      <BackgroundResearchPanel
        findings={[
          {
            id: "finding-1",
            monitorId: "monitor-1",
            title: "러닝화 새 할인",
            priceLabel: "89,000원",
            reason: "저장한 상한보다 낮아졌어요.",
            status: "new",
            openable: true,
          },
        ]}
        monitors={[
          {
            id: "monitor-1",
            title: "러닝화 가격",
            criteria: "120,000원 이하 · 대한민국",
            status: "active",
          },
        ]}
        onAddFinding={onAddFinding}
        onCancelMonitor={onCancelMonitor}
        onHideFinding={onHideFinding}
        onOpenFinding={onOpenFinding}
      />,
      { wrapper: Providers },
    );

    await fireEvent.press(view.getByRole("button", { name: "후보에 추가" }));
    await fireEvent.press(view.getByRole("button", { name: "확인 중지" }));
    await fireEvent.press(view.getByRole("button", { name: "판매처 확인" }));
    expect(onAddFinding).toHaveBeenCalledWith("finding-1");
    expect(onCancelMonitor).toHaveBeenCalledWith("monitor-1");
    expect(onOpenFinding).toHaveBeenCalledWith("finding-1");
    expect(onHideFinding).not.toHaveBeenCalled();
  });

  it("omits background research when only inactive or hidden records remain", async () => {
    const view = await render(
      <BackgroundResearchPanel
        findings={[{
          id: "hidden-finding",
          monitorId: "expired-monitor",
          title: "숨긴 후보",
          reason: "사용자가 숨겼어요.",
          status: "hidden",
        }]}
        monitors={[{
          id: "expired-monitor",
          title: "종료된 확인",
          criteria: "지난 조건",
          status: "expired",
        }]}
      />,
      { wrapper: Providers },
    );

    expect(view.toJSON()).toBeNull();
  });

  it("omits a disabled background research message when there is no meaningful content", async () => {
    const view = await render(
      <BackgroundResearchPanel
        findings={[]}
        monitors={[]}
        unavailableMessage="조건 확인을 사용할 수 없어요."
      />,
      { wrapper: Providers },
    );

    expect(view.toJSON()).toBeNull();
  });

  it("keeps an active monitor visible while no findings have arrived", async () => {
    const view = await render(
      <BackgroundResearchPanel
        findings={[]}
        monitors={[{
          id: "active-monitor",
          title: "러닝화 가격",
          criteria: "120,000원 이하",
          status: "active",
        }]}
      />,
      { wrapper: Providers },
    );

    expect(view.getByTestId("research-monitor.active-monitor")).toBeTruthy();
    expect(view.getByText("조건 확인은 계속되고 있어요. 아직 새 후보는 없습니다.")).toBeTruthy();
  });

  it("separates seller navigation approval from ordering and payment", async () => {
    const onAllow = jest.fn();
    const onDeny = jest.fn();
    const view = await render(
      <ExternalLinkApprovalSheet
        onAllow={onAllow}
        onDeny={onDeny}
        target={{
          destinationLabel: "example-store.com",
          priceLabel: "USD 89.00",
          productTitle: "Wide running shoe",
          sellerName: "Example Store",
        }}
        visible
      />,
      { wrapper: Providers },
    );

    expect(view.getByText(/승인한 옵션·수량만 브라우저에서 준비/)).toBeTruthy();
    expect(onAllow).not.toHaveBeenCalled();
    await fireEvent.press(view.getByRole("button", { name: "판매처 열기" }));
    expect(onAllow).toHaveBeenCalledTimes(1);
    expect(onDeny).not.toHaveBeenCalled();
  });

  it("presents a direct reservation search as user-controlled navigation", async () => {
    const onAllow = jest.fn();
    const view = await render(
      <ExternalLinkApprovalSheet
        onAllow={onAllow}
        onDeny={jest.fn()}
        target={{
          destinationLabel: "map.naver.com",
          productTitle: "성수동 미용실",
          purpose: "reservation",
          sellerName: "네이버 예약",
        }}
        visible
      />,
      { wrapper: Providers },
    );

    expect(view.getByRole("header", { name: "이 지도에서 예약처를 직접 찾을까요?" })).toBeTruthy();
    expect(view.getByText("예약처 검색")).toBeTruthy();
    expect(view.getByText(/로그인, 예약 확정, 주문, 결제는 해당 사이트에서 직접/)).toBeTruthy();
    await fireEvent.press(view.getByRole("button", { name: "브라우저 열기" }));
    expect(onAllow).toHaveBeenCalledTimes(1);
  });

  it("keeps external navigation failures visible in the approval sheet", async () => {
    const view = await render(
      <ExternalLinkApprovalSheet
        error="판매처 사이트를 열지 못했어요. 주소를 다시 확인해 주세요."
        onAllow={jest.fn()}
        onDeny={jest.fn()}
        target={{
          destinationLabel: "example-store.com",
          productTitle: "Wide running shoe",
          sellerName: "Example Store",
        }}
        visible
      />,
      { wrapper: Providers },
    );

    expect(view.getByRole("alert")).toHaveTextContent(/판매처 사이트를 열지 못했어요/);
  });

  it("does not offer seller navigation for a finding without a validated URL", async () => {
    const view = await render(
      <BackgroundResearchPanel
        findings={[{
          id: "finding-without-url",
          monitorId: "monitor-1",
          title: "판매처 주소가 없는 후보",
          reason: "조건에는 맞지만 이동할 주소가 없어요.",
          status: "new",
        }]}
        monitors={[]}
        onOpenFinding={jest.fn()}
      />,
      { wrapper: Providers },
    );

    expect(view.queryByRole("button", { name: "판매처 확인" })).toBeNull();
  });

  it("keeps shopping defaults controlled and asks before discarding a draft", async () => {
    const onClose = jest.fn();
    const onDraftChange = jest.fn();
    const currentValue = { researchCountry: "KR", preferredCurrency: "KRW", uiLocale: "ko-KR" } as const;
    const view = await render(
      <ShoppingMemorySheet
        currentValue={currentValue}
        draftValue={{ ...currentValue, preferredCurrency: "USD" }}
        onApply={jest.fn()}
        onClose={onClose}
        onDraftChange={onDraftChange}
        visible
      />,
      { wrapper: Providers },
    );

    await fireEvent.press(view.getByRole("radio", { name: "KRW · 원" }));
    expect(onDraftChange).toHaveBeenCalledWith({ ...currentValue, preferredCurrency: "KRW" });
    await fireEvent.press(view.getByRole("button", { name: "취소" }));
    expect(view.getByText("변경을 버릴까요?")).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
  });
});
