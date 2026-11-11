import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { LoadingSpinner } from '@/components/ui/LoadingSpinner';
import { IconPlus, IconUpload } from '@/components/ui/icons';
import { OAuthFlowDialog, type DialogHeading } from '@/features/oauth/components/OAuthFlowDialog';
import { ProviderTile } from '@/features/oauth/components/ProviderTile';
import {
  useOAuthFlows,
  usePluginOAuthProviders,
  type ProviderFlowState,
} from '@/features/oauth/hooks/useOAuthFlows';
import {
  OAUTH_PROVIDERS,
  startCard,
  resolveThemedIcon,
  supportsManualCallback,
  type OAuthProviderCard,
} from '@/features/oauth/providers';
import { useAuthStore, useThemeStore } from '@/stores';
import { moveFocusWithArrows, usePopover } from '@/hooks/usePopover';
import styles from './AddAccount.module.scss';

interface AddAccountProps {
  uploading: boolean;
  /** Starts with the panel open. */
  defaultOpen?: boolean;
  /** Controlled panel state, so the page's empty state can open it. */
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
  /** Called after a sign-in adds a credential. */
  onAdded?: () => void;
  disableControls: boolean;
  onUpload: () => void;
  /**
   * A credential provider key (`claude`, `codex`) whose sign-in opens at once, as a click on its
   * tile would; links such as the Dashboard's "Sign in again" set it. Called back once handled.
   */
  startProvider?: string | null;
  onStartHandled?: () => void;
  /** Phones: the button reads "Add"; its name stays "Add account". */
  compact?: boolean;
}

/**
 * Add account: a panel of provider sign-ins plus a credential upload. A provider opens its
 * sign-in dialog; closing the dialog does not stop the sign-in, and the tile shows its progress.
 */
export function AddAccount({
  uploading,
  defaultOpen = false,
  open: openProp,
  onOpenChange,
  onAdded,
  disableControls,
  onUpload,
  startProvider,
  onStartHandled,
  compact = false,
}: AddAccountProps) {
  const { t } = useTranslation();
  const apiBase = useAuthStore((state) => state.apiBase);
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const panelId = useId();
  const { open, setOpen, close, rootRef, triggerRef, panelRef } = usePopover({
    defaultOpen,
    open: openProp,
    onOpenChange,
  });

  const pluginProviders = usePluginOAuthProviders(apiBase);
  const providerCards = useMemo<OAuthProviderCard[]>(
    () => [...OAUTH_PROVIDERS, ...pluginProviders],
    [pluginProviders]
  );

  const focusedRef = useRef<string | null>(null);
  // The last opened provider stays mounted after closing so the exit animation keeps its content.
  const [dialogId, setDialogId] = useState<string | null>(null);
  const [dialogOpen, setDialogOpen] = useState(false);

  const flows = useOAuthFlows({
    providerCards,
    isFocused: (provider) => focusedRef.current === provider,
  });

  const { addedCount } = flows;
  useEffect(() => {
    if (addedCount > 0) onAdded?.();
  }, [addedCount, onAdded]);

  const labelOf = (card: OAuthProviderCard) =>
    card.kind === 'plugin'
      ? card.title
      : typeof card.label === 'string'
        ? card.label
        : t(card.label.key);

  const captionOf = (card: OAuthProviderCard) => {
    if (card.kind === 'plugin') return t('auth_login.method_plugin');
    const method = t(
      card.flow === 'device' ? 'auth_login.method_device' : 'auth_login.method_browser'
    );
    return card.domain ? `${card.domain} · ${method}` : method;
  };

  const iconOf = (card: OAuthProviderCard) =>
    card.kind === 'plugin' ? card.icon : resolveThemedIcon(card.icon, resolvedTheme);

  const headingOf = (card: OAuthProviderCard): DialogHeading => ({
    title:
      card.kind === 'plugin'
        ? t('auth_login.plugin_oauth_title', { name: card.title })
        : t(card.titleKey),
    caption: captionOf(card),
    icon: iconOf(card),
  });

  const tileStatusLabel = (status: ProviderFlowState['status']) =>
    status === 'waiting'
      ? t('auth_login.tile_waiting')
      : status === 'success'
        ? t('auth_login.tile_success')
        : status === 'error'
          ? t('auth_login.tile_error')
          : undefined;

  const openProvider = (card: OAuthProviderCard) => {
    close(false);
    focusedRef.current = card.id;
    setDialogId(card.id);
    setDialogOpen(true);
    // A running, failed, or just-finished attempt reopens as it is; otherwise sign-in starts now.
    if (!flows.states[card.id]?.status) void flows.startAuth(card.id);
  };

  useEffect(() => {
    if (!startProvider) return;
    const card = startCard(startProvider, providerCards);
    // A provider without a sign-in card here opens the panel, so the user can pick another way.
    if (card) openProvider(card);
    else setOpen(true);
    onStartHandled?.();
    // openProvider and the cards change identity each render; the start runs once per request.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [startProvider]);

  const closeDialog = () => {
    if (dialogId) flows.dismissSuccess(dialogId);
    focusedRef.current = null;
    setDialogOpen(false);
  };

  const dialogCard = dialogId ? providerCards.find((card) => card.id === dialogId) : undefined;
  const waiting = Object.values(flows.states).filter((state) => state.status === 'waiting').length;

  return (
    <div className={styles.root} ref={rootRef}>
      <button
        ref={triggerRef}
        type="button"
        className={`btn btn-primary btn-sm ${styles.trigger}`}
        aria-expanded={open}
        aria-controls={open ? panelId : undefined}
        aria-label={compact ? t('accounts.add.button') : undefined}
        onClick={() => setOpen(!open)}
      >
        <IconPlus size={15} />
        {t(compact ? 'accounts.add.button_short' : 'accounts.add.button')}
        {waiting > 0 && (
          <span className={styles.badge} aria-label={t('accounts.add.waiting', { count: waiting })}>
            {waiting}
          </span>
        )}
      </button>
      {open && (
        <div
          id={panelId}
          ref={panelRef}
          className={styles.panel}
          role="group"
          aria-label={t('accounts.add.title')}
          onKeyDown={moveFocusWithArrows}
        >
          <p className={styles.heading}>{t('accounts.add.oauth')}</p>
          <div className={styles.grid}>
            {providerCards.map((card, index) => {
              const status = flows.states[card.id]?.status;
              return (
                <ProviderTile
                  key={card.id}
                  label={labelOf(card)}
                  caption={captionOf(card)}
                  icon={iconOf(card)}
                  index={index}
                  status={status}
                  statusLabel={tileStatusLabel(status)}
                  onOpen={() => openProvider(card)}
                />
              );
            })}
          </div>
          <p className={styles.heading}>{t('accounts.add.file')}</p>
          <button
            type="button"
            className={styles.upload}
            disabled={disableControls || uploading}
            onClick={() => {
              close(true);
              onUpload();
            }}
          >
            <span className={styles.uploadGlyph} aria-hidden="true">
              {uploading ? <LoadingSpinner size={16} /> : <IconUpload size={18} />}
            </span>
            <span className={styles.uploadText}>
              <span className={styles.uploadLabel}>{t('accounts.add.upload')}</span>
              <span className={styles.uploadCaption}>{t('accounts.add.upload_hint')}</span>
            </span>
          </button>
        </div>
      )}

      {dialogCard && (
        <OAuthFlowDialog
          open={dialogOpen}
          providerId={dialogCard.id}
          heading={headingOf(dialogCard)}
          state={flows.states[dialogCard.id] ?? {}}
          text={(suffix) => flows.providerText(dialogCard.id, suffix)}
          supportsCallback={supportsManualCallback(dialogCard)}
          callbackRequired={dialogCard.kind === 'builtin'}
          onStart={() => void flows.startAuth(dialogCard.id)}
          onCallbackChange={(value) => flows.setCallbackInput(dialogCard.id, value)}
          onCallbackSubmit={() => void flows.submitCallback(dialogCard.id)}
          onDone={closeDialog}
          onClose={closeDialog}
        />
      )}
    </div>
  );
}
