import { fireEvent, render } from "@testing-library/react-native";

import { LocaleProvider } from "../i18n/LocaleProvider";
import { BrowserDestinationLauncher } from "./BrowserDestinationLauncher";
import { orderedBrowserDestinations } from "./browserDestinations";

describe("BrowserDestinationLauncher", () => {
  it("renders reviewed targets and returns the exact selected target", async () => {
    const onSelect = jest.fn();
    const destinations = orderedBrowserDestinations("성수동 미용실").filter(({ id }) =>
      id === "COUPANG" || id === "NAVER_BOOKING",
    );
    const view = await render(
      <LocaleProvider>
        <BrowserDestinationLauncher destinations={destinations} onSelect={onSelect} />
      </LocaleProvider>,
    );

    expect(view.getByText("쇼핑 검색")).toBeTruthy();
    expect(view.getByText("예약처 검색")).toBeTruthy();
    fireEvent.press(view.getByTestId("browser-destination.NAVER_BOOKING"));
    expect(onSelect).toHaveBeenCalledWith(
      expect.objectContaining({
        id: "NAVER_BOOKING",
        host: "map.naver.com",
        kind: "reservation",
      }),
    );
  });

  it("shows an explicit empty state", async () => {
    const view = await render(
      <LocaleProvider>
        <BrowserDestinationLauncher destinations={[]} onSelect={jest.fn()} />
      </LocaleProvider>,
    );

    expect(view.getByText("열 수 있는 검색처가 없어요.")).toBeTruthy();
  });
});
