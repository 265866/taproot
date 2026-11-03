import { useEffect, type ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { useAuthStore } from '@/stores';
import type { ConnectionFailure, ConnectionStatus } from '@/types';

interface ConnectionGateViewProps {
  status: ConnectionStatus;
  failure: ConnectionFailure | null;
  onRetry: () => void;
  children: ReactElement;
}

export function ConnectionGateView({
  status,
  failure,
  onRetry,
  children,
}: ConnectionGateViewProps) {
  const { t } = useTranslation();

  if (status === 'connected') return children;

  if (status === 'error') {
    const key = failure === 'forbidden' ? 'forbidden' : 'unreachable';
    return (
      <div className="connection-gate" role="alert">
        <div className="connection-gate-card">
          <h1 className="connection-gate-title">{t(`connection_gate.${key}_title`)}</h1>
          <p className="connection-gate-body">{t(`connection_gate.${key}_body`)}</p>
          <Button variant="secondary" onClick={onRetry}>
            {t('connection_gate.retry')}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="connection-gate" aria-busy="true">
      <LoadingSpinner />
      <span className="connection-gate-body">{t('connection_gate.connecting')}</span>
    </div>
  );
}

/** Connects to the same-origin management API once and holds the app until it answers. */
export function ConnectionGate({ children }: { children: ReactElement }) {
  const status = useAuthStore((state) => state.connectionStatus);
  const failure = useAuthStore((state) => state.connectionFailure);
  const connect = useAuthStore((state) => state.connect);

  useEffect(() => {
    if (useAuthStore.getState().connectionStatus === 'disconnected') {
      void connect();
    }
  }, [connect]);

  return (
    <ConnectionGateView status={status} failure={failure} onRetry={() => void connect()}>
      {children}
    </ConnectionGateView>
  );
}
