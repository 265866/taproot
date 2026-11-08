import { useTranslation } from 'react-i18next';
import { IconDownload } from '@/components/ui/icons';
import { taprootApi, type RequestLogBlob } from '@/services/api/taproot';
import { formatBytes } from '../model/format';
import styles from './BlobRef.module.scss';

const FORMAT: Record<NonNullable<RequestLogBlob['type']>, string> = {
  'image/jpeg': 'JPEG',
  'image/png': 'PNG',
  'image/gif': 'GIF',
  'image/webp': 'WebP',
};

/**
 * One payload the detail left out. An image shows a thumbnail, fetched once this renders (inside
 * an open section and an expanded JSON node) and cached by the browser; anything else links to
 * its download.
 */
export function BlobRef({ hash, blob }: { hash: string; blob: RequestLogBlob }) {
  const { t } = useTranslation();
  const url = taprootApi.requestLogBlobUrl(hash);
  const size = formatBytes(blob.bytes);
  if (blob.type) {
    const label = t('taproot_logs.blob.image', { format: FORMAT[blob.type], size });
    return (
      <a
        className={styles.blob}
        href={url}
        target="_blank"
        rel="noopener noreferrer"
        title={t('taproot_logs.blob.open_image')}
      >
        <img className={styles.thumb} src={url} alt={label} loading="lazy" decoding="async" />
        <span className={styles.caption}>{label}</span>
      </a>
    );
  }
  return (
    <a className={styles.blob} href={url} download title={t('taproot_logs.blob.download')}>
      <IconDownload size={12} aria-hidden="true" />
      <span className={styles.caption}>{t('taproot_logs.blob.binary', { size })}</span>
    </a>
  );
}
