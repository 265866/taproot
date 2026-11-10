import { useTranslation } from 'react-i18next';

export function LoadingSpinner({
  size = 20,
  className = '',
}: {
  size?: number;
  className?: string;
}) {
  const { t } = useTranslation();
  return (
    <div
      className={`loading-spinner${className ? ` ${className}` : ''}`}
      style={{ width: size, height: size, borderWidth: size / 7 }}
      role="status"
      aria-live="polite"
    >
      <span className="visually-hidden">{t('common.loading')}</span>
    </div>
  );
}
