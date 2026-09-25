import { fireEvent, render, waitFor } from "@testing-library/react-native";
import { SafeAreaProvider } from "react-native-safe-area-context";

import { LocaleProvider } from "../i18n/LocaleProvider";
import { ActionButton } from "./ActionButton";
import { AppScaffold } from "./AppScaffold";
import { BudgetSheet } from "./BudgetSheet";
import { BrowserSessionSurface } from "./BrowserSessionSurface";
import { CandidateCard } from "./CandidateCard";
import { ChoiceCard } from "./ChoiceCard";
import {
  ClarificationSheet,
  type ClarificationDraft,
} from "./ClarificationSheet";

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

describe("mobile shared components", () => {
  it("qualifies live purchase preparation as supported-merchant only in both locales", async () => {
    const view = await render(
      <AppScaffold galleryEnabled={false} galleryOpen={false} mode="server" onToggleGallery={jest.fn()}>
        <></>
      </AppScaffold>,
      { wrapper: Providers },
    );

    expect(view.getByText("실시간 조사 · 지원 판매처 구매 준비")).toBeTruthy();
    await fireEvent.press(view.getByRole("button", { name: "English" }));
    expect(view.getByText("Live research · Supported merchant preparation")).toBeTruthy();
  });

  it("emits page readiness from the loading browser session without embedding a merchant frame", async () => {
    const onPageReady = jest.fn();
    const view = await render(
      <BrowserSessionSurface
        controlMode="agent"
        onPageReady={onPageReady}
        session={{
          merchantName: "Example Store",
          productTitle: "Wide running shoe",
          state: "loading",
        }}
      />,
      { wrapper: Providers },
    );

    expect(view.getByTestId("browser.session.state.loading")).toBeTruthy();
    expect(view.getByText("판매처 상품을 여는 중")).toBeTruthy();
    expect(view.queryByTestId("browser.sign-in-completed")).toBeNull();
    await waitFor(() => expect(onPageReady).toHaveBeenCalledTimes(1));
  });

  it("omits credential and payment controls from the web review simulator", async () => {
    const signIn = await render(
      <BrowserSessionSurface
        controlMode="user"
        reviewMode
        session={{
          merchantName: "Example Store",
          productTitle: "Wide running shoe",
          state: "sign_in_required",
        }}
      />,
      { wrapper: Providers },
    );

    expect(signIn.getByTestId("browser.review-no-credentials")).toBeTruthy();
    expect(signIn.queryByText("아이디 또는 이메일")).toBeNull();
    expect(signIn.queryByText("비밀번호")).toBeNull();

    await signIn.rerender(
      <BrowserSessionSurface
        controlMode="user"
        reviewMode
        session={{
          merchantName: "Example Store",
          productTitle: "Wide running shoe",
          state: "payment_handoff",
        }}
      />,
    );
    expect(signIn.getByTestId("browser.review-no-payment")).toBeTruthy();
    expect(signIn.queryByText("판매처 결제 버튼 · 사용자만 누름")).toBeNull();
  });

  it("keeps the action name available while a button is busy", async () => {
    const view = await render(<ActionButton busy label="준비 중" onPress={jest.fn()} />);

    const button = view.getByRole("button", { name: "준비 중" });
    expect(button.props.accessibilityState).toMatchObject({ busy: true, disabled: true });
  });

  it("exposes selected and disabled choice states without relying on color", async () => {
    const onPress = jest.fn();
    const view = await render(
      <ChoiceCard
        description="데모 지역 선택"
        onPress={onPress}
        selected
        title="서울 마포구"
      />,
    );

    const selected = view.getByRole("radio", { checked: true });
    expect(selected.props.accessibilityState).toMatchObject({ checked: true, selected: true });
    expect(view.getByText("✓")).toBeTruthy();
    await fireEvent.press(selected);
    expect(onPress).toHaveBeenCalledTimes(1);
  });

  it("keeps a clarification answer in draft until the apply action", async () => {
    let draft: ClarificationDraft = { customText: "", noPreference: false };
    const onApply = jest.fn();
    const onDraftChange = jest.fn((next: ClarificationDraft) => { draft = next; });
    const view = await render(
      <ClarificationSheet
        current={1}
        draft={draft}
        onApply={onApply}
        onClose={jest.fn()}
        onDraftChange={onDraftChange}
        question={{
          id: "q-region",
          title: "어느 지역으로 준비할까요?",
          reason: "배송 가능 여부를 확인해요.",
          optional: false,
          allowCustom: true,
          options: [
            { id: "mapo", title: "서울 마포구", description: "데모 지역" },
            { id: "other", title: "다른 지역", description: "직접 입력" },
          ],
        }}
        total={3}
        visible
      />,
      { wrapper: Providers },
    );

    await fireEvent.press(view.getByRole("radio", { name: /서울 마포구/ }));
    expect(onApply).not.toHaveBeenCalled();
    expect(onDraftChange).toHaveBeenCalledWith({
      selectedOptionId: "mapo",
      customText: "",
      noPreference: false,
    });

    await view.rerender(
      <ClarificationSheet
        current={1}
        draft={draft}
        onApply={onApply}
        onClose={jest.fn()}
        onDraftChange={onDraftChange}
        question={{
          id: "q-region",
          title: "어느 지역으로 준비할까요?",
          reason: "배송 가능 여부를 확인해요.",
          optional: false,
          allowCustom: true,
          options: [
            { id: "mapo", title: "서울 마포구", description: "데모 지역" },
            { id: "other", title: "다른 지역", description: "직접 입력" },
          ],
        }}
        total={3}
        visible
      />,
    );
    await fireEvent.press(view.getByRole("button", { name: "이 답변 적용" }));
    expect(onApply).toHaveBeenCalledTimes(1);
  });

  it("confirms before discarding a clarification draft", async () => {
    const onClose = jest.fn();
    const view = await render(
      <ClarificationSheet
        current={1}
        draft={{ selectedOptionId: "mapo", customText: "", noPreference: false }}
        hasUnappliedChanges
        onApply={jest.fn()}
        onClose={onClose}
        onDraftChange={jest.fn()}
        question={{
          id: "q-region",
          title: "어느 지역으로 준비할까요?",
          reason: "배송 가능 여부를 확인해요.",
          optional: false,
          allowCustom: true,
          options: [{ id: "mapo", title: "서울 마포구", description: "데모 지역" }],
        }}
        total={3}
        visible
      />,
      { wrapper: Providers },
    );

    await fireEvent.press(view.getByRole("button", { name: "닫기" }));
    expect(view.getByText("선택을 버릴까요?")).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
    await fireEvent.press(view.getByRole("button", { name: "선택 버리기" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("shows a budget preview separately from the apply command", async () => {
    const onApply = jest.fn();
    const onClose = jest.fn();
    const view = await render(
      <BudgetSheet
        budgetOptions={[80_000]}
        currency="KRW"
        currentBudget={150_000}
        draftBudget={80_000}
        onApply={onApply}
        onClose={onClose}
        onDraftChange={jest.fn()}
        preview={{
          knownTotal: { currency: "KRW", amount: 75_000 },
          remaining: { currency: "KRW", amount: 5_000 },
          overage: { currency: "KRW", amount: 0 },
          totalState: "COMPLETE",
          comparisonState: "AVAILABLE",
          summary: "장식을 제외하고 필수 항목은 유지해요.",
        }}
        visible
      />,
      { wrapper: Providers },
    );

    expect(await view.findByText(/75,000원/)).toBeTruthy();
    expect(onApply).not.toHaveBeenCalled();
    await fireEvent.press(view.getByRole("button", { name: "취소" }));
    expect(view.getByText("예산 변경을 취소할까요?")).toBeTruthy();
    expect(onClose).not.toHaveBeenCalled();
    await fireEvent.press(view.getByRole("button", { name: "계속 조정" }));
    await fireEvent.press(view.getByRole("button", { name: "이 예산 적용" }));
    expect(onApply).toHaveBeenCalledTimes(1);
  });

  it("labels an over-budget local preview as a shortfall instead of feasible", async () => {
    const view = await render(
      <BudgetSheet
        budgetOptions={[80_000]}
        currency="KRW"
        currentBudget={150_000}
        draftBudget={80_000}
        onApply={jest.fn()}
        onClose={jest.fn()}
        onDraftChange={jest.fn()}
        preview={{
          knownTotal: { currency: "KRW", amount: 120_000 },
          remaining: { currency: "KRW", amount: 0 },
          overage: { currency: "KRW", amount: 40_000 },
          totalState: "COMPLETE",
          comparisonState: "AVAILABLE",
          summary: "현재 항목 기준의 비권위적 미리보기",
        }}
        visible
      />,
      { wrapper: Providers },
    );

    expect(view.getByText("예산 변경 미리보기")).toBeTruthy();
    expect(view.getByText("현재 합계의 예산 초과분")).toBeTruthy();
    expect(view.getByText("40,000원")).toBeTruthy();
    expect(view.queryByText("가능한 준비안")).toBeNull();
  });

  it("preserves a preview total currency and withholds an invalid cross-currency remainder", async () => {
    const view = await render(
      <BudgetSheet
        budgetOptions={[80_000]}
        currency="KRW"
        currentBudget={150_000}
        draftBudget={80_000}
        onApply={jest.fn()}
        onClose={jest.fn()}
        onDraftChange={jest.fn()}
        preview={{
          knownTotal: { currency: "USD", amount: 24.99 },
          remaining: { currency: "KRW", amount: 0 },
          overage: { currency: "KRW", amount: 0 },
          totalState: "PARTIAL",
          comparisonState: "CURRENCY_MISMATCH",
          summary: "통화가 다른 후보",
        }}
        visible
      />,
      { wrapper: Providers },
    );

    expect(view.getByText("USD 24.99")).toBeTruthy();
    expect(view.getByText(/잔액을 계산할 수 없어요/)).toBeTruthy();
    expect(view.queryByText("24.99원")).toBeNull();
  });

  it("renders observed ranges and unknown prices without substituting zero", async () => {
    const base = {
      kind: "product" as const,
      source: "AMAZON",
      option: "Observed product",
      quantity: 1,
      reason: "Matches the request",
      timing: "External purchase",
      selected: false,
      visualLabel: "A",
      visualTone: "water" as const,
    };
    const view = await render(
      <>
        <CandidateCard
          candidate={{
            ...base,
            id: "range",
            title: "Range product",
            price: {
              kind: "RANGE",
              minimum: { currency: "USD", amount: 24.99 },
              maximum: { currency: "USD", amount: 39.99 },
            },
          }}
        />
        <CandidateCard
          candidate={{
            ...base,
            id: "unknown",
            title: "Unknown product",
            price: { kind: "UNKNOWN", reasonCode: "PRICE_UNOBSERVED" },
          }}
        />
      </>,
      { wrapper: Providers },
    );

    expect(view.getByText("USD 24.99 – USD 39.99")).toBeTruthy();
    expect(view.getByText("가격 확인 중")).toBeTruthy();
    expect(view.queryByText("0원")).toBeNull();
  });
});
