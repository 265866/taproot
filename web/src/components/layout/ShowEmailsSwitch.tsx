import type { FocusEvent, MouseEvent } from 'react';
import { useTranslation } from 'react-i18next';
import { IconEye, IconEyeOff } from '@/components/ui/icons';
import { usePrivacyStore, useShowEmails } from '@/stores/usePrivacyStore';

interface ShowEmailsSwitchProps {
  /** The collapsed sidebar's icon button; its name and state come from the tooltip and ARIA. */
  iconOnly: boolean;
  describedBy?: string;
  onMouseEnter?: (event: MouseEvent<HTMLElement>) => void;
  onMouseLeave?: () => void;
  onFocus?: (event: FocusEvent<HTMLElement>) => void;
  onBlur?: (event: FocusEvent<HTMLElement>) => void;
}

/**
 * The one switch that shows or hides account emails on every page. It lives in the sidebar's
 * footer and starts off on every page load (see `usePrivacyStore`).
 */
export function ShowEmailsSwitch({ iconOnly, describedBy, ...handlers }: ShowEmailsSwitchProps) {
  const { t } = useTranslation();
  const showEmails = useShowEmails();
  const toggleShowEmails = usePrivacyStore((state) => state.toggleShowEmails);
  const label = t('sidebar.show_emails');
  return (
    <button
      type="button"
      role="switch"
      aria-checked={showEmails}
      aria-label={iconOnly ? label : undefined}
      aria-describedby={describedBy}
      className={iconOnly ? 'sidebar-icon-button sidebar-switch-icon-only' : 'sidebar-switch'}
      onClick={toggleShowEmails}
      {...handlers}
    >
      <span className="sidebar-switch-glyph" aria-hidden="true">
        {showEmails ? <IconEye size={16} /> : <IconEyeOff size={16} />}
      </span>
      {iconOnly ? null : (
        <>
          <span className="sidebar-switch-label">{label}</span>
          <span className="sidebar-switch-track" aria-hidden="true">
            <span className="sidebar-switch-thumb" />
          </span>
        </>
      )}
    </button>
  );
}
