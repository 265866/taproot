import { useId, useState, type FormEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { TaprootMark } from '@/components/common/TaprootMark';
import { useEmailMask } from '@/hooks/useEmailMask';
import { submitAccessRequest } from './accessRequest';

interface RequestAccessScreenProps {
  /** The device already has an open request, so the screen starts in its sent state. */
  requested: boolean;
  /** The device can already use the proxy, so it asks for the dashboard. */
  proxy: boolean;
  /** The name to start the form with. */
  defaultName: string;
  request: (name: string) => Promise<unknown>;
  /** Connects again, which opens the dashboard once the request was allowed. */
  onCheck: () => void;
}

/**
 * The connection gate for a tailnet device without Admin on a claimed server: it asks the
 * server's admin for access, or for the dashboard when it has Proxy, and may name itself. The
 * request grants nothing.
 */
export function RequestAccessScreen({
  requested,
  proxy,
  defaultName,
  request,
  onCheck,
}: RequestAccessScreenProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const nameId = useId();
  const [sent, setSent] = useState(requested);
  const [name, setName] = useState(defaultName);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (pending) return;
    setPending(true);
    setError(null);
    const failure = await submitAccessRequest(name, request, t);
    setPending(false);
    if (failure === null) setSent(true);
    else setError(failure);
  };

  if (sent) {
    return (
      <div className="connection-gate">
        <div className="connection-gate-card" role="status">
          <TaprootMark className="connection-gate-mark" />
          <h1 className="connection-gate-title">{t('connection_gate.request_sent')}</h1>
          <Button onClick={onCheck}>{t('connection_gate.request_check')}</Button>
          <Button variant="ghost" onClick={() => setSent(false)}>
            {t('connection_gate.request_rename')}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <div className="connection-gate">
      <form className="connection-gate-card" onSubmit={(event) => void submit(event)}>
        <TaprootMark className="connection-gate-mark" />
        <h1 className="connection-gate-title">
          {t(proxy ? 'connection_gate.request_dashboard_title' : 'connection_gate.request_title')}
        </h1>
        <label htmlFor={nameId} className="connection-gate-body">
          {t('connection_gate.request_name_label')}
        </label>
        <input
          id={nameId}
          className="input"
          value={name}
          onChange={(event) => setName(event.target.value)}
          autoComplete="off"
          maxLength={64}
          aria-invalid={error ? true : undefined}
        />
        {error && (
          <p className="connection-gate-error" role="alert">
            {mask(error)}
          </p>
        )}
        <Button type="submit" loading={pending}>
          {t(proxy ? 'connection_gate.request_dashboard_submit' : 'connection_gate.request_submit')}
        </Button>
      </form>
    </div>
  );
}
