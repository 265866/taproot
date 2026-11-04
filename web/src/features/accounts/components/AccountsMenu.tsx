import { useId, type KeyboardEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { IconEllipsis } from '@/components/ui/icons';
import { usePopover } from './usePopover';
import styles from './AccountsMenu.module.scss';

export interface AccountsMenuItem {
  key: string;
  label: string;
  danger?: boolean;
  disabled?: boolean;
  /** Starts a new group with a divider above it. */
  divided?: boolean;
  onSelect: () => void;
}

/** The page's overflow menu: less frequent and bulk actions, each confirming its own effect. */
export function AccountsMenu({
  items,
  defaultOpen = false,
}: {
  items: AccountsMenuItem[];
  /** Starts with the menu open. */
  defaultOpen?: boolean;
}) {
  const { t } = useTranslation();
  const menuId = useId();
  const { open, setOpen, close, rootRef, triggerRef, panelRef } = usePopover(defaultOpen);

  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return;
    event.preventDefault();
    const buttons = Array.from(
      panelRef.current?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? []
    );
    const index = buttons.indexOf(document.activeElement as HTMLButtonElement);
    const next = event.key === 'ArrowDown' ? index + 1 : index - 1;
    buttons[(next + buttons.length) % buttons.length]?.focus();
  };

  return (
    <div className={styles.root} ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className={styles.trigger}
        aria-label={t('accounts.menu.label')}
        aria-haspopup="menu"
        aria-expanded={open}
        aria-controls={open ? menuId : undefined}
        onClick={() => setOpen((value) => !value)}
      >
        <IconEllipsis size={18} />
      </button>
      {open && (
        <div
          id={menuId}
          ref={panelRef}
          role="menu"
          aria-label={t('accounts.menu.label')}
          className={styles.menu}
          onKeyDown={handleKeyDown}
        >
          {items.map((item) => (
            <button
              key={item.key}
              type="button"
              role="menuitem"
              className={styles.item}
              data-danger={item.danger || undefined}
              data-divided={item.divided || undefined}
              disabled={item.disabled}
              onClick={() => {
                close(true);
                item.onSelect();
              }}
            >
              {item.label}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
