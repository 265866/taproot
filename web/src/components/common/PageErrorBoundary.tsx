import { Component, type ErrorInfo, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { useRouteError } from 'react-router-dom';
import { Button } from '@/components/ui/Button';
import { getErrorMessage } from '@/utils/helpers';

/** A plain recovery card: what failed, the reason, and a reload. */
export function PageErrorView({ error }: { error: unknown }) {
  const { t } = useTranslation();
  const message = getErrorMessage(error);
  return (
    <div className="connection-gate-card" role="alert">
      <h1 className="connection-gate-title">{t('shell.page_error_title')}</h1>
      <p className="connection-gate-body">{t('shell.page_error_body')}</p>
      {message ? <p className="connection-gate-hint">{message}</p> : null}
      <Button onClick={() => window.location.reload()}>{t('shell.page_error_reload')}</Button>
    </div>
  );
}

/** The router's last resort, for an error outside any page (the shell or the connection gate). */
export function AppErrorScreen() {
  const error = useRouteError();
  return (
    <div className="connection-gate">
      <PageErrorView error={error} />
    </div>
  );
}

interface PageErrorBoundaryProps {
  /** A new value (the page's path) clears the error, so navigating away recovers. */
  resetKey: string;
  children: ReactNode;
}

interface PageErrorBoundaryState {
  error: unknown;
  resetKey: string;
}

/** Keeps a page's failure (a chunk that will not load, a render error) inside the page area. */
export class PageErrorBoundary extends Component<PageErrorBoundaryProps, PageErrorBoundaryState> {
  state: PageErrorBoundaryState = { error: null, resetKey: this.props.resetKey };

  static getDerivedStateFromError(error: unknown): Partial<PageErrorBoundaryState> {
    return { error: error ?? new Error('Unknown error') };
  }

  static getDerivedStateFromProps(
    props: PageErrorBoundaryProps,
    state: PageErrorBoundaryState
  ): Partial<PageErrorBoundaryState> | null {
    return props.resetKey === state.resetKey ? null : { error: null, resetKey: props.resetKey };
  }

  componentDidCatch(error: unknown, info: ErrorInfo) {
    console.error('Page failed to render:', error, info.componentStack);
  }

  render() {
    return this.state.error ? <PageErrorView error={this.state.error} /> : this.props.children;
  }
}
