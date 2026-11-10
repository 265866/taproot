import { useId, useState } from 'react';
import { Link } from 'react-router-dom';
import { useTranslation } from 'react-i18next';
import { TaprootMark } from '@/components/common/TaprootMark';
import { IconArrowRight, IconCheck } from '@/components/ui/icons';
import { copyToClipboard } from '@/utils/clipboard';
import {
  addressHint,
  claudeCodeSnippet,
  codexSnippet,
  setupSteps,
  type FirstRunFacts,
  type SetupStep,
} from '../model/firstRun';
import { accessLink, accountsLink, signInLink } from '../model/links';
import { providerColor } from '../model/providers';
import styles from './FirstRun.module.scss';

interface FirstRunProps {
  facts: FirstRunFacts;
  /** The guard is off: every caller already reaches the dashboard. */
  guardOff: boolean;
  /** The server's address as this browser reaches it. */
  baseUrl: string;
  /** A caveat for that address; see `addressHint`. */
  hint: ReturnType<typeof addressHint>;
}

/** The main column before the first account and request: three steps that tick themselves off. */
export function FirstRun({ facts, guardOff, baseUrl, hint }: FirstRunProps) {
  const { t } = useTranslation();
  const headingId = useId();
  const steps = setupSteps(facts);

  return (
    <section className={styles.firstRun} aria-labelledby={headingId}>
      <div className={styles.header}>
        <span className={styles.mark} aria-hidden="true">
          <TaprootMark />
        </span>
        <div>
          <h2 id={headingId} className={styles.title}>
            {t('dashboard.setup.title')}
          </h2>
          <p className={styles.lede}>{t('dashboard.setup.lede')}</p>
        </div>
      </div>
      <ol className={styles.steps} aria-label={t('dashboard.setup.steps_label')}>
        {steps.map((step, index) => (
          <Step key={step.id} step={step} number={index + 1}>
            {step.id === 'account' ? <AccountStep done={step.state === 'done'} /> : null}
            {step.id === 'client' ? (
              <ClientStep done={step.state === 'done'} baseUrl={baseUrl} hint={hint} />
            ) : null}
            {step.id === 'devices' ? (
              <DevicesStep done={step.state === 'done'} guardOff={guardOff} />
            ) : null}
          </Step>
        ))}
      </ol>
    </section>
  );
}

function Step({
  step,
  number,
  children,
}: {
  step: SetupStep;
  number: number;
  children: React.ReactNode;
}) {
  const { t } = useTranslation();
  const done = step.state === 'done';
  return (
    <li
      className={styles.step}
      data-state={step.state}
      aria-current={step.state === 'current' ? 'step' : undefined}
    >
      <span className={styles.number} aria-hidden="true">
        {done ? <IconCheck size={14} strokeWidth={3} /> : number}
      </span>
      <div className={styles.stepBody}>
        <h3 className={styles.stepTitle}>
          {t(`dashboard.setup.${step.id}_title`)}
          <span className={styles.pill} data-done={done}>
            {t(done ? 'dashboard.setup.done' : 'dashboard.setup.pending')}
          </span>
        </h3>
        {children}
      </div>
    </li>
  );
}

function AccountStep({ done }: { done: boolean }) {
  const { t } = useTranslation();
  if (done) {
    return (
      <>
        <p className={styles.text}>{t('dashboard.setup.account_done')}</p>
        <div className={styles.actions}>
          <Link className={styles.arrowLink} to={accountsLink()}>
            {t('dashboard.setup.open_accounts')}
            <IconArrowRight size={12} />
          </Link>
        </div>
      </>
    );
  }
  return (
    <>
      <p className={styles.text}>
        {t('dashboard.setup.account_text_before')}
        <code>localhost</code>
        {t('dashboard.setup.account_text_after')}
      </p>
      <div className={styles.actions}>
        <Link className="btn btn-primary btn-sm" to={signInLink('claude')}>
          <span className={styles.dot} style={{ background: providerColor('claude') }} />
          {t('dashboard.setup.sign_in_claude')}
        </Link>
        <Link className="btn btn-secondary btn-sm" to={signInLink('codex')}>
          <span className={styles.dot} style={{ background: providerColor('codex') }} />
          {t('dashboard.setup.sign_in_codex')}
        </Link>
      </div>
    </>
  );
}

function ClientStep({
  done,
  baseUrl,
  hint,
}: {
  done: boolean;
  baseUrl: string;
  hint: FirstRunProps['hint'];
}) {
  const { t } = useTranslation();
  return (
    <>
      <p className={styles.text}>
        {t(done ? 'dashboard.setup.client_done' : 'dashboard.setup.client_text')}
      </p>
      {hint ? <p className={styles.hint}>{t(`dashboard.setup.address_${hint}`)}</p> : null}
      <div className={styles.snippets}>
        <Snippet title={t('dashboard.setup.claude_code_title')} code={claudeCodeSnippet(baseUrl)} />
        <Snippet title={t('dashboard.setup.codex_title')} code={codexSnippet(baseUrl)} />
      </div>
    </>
  );
}

function DevicesStep({ done, guardOff }: { done: boolean; guardOff: boolean }) {
  const { t } = useTranslation();
  return (
    <>
      <p className={styles.text}>
        {t(
          guardOff
            ? 'dashboard.setup.devices_guard_off'
            : done
              ? 'dashboard.setup.devices_done'
              : 'dashboard.setup.devices_text'
        )}
      </p>
      {guardOff ? null : (
        <div className={styles.actions}>
          <Link className={styles.arrowLink} to={accessLink()}>
            {t('dashboard.setup.open_access')}
            <IconArrowRight size={12} />
          </Link>
        </div>
      )}
    </>
  );
}

function Snippet({ title, code }: { title: string; code: string }) {
  const { t } = useTranslation();
  const [copied, setCopied] = useState<boolean | null>(null);
  return (
    <figure className={styles.snippet}>
      <figcaption className={styles.snippetHead}>
        <span>{title}</span>
        <button
          type="button"
          className={`btn btn-secondary btn-sm ${styles.copy}`}
          aria-label={t('dashboard.setup.copy_label', { title })}
          onClick={() => void copyToClipboard(code).then(setCopied)}
        >
          {t('dashboard.setup.copy')}
        </button>
        <span className={styles.copied} role="status">
          {copied === true
            ? t('dashboard.setup.copied')
            : copied === false
              ? t('dashboard.setup.copy_failed')
              : ''}
        </span>
      </figcaption>
      <pre className={styles.code}>{code}</pre>
    </figure>
  );
}
