import { fireEvent, render, within } from "@testing-library/react-native";

import { LocaleProvider } from "../i18n/LocaleProvider";
import { ComponentGallery } from "./ComponentGallery";

describe("ComponentGallery shopping surfaces", () => {
  it("renders reusable shopping previews and opens each connected review sheet", async () => {
    const view = await render(
      <LocaleProvider>
        <ComponentGallery />
      </LocaleProvider>,
    );

    expect(view.getByText(/검수용 합성 데이터입니다/)).toBeTruthy();
    expect(view.getByTestId("shopping-goal.gallery-shopping-goal")).toBeTruthy();
    expect(view.getByTestId("shopping-proposal.gallery-proposal")).toBeTruthy();
    expect(view.getByTestId("research-monitor.gallery-monitor")).toBeTruthy();
    expect(view.getByTestId("research-finding.gallery-finding")).toBeTruthy();

    await fireEvent.press(view.getByRole("button", { name: "제안 적용" }));
    expect(view.getByText("적용됨")).toBeTruthy();

    await fireEvent.press(view.getByTestId("gallery.open-activity"));
    expect(view.getByText("Lane의 진행 기록")).toBeTruthy();
    expect(view.getAllByText("활동 기록").length).toBeGreaterThan(0);
    await fireEvent.press(view.getByRole("button", { name: "닫기" }));

    await fireEvent.press(view.getByTestId("gallery.open-memory"));
    expect(view.getByText("명시적으로 저장한 기본값")).toBeTruthy();
    await fireEvent.press(view.getByRole("button", { name: "닫기" }));

    expect(view.getByRole("button", { name: "판매처 확인" })).toBeTruthy();
    await fireEvent.press(view.getByTestId("gallery.open-external-approval"));
    expect(view.getByRole("button", { name: "판매처 열기" })).toBeTruthy();
    expect(view.getByText(/승인한 옵션·수량만 브라우저에서 준비/)).toBeTruthy();

    await fireEvent.press(view.getByRole("button", { name: "판매처 열기" }));
    expect(view.getByTestId("run.screen")).toBeTruthy();
    expect(view.getByText("UI 검수용 화면 · 실제 브라우저 아님")).toBeTruthy();
    expect(view.getByTestId("browser.origin-host").props.children).toBe("shop.example");

    const browser = within(view.getByTestId("run.screen"));
    expect(browser.getByText(/AI 전송 범위: 필터가 허용한 페이지 텍스트/)).toBeTruthy();
    await fireEvent.press(browser.getByTestId("browser.take-over"));
    expect(browser.getByText("직접 조작 중")).toBeTruthy();
    await fireEvent.press(browser.getByTestId("browser.resume-agent"));
    expect(browser.getByText("AI 조작 중")).toBeTruthy();
    await fireEvent.press(browser.getByRole("button", { name: "닫기" }));

    await fireEvent.press(view.getByTestId("gallery.open-handoff"));
    expect(view.getByTestId("handoff.sheet")).toBeTruthy();
    expect(view.getByText("이제 직접 확인하고 결제해 주세요")).toBeTruthy();
    expect(view.getByText(/비밀번호·OTP·결제 정보/)).toBeTruthy();
  });
});
