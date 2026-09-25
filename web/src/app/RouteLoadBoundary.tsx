import { Component, type ReactNode } from "react";
import { useLocation } from "react-router";
import { useLocale } from "../shared/i18n";
import { FeedbackState } from "../shared/ui";

type RouteErrorBoundaryProps = {
  children: ReactNode;
  fallback: ReactNode;
};

type RouteErrorBoundaryState = {
  failed: boolean;
};

class RouteErrorBoundary extends Component<
  RouteErrorBoundaryProps,
  RouteErrorBoundaryState
> {
  state: RouteErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): RouteErrorBoundaryState {
    return { failed: true };
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}

export function RouteLoadBoundary({ children }: { children: ReactNode }) {
  const { l } = useLocale();
  const location = useLocation();
  const resetKey = `${location.pathname}${location.search}`;

  return (
    <RouteErrorBoundary
      key={resetKey}
      fallback={(
        <section className="workspace-card">
          <FeedbackState
            state="error"
            title={l("We couldn't open this screen", "이 화면을 열지 못했습니다")}
            description={l(
              "Your cart is still saved. Reload the page to use the latest screen files and try again.",
              "장바구니는 그대로 저장되어 있습니다. 최신 화면 파일로 다시 시도하려면 페이지를 새로고침해 주세요.",
            )}
            action={{
              label: l("Reload page", "페이지 새로고침"),
              onAction: () => window.location.reload(),
            }}
          />
        </section>
      )}
    >
      {children}
    </RouteErrorBoundary>
  );
}
