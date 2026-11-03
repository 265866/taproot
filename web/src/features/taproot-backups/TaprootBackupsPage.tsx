import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@/components/common/PlaceholderPage';

export function TaprootBackupsPage() {
  const { t } = useTranslation();
  return <PlaceholderPage title={t('taproot_backups.title')} description={t('taproot_backups.placeholder')} />;
}
