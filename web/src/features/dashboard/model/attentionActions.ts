import type { AttentionItem } from '@/services/api/taprootStatus';
import { accessLink, accountsLink, appLogLink, settingsLink, signInLink } from './links';

/**
 * What an item's fix button does: open a page (a hash route), run inline (enable an account or back
 * up now), open an outside page, or, for the app log while file logging is off, say where to look
 * instead of linking to an empty tab.
 */
export type ItemAction =
  | { kind: 'link'; to: string; labelKey: string }
  | { kind: 'enable'; authIndex: string; labelKey: string }
  | { kind: 'backup'; labelKey: string }
  | { kind: 'external'; href: string; labelKey: string }
  | { kind: 'note'; textKey: string }
  | { kind: 'none' };

const isHttpsUrl = (value: string) => {
  try {
    return new URL(value).protocol === 'https:';
  } catch {
    return false;
  }
};

/**
 * Maps an attention item's action to the button the rail shows. `loggingToFile` is the server's
 * `logging-to-file` setting; null while the config has not loaded, which reads as off, because a
 * link to an empty tab is worse than a pointer to the real log.
 */
export function itemAction(item: AttentionItem, loggingToFile: boolean | null): ItemAction {
  const { type, target } = item.action;
  switch (type) {
    case 'sign_in':
      return target
        ? { kind: 'link', to: signInLink(target), labelKey: 'dashboard.needs.action_sign_in' }
        : { kind: 'none' };
    case 'enable':
      return target
        ? { kind: 'enable', authIndex: target, labelKey: 'dashboard.needs.action_enable' }
        : { kind: 'none' };
    case 'review_access':
      return {
        kind: 'link',
        to: accessLink(target || undefined),
        labelKey: 'dashboard.needs.action_review',
      };
    case 'backup_now':
      return { kind: 'backup', labelKey: 'dashboard.needs.action_backup' };
    case 'open_settings':
      return {
        kind: 'link',
        to: settingsLink(target || 'alerts'),
        labelKey: 'dashboard.needs.action_settings',
      };
    case 'open_accounts':
      return {
        kind: 'link',
        to: accountsLink(target || undefined),
        labelKey: 'dashboard.needs.action_accounts',
      };
    case 'open_logs':
      return loggingToFile
        ? { kind: 'link', to: appLogLink(), labelKey: 'dashboard.needs.action_logs' }
        : { kind: 'note', textKey: 'dashboard.needs.read_server_log' };
    case 'docs':
      return isHttpsUrl(target)
        ? { kind: 'external', href: target, labelKey: 'dashboard.needs.action_docs' }
        : { kind: 'none' };
    case 'unknown':
      return { kind: 'none' };
    default: {
      const exhaustive: never = type;
      void exhaustive;
      return { kind: 'none' };
    }
  }
}

/** The muted word before an item's age: what it is about. */
export function itemCategoryKey(item: AttentionItem): string {
  switch (item.subject.type) {
    case 'account':
      return 'dashboard.needs.category_account';
    case 'provider':
      return 'dashboard.needs.category_pool';
    case 'device':
      return 'dashboard.needs.category_access_request';
    case 'access':
      return 'dashboard.needs.category_access';
    case 'backups':
      return 'dashboard.needs.category_backups';
    case 'alerts':
      return 'dashboard.needs.category_alerts';
    case 'tailscaled':
      return 'dashboard.needs.category_tailscale';
    default:
      return 'dashboard.needs.category_other';
  }
}
