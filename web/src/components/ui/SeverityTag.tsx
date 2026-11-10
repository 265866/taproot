import { useTranslation } from 'react-i18next';
import { SEVERITY_TONE, type Severity } from './status';
import styles from './SeverityTag.module.scss';

/** A small word tag that says how urgent an item is: Blocking, Waiting, Overdue, At risk, OK. */
export function SeverityTag({ severity, className }: { severity: Severity; className?: string }) {
  const { t } = useTranslation();
  return (
    <span
      className={[styles.tag, styles[SEVERITY_TONE[severity]], className].filter(Boolean).join(' ')}
      data-severity={severity}
      data-tone={SEVERITY_TONE[severity]}
    >
      {t(`console.severity.${severity}`)}
    </span>
  );
}
