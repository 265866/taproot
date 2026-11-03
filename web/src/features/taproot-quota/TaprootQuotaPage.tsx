import { useTranslation } from 'react-i18next';
import { Link } from 'react-router-dom';
import { PlaceholderPage } from '@/components/common/PlaceholderPage';
import { ROUTES } from '@/router/routes';

export function TaprootQuotaPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage title={t('taproot_quota.title')} description={t('taproot_quota.placeholder')}>
      <Link to={ROUTES.legacyQuota}>{t('taproot_quota.legacy_link')}</Link>
    </PlaceholderPage>
  );
}
