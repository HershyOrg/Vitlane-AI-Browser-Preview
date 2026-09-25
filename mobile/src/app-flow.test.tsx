import { fireEvent, render, waitFor } from "@testing-library/react-native";

import App from "../App";

const partyIntent = "10월 3일에 친구 6명이랑 집에서 파티할 거야. 15만 원 안으로 준비해줘. 컵이랑 스피커는 있어.";

async function submitPartyIntent(view: Awaited<ReturnType<typeof render>>) {
  await fireEvent.changeText(view.getByLabelText(/10월 3일/), partyIntent);
  await fireEvent.press(view.getByRole("button", { name: "준비 시작" }));
  await view.findByText("추천 준비안");
}

describe("fixture workspace flow", () => {
  it("shows a usable plan immediately and keeps refinements optional", async () => {
    const view = await render(<App />);

    expect(view.getByText("Lane")).toBeTruthy();
    expect(view.getByText("준비 요청을 기다리고 있어요")).toBeTruthy();
    expect(view.getByTestId("home.intent")).toBeTruthy();
    expect(view.getByTestId("home.send")).toBeTruthy();

    await submitPartyIntent(view);

    expect(view.queryByTestId("question.sheet")).toBeNull();
    expect(view.getByText("먼저 이렇게 가정했어요")).toBeTruthy();
    expect(view.getByTestId("assumption.region")).toBeTruthy();
    expect(view.getByTestId("assumption.pickup-time")).toBeTruthy();
    expect(view.getByTestId("assumption.service")).toBeTruthy();
    expect(view.queryByTestId("question.blocking")).toBeNull();
    expect(view.queryByText("현재 조건")).toBeNull();
    expect(view.getAllByText("124,000원").length).toBeGreaterThan(0);

    await fireEvent.press(view.getByTestId("assumption.region"));
    expect(await view.findByTestId("question.sheet")).toBeTruthy();
    expect(view.getByText("어느 지역으로 준비할까요?")).toBeTruthy();
    expect(view.queryByText("케이크 픽업도 함께 준비할까요?")).toBeNull();
    expect(view.getByRole("radio", { checked: true, name: /서울 마포구/ })).toBeTruthy();
    await fireEvent.press(view.getByRole("button", { name: "이 답변 적용" }));
    await waitFor(() => expect(view.queryByTestId("question.sheet")).toBeNull());

    expect(view.queryByTestId("assumption.region")).toBeNull();
    expect(view.getByTestId("assumption.service")).toBeTruthy();
    expect(view.getAllByText("124,000원").length).toBeGreaterThan(0);

    await fireEvent.press(view.getByRole("button", { name: "예산 조정" }));
    await fireEvent.press(view.getByRole("radio", { name: /80,000원/ }));
    await waitFor(() => expect(view.getAllByText("75,000원").length).toBeGreaterThan(1));
    await fireEvent.press(view.getByRole("button", { name: "이 예산 적용" }));

    await waitFor(() => {
      expect(view.getAllByText("75,000원").length).toBeGreaterThan(0);
      expect(view.getByText("8만 원 예산에 맞춘 준비")).toBeTruthy();
    });
    expect(view.getByTestId("assumption.service")).toBeTruthy();

    await fireEvent.press(view.getByRole("button", { name: "픽업을 오후 5시로" }));
    await fireEvent.press(view.getByRole("button", { name: "수정 요청" }));

    await waitFor(() => {
      expect(view.getByText("✓ 케이크 픽업을 오후 5시로 변경")).toBeTruthy();
    });
    expect(view.getByTestId("assumption.service")).toBeTruthy();

    await fireEvent.press(view.getByRole("button", { name: "English" }));
    await waitFor(() => {
      expect(view.getAllByText("KRW 75,000").length).toBeGreaterThan(1);
      expect(view.getByText("✓ Cake pickup changed to 5 PM")).toBeTruthy();
    });
  });

  it("applies the optional goods-only refinement only after the user opens it", async () => {
    const view = await render(<App />);
    await submitPartyIntent(view);

    await fireEvent.press(view.getByTestId("assumption.service"));
    expect(await view.findByTestId("question.sheet")).toBeTruthy();
    await fireEvent.press(view.getByRole("radio", { name: /상품만 준비/ }));
    await fireEvent.press(view.getByRole("button", { name: "이 답변 적용" }));

    await waitFor(() => {
      expect(view.getAllByText("82,000원").length).toBeGreaterThan(0);
      expect(view.getByTestId("candidate.party-goods-only")).toBeTruthy();
    });
    expect(view.queryByTestId("question.sheet")).toBeNull();
    expect(view.queryByTestId("assumption.service")).toBeNull();
    expect(view.queryByTestId("assumption.pickup-time")).toBeNull();
    expect(view.getByText("배송 상품만 준비")).toBeTruthy();
  });
});
