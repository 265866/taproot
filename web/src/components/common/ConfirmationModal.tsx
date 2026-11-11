import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal } from '@/components/ui/Modal';
import { Button } from '@/components/ui/Button';
import { useNotificationStore } from '@/stores';
import { useEmailMask } from '@/hooks/useEmailMask';

export function ConfirmationModal() {
  const { t } = useTranslation();
  const { mask } = useEmailMask();
  const confirmation = useNotificationStore((state) => state.confirmation);
  const hideConfirmation = useNotificationStore((state) => state.hideConfirmation);
  const setConfirmationLoading = useNotificationStore((state) => state.setConfirmationLoading);

  const { isOpen, isLoading, options: currentOptions } = confirmation;
  // The store drops the options on close; keep showing the last ones while the dialog animates
  // out, so the Modal stays mounted and returns focus to what opened it.
  const [lastOptions, setLastOptions] = useState(currentOptions);
  if (currentOptions && currentOptions !== lastOptions) setLastOptions(currentOptions);
  const options = currentOptions ?? lastOptions;

  if (!options) {
    return null;
  }

  const {
    title,
    message,
    onConfirm,
    onCancel,
    confirmText,
    cancelText,
    variant = 'primary',
  } = options;

  // Clicks on the closing dialog must not act on choices already made.
  const handleConfirm = async () => {
    if (!isOpen) return;
    try {
      setConfirmationLoading(true);
      await onConfirm();
      hideConfirmation();
    } catch (error) {
      console.error('Confirmation action failed:', error);
      // Optional: show error notification here if needed,
      // but usually the calling component handles specific errors.
    } finally {
      setConfirmationLoading(false);
    }
  };

  const handleCancel = () => {
    if (!isOpen || isLoading) {
      return;
    }
    if (onCancel) {
      onCancel();
    }
    hideConfirmation();
  };

  return (
    <Modal open={isOpen} onClose={handleCancel} title={title} closeDisabled={isLoading}>
      {typeof message === 'string' ? (
        <p style={{ margin: '1rem 0' }}>{mask(message)}</p>
      ) : (
        <div style={{ margin: '1rem 0' }}>{message}</div>
      )}
      <div className="confirmation-actions">
        <Button variant="ghost" onClick={handleCancel} disabled={isLoading}>
          {cancelText || t('common.cancel')}
        </Button>
        <Button variant={variant} onClick={handleConfirm} loading={isLoading}>
          {confirmText || t('common.confirm')}
        </Button>
      </div>
    </Modal>
  );
}
