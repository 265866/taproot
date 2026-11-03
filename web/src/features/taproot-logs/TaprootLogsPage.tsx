import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { PlaceholderPage } from '@/components/common/PlaceholderPage';
import { ROUTES } from '@/router/routes';

export function TaprootLogsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('taproot_logs.title')} description={t('taproot_logs.placeholder')}>
      <Link to={ROUTES.legacyLogs}>{t('taproot_logs.legacy_link')}</Link>
    </PlaceholderPage>
  );
}
