import { Component, type ErrorInfo, type ReactNode } from "react";
import { RotateCcw, TriangleAlert } from "lucide-react";
import { Button } from "./ui";

/** 页面级错误边界：单个页面出错时显示提示，而不是整个窗口白屏 */
export class ErrorBoundary extends Component<{ children: ReactNode; resetKey?: string }, { error: Error | null }> {
  state = { error: null as Error | null };

  static getDerivedStateFromError(error: Error) {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error(error, info.componentStack);
  }

  componentDidUpdate(prev: { resetKey?: string }) {
    if (prev.resetKey !== this.props.resetKey && this.state.error) this.setState({ error: null });
  }

  render() {
    if (!this.state.error) return this.props.children;
    return (
      <div className="flex h-full items-center justify-center p-8">
        <div className="card max-w-[520px] p-6 text-center">
          <div className="mx-auto mb-4 flex h-12 w-12 items-center justify-center rounded-2xl bg-bad/12 text-bad">
            <TriangleAlert size={22} />
          </div>
          <div className="text-[15px] font-semibold">页面出现错误</div>
          <div className="mt-2 font-mono text-[12px] break-words text-muted">{this.state.error.message}</div>
          <Button className="mt-5" icon={RotateCcw} onClick={() => this.setState({ error: null })}>
            重新加载此页面
          </Button>
        </div>
      </div>
    );
  }
}
