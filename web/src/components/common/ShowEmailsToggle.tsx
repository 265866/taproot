import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/Button';
import { IconEye, IconEyeOff } from '@/components/ui/icons';
import { usePrivacyStore, useShowEmails } from '@/stores/usePrivacyStore';
import styles from './ShowEmailsToggle.module.scss';

/** Shows or hides account emails on every page at once, for this session only. */
export function ShowEmailsToggle() {
  const { t } = useTranslation();
  const showEmails = useShowEmails();
  const toggleShowEmails = usePrivacyStore((state) => state.toggleShowEmails);
  return (
    <Button
      variant="secondary"
      size="sm"
      className={styles.toggle}
      aria-pressed={showEmails}
      onClick={toggleShowEmails}
    >
      {showEmails ? <IconEyeOff size={15} /> : <IconEye size={15} />}
      {t(showEmails ? 'common.hide_emails' : 'common.show_emails')}
    </Button>
  );
}
