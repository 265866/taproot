import { useTranslation } from 'react-i18next';
import { PlaceholderPage } from '@/components/common/PlaceholderPage';

export function TaprootSettingsPage() {
  const { t } = useTranslation();
  return (
    <PlaceholderPage
      title={t('taproot_settings.title')}
      description={t('taproot_settings.placeholder')}
    />
  );
}
