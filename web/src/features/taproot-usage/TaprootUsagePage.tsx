import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@/components/common/PlaceholderPage';

export function TaprootUsagePage() {
  const { t } = useTranslation();
  return <PlaceholderPage title={t('taproot_usage.title')} description={t('taproot_usage.placeholder')} />;
}
