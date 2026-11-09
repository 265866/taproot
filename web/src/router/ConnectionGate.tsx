import { useEffect, type ReactElement } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { taprootApi } from '@/services/api/taproot';
import { useAuthStore } from '@/stores';
import type { ConnectionFailure, ConnectionStatus, DeniedDevice } from '@/types';
import { ClaimScreen } from './ClaimScreen';

const NAME_SLOT = '\u0000';

/**
 * Renders a translated sentence with the device name in bold where the slot was. The name comes
 * from the server, so it is rendered as text, never parsed as markup.
 */
function withStrongName(sentence: string, name: string) {
  const [before, ...rest] = sentence.split(NAME_SLOT);
  return (
    <>
      {before}
      <strong>{name}</strong>
      {rest.join(name)}
    </>
  );
}

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
  children: ReactElement;
}

export function ConnectionGateView({
  status,
  failure,
  device = null,
  onRetry,
  claim = taprootApi.claim,
  onClaimed = onRetry,
  children,
}: ConnectionGateViewProps) {
  const { t } = useTranslation();

  if (status === 'connected') return children;

  if (status === 'error' && failure === 'not_admin' && device?.claimable) {
    return <ClaimScreen node={device.node} claim={claim} onClaimed={onClaimed} />;
  }

  if (status === 'error') {
    const key = failure ?? 'unreachable';
    const named = key === 'not_admin' && device;
    const body = named
      ? withStrongName(
          t(device.proxy ? 'connection_gate.not_admin_body' : 'connection_gate.no_access_body', {
            node: NAME_SLOT,
          }),
          device.node
        )
      : key === 'not_admin'
        ? t('connection_gate.not_admin_body_unnamed')
        : t(`connection_gate.${key}_body`);
    return (
      <div className="connection-gate" role="alert">
        <div className="connection-gate-card">
          <h1 className="connection-gate-title">{t(`connection_gate.${key}_title`)}</h1>
          <p className="connection-gate-body">{body}</p>
          {named && (
            <dl className="connection-gate-device">
              <dt>{t('connection_gate.this_device')}</dt>
              <dd>{[device.node, device.login].filter(Boolean).join(' · ')}</dd>
              {device.nodeId && (
                <dd>
                  <code>{device.nodeId}</code>
                </dd>
              )}
            </dl>
          )}
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
