// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it, vi } from "vitest";
import { Selection } from "./Selection";

const options = [
  { value: "7", label: "7일" },
  { value: "30", label: "30일" },
];

describe("Selection (Still Water 3-1)", () => {
  it("선택된 항목만 on 상태이고 contract class를 쓴다", () => {
    const markup = renderToStaticMarkup(
      <Selection ariaLabel="조회 기간" onChange={() => undefined} options={options} value="30" />,
    );
    expect(markup).toContain("vt-selection");
    expect(markup).toContain("vt-selection__option");
    expect(markup.match(/data-state="on"/g)).toHaveLength(1);
    expect(markup.match(/data-state="off"/g)).toHaveLength(1);
  });

  it("다른 항목을 누르면 onChange가 오고, 현재 항목을 다시 눌러도 빈 값은 오지 않는다", async () => {
    const onChange = vi.fn();
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => {
      root.render(<Selection ariaLabel="조회 기간" onChange={onChange} options={options} value="30" />);
    });
    const buttons = [...container.querySelectorAll<HTMLButtonElement>("button")];
    expect(buttons.map((button) => button.textContent)).toEqual(["7일", "30일"]);
    await act(async () => {
      buttons[0].click();
    });
    expect(onChange).toHaveBeenCalledWith("7");
    onChange.mockClear();
    await act(async () => {
      buttons[1].click();
    });
    expect(onChange).not.toHaveBeenCalled();
    await act(async () => root.unmount());
    container.remove();
  });
});
