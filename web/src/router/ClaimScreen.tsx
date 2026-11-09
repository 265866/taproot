import { useState, type FormEvent, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { TaprootMark } from '@/components/common/TaprootMark';
import { useEmailMask } from '@/hooks/useEmailMask';
import { CLAIM_CODE_LENGTH, formatClaimInput, submitClaim } from './claimCode';

const SLOT = '\u0000';

/** Puts `node` where the slot was in a translated sentence, so server text is never markup. */
function withSlot(sentence: string, node: ReactNode) {
  const [before, ...rest] = sentence.split(SLOT);
  return (
    <>
      {before}
      {node}
      {rest.join('')}
    </>
  );
}

interface ClaimScreenProps {
  /** The visiting device's name, as the guard names it. */
  node: string;
  claim: (code: string) => Promise<unknown>;
  onClaimed: () => void;
}

/**
 * The connection gate while the server is unclaimed: no device has Admin, and the visiting
 * tailnet device can become the first admin with the setup code from taproot's log.
 */
export function ClaimScreen({ node, claim, onClaimed }: ClaimScreenProps) {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const [code, setCode] = useState('');
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const complete = code.replace('-', '').length === CLAIM_CODE_LENGTH;

  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!complete || pending) return;
    setPending(true);
    setError(null);
    const failure = await submitClaim(code, claim, t);
    if (failure === null) {
      onClaimed();
      return;
    }
    setError(failure);
    setPending(false);
  };

  return (
    <div className="connection-gate">
      <form className="connection-gate-card" onSubmit={(event) => void submit(event)}>
        <TaprootMark className="connection-gate-mark" />
        <h1 className="connection-gate-title">{t('connection_gate.claim_title')}</h1>
        <p className="connection-gate-body">
          {withSlot(t('connection_gate.claim_body', { node: SLOT }), <strong>{node}</strong>)}
        </p>
        <input
          className="input connection-gate-code"
          value={code}
          onChange={(event) => setCode(formatClaimInput(event.target.value))}
          placeholder="XXXX-XXXX"
          aria-label={t('connection_gate.claim_code_label')}
          aria-invalid={error ? true : undefined}
          autoComplete="off"
          autoCapitalize="characters"
          spellCheck={false}
          maxLength={CLAIM_CODE_LENGTH + 1}
          autoFocus
        />
        {error && (
          <p className="connection-gate-error" role="alert">
            {mask(error)}
          </p>
        )}
        <Button type="submit" disabled={!complete} loading={pending}>
          {t('connection_gate.claim_submit')}
        </Button>
        <p className="connection-gate-hint">
          {withSlot(
            t('connection_gate.claim_hint', { command: SLOT }),
            <code>podman logs taproot</code>
          )}
        </p>
      </form>
    </div>
  );
}
