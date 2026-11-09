import { useEffect, type ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { taprootApi } from '@/services/api/taproot';
import { useAuthStore } from '@/stores';
import type { ConnectionFailure, ConnectionStatus, DeniedDevice } from '@/types';
import { ClaimScreen } from './ClaimScreen';
import { browserDeviceName } from './deviceGuess';
import { RequestAccessScreen } from './RequestAccessScreen';

interface ConnectionGateViewProps {
  status: ConnectionStatus;
  failure: ConnectionFailure | null;
  /** The refused device, for a `not_admin` failure. */
  device?: DeniedDevice | null;
  onRetry: () => void;
  /** Sends the setup code while the server is unclaimed. */
  claim?: (code: string) => Promise<unknown>;
  /** Runs after a successful claim. */
  onClaimed?: () => void;
  /** Asks for access for a device without Admin on a claimed server. */
  requestAccess?: (name: string) => Promise<unknown>;
  /** The request form's starting name; defaults to a guess from the browser. */
  defaultName?: string;
  children: ReactElement;
}

export function ConnectionGateView({
  status,
  failure,
  device = null,
  onRetry,
  claim = taprootApi.claim,
  onClaimed = onRetry,
  requestAccess = taprootApi.requestAccess,
  defaultName,
  children,
}: ConnectionGateViewProps) {
  const { t } = useTranslation();

  if (status === 'connected') return children;

  // A device without a name is outside the tailnet and cannot claim.
  if (status === 'error' && failure === 'not_admin' && device?.claimable && device.node) {
    return <ClaimScreen node={device.node} claim={claim} onClaimed={onClaimed} />;
  }

  if (status === 'error' && failure === 'not_admin' && device) {
    return (
      <RequestAccessScreen
        requested={device.requested}
        proxy={device.proxy}
        defaultName={defaultName ?? browserDeviceName()}
        request={requestAccess}
        onCheck={onRetry}
      />
    );
  }

  if (status === 'error') {
    const key = failure ?? 'unreachable';
    const body =
      key === 'not_admin'
        ? t('connection_gate.not_admin_body_unnamed')
        : t(`connection_gate.${key}_body`);
    return (
      <div className="connection-gate" role="alert">
        <div className="connection-gate-card">
          <h1 className="connection-gate-title">{t(`connection_gate.${key}_title`)}</h1>
          <p className="connection-gate-body">{body}</p>
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
  const device = useAuthStore((state) => state.connectionFailureDevice);
  const connect = useAuthStore((state) => state.connect);

  useEffect(() => {
    if (useAuthStore.getState().connectionStatus === 'disconnected') {
      void connect();
    }
  }, [connect]);

  return (
    <ConnectionGateView
      status={status}
      failure={failure}
      device={device}
      onRetry={() => void connect()}
      onClaimed={() => window.location.reload()}
    >
      {children}
    </ConnectionGateView>
  );
}
