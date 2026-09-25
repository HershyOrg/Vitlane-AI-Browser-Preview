import { Component, type ReactNode } from "react";
import { Button } from "../../../shared/ui";

type SectionErrorBoundaryProps = {
  title: string;
  description: string;
  retryLabel: string;
  children: ReactNode;
};

type SectionErrorBoundaryState = {
  failed: boolean;
};

// 한 구역의 렌더 예외가 화면 전체를 비우지 않도록 막는 마지막 방어선이다.
// production 재조사 FAILED round에서 워크스페이스 진입 자체가 막힌 회귀의
// 재발 방지용으로, 실패한 구역만 안내와 다시 시도로 대체한다.
export class SectionErrorBoundary extends Component<
  SectionErrorBoundaryProps,
  SectionErrorBoundaryState
> {
  state: SectionErrorBoundaryState = { failed: false };

  static getDerivedStateFromError(): SectionErrorBoundaryState {
    return { failed: true };
  }

  render() {
    if (!this.state.failed) return this.props.children;
    return (
      <div className="curation-artifact-empty curation-artifact-empty--recovery" role="alert">
        <strong>{this.props.title}</strong>
        <p>{this.props.description}</p>
        <Button
          type="button"
          emphasis="secondary"
          size="compact"
          onClick={() => this.setState({ failed: false })}
        >
          {this.props.retryLabel}
        </Button>
      </div>
    );
  }
}
