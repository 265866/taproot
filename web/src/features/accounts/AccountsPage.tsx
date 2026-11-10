import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { useNavigate, useSearchParams } from 'react-router-dom';
import { readAccountsLink, withoutAccountsLink } from '@/router/deepLinks';
import { ShowEmailsToggle } from '@/components/common/ShowEmailsToggle';
import { usePageTransitionLayer } from '@/components/common/PageTransitionLayer';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { Select } from '@/components/ui/Select';
import { SelectionCheckbox } from '@/components/ui/SelectionCheckbox';
import { Skeleton } from '@/components/ui/Skeleton';
import { IconChevronLeft, IconRefreshCw } from '@/components/ui/icons';
import { invalidateAuthFileDerivedCaches } from '@/features/authFiles/cacheInvalidation';
import { AuthFileRefreshResults } from '@/features/authFiles/components/AuthFileRefreshResults';
import { BatchActionBar } from '@/features/authFiles/components/BatchActionBar';
import { ProviderTabs } from '@/features/authFiles/components/ProviderTabs';
import {
  getAuthFileStatusMessage,
  getTypeLabel,
  isRuntimeOnlyAuthFile,
  normalizeProviderKey,
} from '@/features/authFiles/constants';
import { useAuthFilesData } from '@/features/authFiles/hooks/useAuthFilesData';
import { useAuthFilesModels } from '@/features/authFiles/hooks/useAuthFilesModels';
import { useAuthFilesPrefixProxyEditor } from '@/features/authFiles/hooks/useAuthFilesPrefixProxyEditor';
import { useAuthFilesStatusBarCache } from '@/features/authFiles/hooks/useAuthFilesStatusBarCache';
import { getAuthFileRefreshKey } from '@/features/authFiles/manualRefresh';
import { Ledger } from '@/features/taproot-quota/components/Ledger';
import { ProviderCard } from '@/features/taproot-quota/components/ProviderCard';
import { Segmented } from '@/features/taproot-quota/components/Segmented';
import type { ChartView } from '@/features/taproot-quota/model/chart';
import { accountName, formatDay } from '@/features/taproot-quota/model/format';
import { liveEntriesByAuthIndex, liveEntriesByProvider } from '@/features/taproot-quota/model/live';
import {
  BURN_WINDOWS_HOURS,
  FUTURE_USAGE,
  type BurnWindowHours,
  type FutureUsage,
} from '@/features/taproot-quota/model/poolView';
import {
  defaultRange,
  isDefaultRange,
  lastDay,
  shiftRange,
  type DateRange,
} from '@/features/taproot-quota/model/range';
import { useQuotaData } from '@/features/taproot-quota/useQuotaData';
import { REFRESHABLE_PROVIDERS, useQuotaRefresh } from '@/features/taproot-quota/useQuotaRefresh';
import quotaStyles from '@/features/taproot-quota/quota.module.scss';
import { useEmailMask } from '@/hooks/useEmailMask';
import { outcomeFromError, useHeaderRefresh } from '@/hooks/useHeaderRefresh';
import { useInterval } from '@/hooks/useInterval';
import { useNow } from '@/hooks/useNow';
import { ROUTES } from '@/router/routes';
import { useAuthStore, useNotificationStore, useThemeStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { copyToClipboard } from '@/utils/clipboard';
import { statusBarDataFromRecentRequests } from '@/utils/recentRequests';
import { AccountControls } from './components/AccountControls';
import { AccountSheet } from './components/AccountSheet';
import { AccountsMenu, type AccountsMenuItem } from './components/AccountsMenu';
import { AddAccount } from './components/AddAccount';
import {
  STATUS_FILTERS,
  accountHealth,
  fileAccountKey,
  matchesAccountSearch,
  matchesStatusFilter,
  mergeAccountGroups,
  pageNow,
  type StatusFilter,
} from './model';
import styles from './AccountsPage.module.scss';

/** Credential files reload in the background this often, as Auth Files did. */
const FILES_REFRESH_MS = 240_000;

/**
 * Accounts: the pooled quota charts, then one row per account with its quota, health, and enable
 * switch. Manage opens everything else about an account; Add account signs in or uploads.
 */
export function AccountsPage() {
  const { t, i18n } = useTranslation();
  const { mask, showEmails } = useEmailMask();
  const locale = i18n.resolvedLanguage;
  const resolvedTheme = useThemeStore((state) => state.resolvedTheme);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore((state) => state.showNotification);
  const pageTransitionLayer = usePageTransitionLayer();
  const isCurrentLayer = pageTransitionLayer ? pageTransitionLayer.status === 'current' : true;
  const clock = useNow();
  const navigate = useNavigate();

  const [range, setRange] = useState<DateRange>(() => defaultRange(Date.now()));
  const [searchParams, setSearchParams] = useSearchParams();
  // `?provider=codex` (or `?signIn=codex`) opens on that provider's tab, as links from the
  // Dashboard do.
  const [tab, setTab] = useState(() => {
    const { provider } = readAccountsLink(searchParams);
    return provider ? normalizeProviderKey(provider) : 'all';
  });
  const [view, setView] = useState<ChartView>('remaining');
  const [burnHours, setBurnHours] = useState<BurnWindowHours>(3);
  const [future, setFuture] = useState<FutureUsage>('same');
  const [query, setQuery] = useState('');
  const [statusFilter, setStatusFilter] = useState<StatusFilter>('all');
  const [selecting, setSelecting] = useState(false);
  const [addOpen, setAddOpen] = useState(false);
  const titleRef = useRef<HTMLHeadingElement>(null);
  // The managed account stays set after closing so the sheet's exit animation keeps its content.
  const [manageKey, setManageKey] = useState<string | null>(null);
  const [manageOpen, setManageOpen] = useState(false);

  const {
    data: quotaData,
    loading: quotaLoading,
    error: quotaError,
    refresh: reloadQuota,
  } = useQuotaData(range);
  const quotaRefresh = useQuotaRefresh(reloadQuota);
  const models = useAuthFilesModels();
  const invalidateDerivedCaches = useCallback(
    (names?: string[]) => invalidateAuthFileDerivedCaches(models.invalidateModels, names),
    [models.invalidateModels]
  );
  const fileActions = useAuthFilesData({ onFilesMutated: invalidateDerivedCaches });
  const {
    files,
    loadFiles,
    loading: filesLoading,
    error: filesError,
    uploading,
    deletingAll,
    statusUpdating,
    manualRefreshing,
    refreshingAllCredentials,
    refreshResults,
    closeRefreshResults,
    handleRefreshAllCredentials,
    fileInputRef,
    handleUploadClick,
    handleFileChange,
    handleDeleteAll,
    handleStatusToggle,
    selectedFiles,
    selectionCount,
    toggleSelect,
    selectAllVisible,
    invertVisibleSelection,
    deselectAll,
    retainSelection,
    batchStatusUpdating,
    batchDownload,
    batchSetStatus,
    batchDelete,
    deleting,
    cooldownResetting,
    handleManualRefresh,
    handleCooldownReset,
    handleDownload,
    handleDelete,
  } = fileActions;
  const loadFilesInBackground = useCallback(() => loadFiles({ background: true }), [loadFiles]);
  const disableControls = connectionStatus !== 'connected' || refreshingAllCredentials;
  const editor = useAuthFilesPrefixProxyEditor({
    disableControls,
    loadFiles: loadFilesInBackground,
    onFilesMutated: invalidateDerivedCaches,
  });
  const statusBarCache = useAuthFilesStatusBarCache(files);

  /* ---------- loading ---------- */

  const firstLoadRef = useRef(true);
  useEffect(() => {
    if (!isCurrentLayer) return;
    void loadFiles(firstLoadRef.current ? undefined : { background: true });
    firstLoadRef.current = false;
  }, [isCurrentLayer, loadFiles]);
  useInterval(
    () => void loadFilesInBackground().catch(() => {}),
    isCurrentLayer ? FILES_REFRESH_MS : null
  );

  // Upload and delete update the list themselves; a finished sign-in does not.
  const reloadAfterSignIn = useCallback(() => {
    void loadFilesInBackground();
    void reloadQuota();
  }, [loadFilesInBackground, reloadQuota]);

  const clearDeepLink = useCallback(() => {
    setSearchParams(withoutAccountsLink, { replace: true });
  }, [setSearchParams]);

  const reloadAll = useCallback(async () => {
    const [quotaError, filesError] = await Promise.all([reloadQuota(), loadFilesInBackground()]);
    return outcomeFromError(filesError || quotaError);
  }, [loadFilesInBackground, reloadQuota]);
  useHeaderRefresh(reloadAll);

  /* ---------- the account model ---------- */

  const filesLoaded = !filesLoading && !filesError;
  const groups = useMemo(
    () => mergeAccountGroups(quotaData?.groups ?? [], files, filesLoaded),
    [quotaData, files, filesLoaded]
  );
  const fileByKey = useMemo(
    () => new Map(files.map((file) => [fileAccountKey(file), file])),
    [files]
  );
  const liveByProvider = useMemo(() => liveEntriesByProvider(files), [files]);
  const liveByAuthIndex = useMemo(() => liveEntriesByAuthIndex(files), [files]);

  const tabTypes = useMemo(() => ['all', ...groups.map((g) => g.provider)], [groups]);
  const counts = useMemo(() => {
    const out: Record<string, number> = { all: 0 };
    for (const g of groups) {
      out[g.provider] = g.accounts.length;
      out.all += g.accounts.length;
    }
    return out;
  }, [groups]);
  const activeTab = tabTypes.includes(tab) ? tab : 'all';
  const visible = activeTab === 'all' ? groups : groups.filter((g) => g.provider === activeTab);
  const allAccounts = groups.flatMap((g) => g.accounts);
  const reporting = allAccounts.filter((a) => a.windows.length > 0).length;
  const problems = allAccounts.filter((a) =>
    matchesStatusFilter('problem', a, fileByKey.get(a.authIndex))
  ).length;

  /**
   * Files of the rows on screen with a checkbox: what the ledger renders after the tab, filters,
   * search, and collapsed groups (nothing in the Cards view). Batch actions reach only these.
   */
  const [renderedKeys, setRenderedKeys] = useState<readonly string[]>([]);
  const shownFiles = useMemo(
    () =>
      renderedKeys.flatMap((key) => {
        const file = fileByKey.get(key);
        return file && !isRuntimeOnlyAuthFile(file) ? [file] : [];
      }),
    [renderedKeys, fileByKey]
  );
  // A selection never outlives its rows: hiding a row unselects it.
  useEffect(() => {
    retainSelection(shownFiles.map((file) => file.name));
  }, [retainSelection, shownFiles]);

  const statusDataFor = useCallback(
    (file: AuthFileItem | undefined) => {
      if (!file) return null;
      const key = typeof file.authIndex === 'string' ? file.authIndex : null;
      return (
        (key && statusBarCache.get(key)) ||
        statusBarDataFromRecentRequests(file.recentRequests ?? [])
      );
    },
    [statusBarCache]
  );

  /* ---------- actions ---------- */

  const refreshAll = () =>
    void quotaRefresh.refreshAll(
      groups.flatMap((g) =>
        g.accounts
          .filter((a) => !a.disabled && REFRESHABLE_PROVIDERS.has(a.provider))
          .map((a) => ({ authIndex: a.authIndex, name: accountName(a, showEmails) }))
      )
    );
  const refreshBusy = quotaRefresh.allRunning || (quotaLoading && quotaData !== null);

  const copyText = useCallback(
    async (text: string) => {
      const copied = await copyToClipboard(text);
      showNotification(
        copied ? t('notification.link_copied') : t('notification.copy_failed'),
        copied ? 'success' : 'error'
      );
    },
    [showNotification, t]
  );

  const openManage = (key: string) => {
    setManageKey(key);
    setManageOpen(true);
  };

  const tabFilter = activeTab === 'all' ? 'all' : normalizeProviderKey(activeTab);
  const scope = activeTab === 'all' ? null : getTypeLabel(t, activeTab);
  const deleteMatching = (problemOnly: boolean, disabledOnly: boolean) =>
    handleDeleteAll({
      filter: tabFilter,
      problemOnly,
      disabledOnly,
      onResetFilterToAll: () => setTab('all'),
    });
  const stopSelecting = () => {
    deselectAll();
    setSelecting(false);
  };
  const clearFilters = () => {
    setQuery('');
    setStatusFilter('all');
    setTab('all');
  };
  const bulkDisabled = disableControls || filesLoading || deletingAll || files.length === 0;
  const menuItems: AccountsMenuItem[] = [
    {
      key: 'refresh-credentials',
      label: t('auth_files.refresh_all_button'),
      disabled: disableControls || filesLoading || Object.keys(manualRefreshing).length > 0,
      onSelect: handleRefreshAllCredentials,
    },
    {
      key: 'select',
      label: selecting ? t('accounts.menu.stop_selecting') : t('accounts.menu.select'),
      onSelect: selecting ? stopSelecting : () => setSelecting(true),
    },
    {
      key: 'model-rules',
      label: t('accounts.menu.model_rules'),
      onSelect: () => navigate(ROUTES.routing),
    },
    {
      key: 'delete-problem',
      label: scope
        ? t('accounts.menu.delete_problem_in', { provider: scope })
        : t('accounts.menu.delete_problem'),
      danger: true,
      divided: true,
      disabled: bulkDisabled,
      onSelect: () => deleteMatching(true, false),
    },
    {
      key: 'delete-disabled',
      label: scope
        ? t('accounts.menu.delete_disabled_in', { provider: scope })
        : t('accounts.menu.delete_disabled'),
      danger: true,
      disabled: bulkDisabled,
      onSelect: () => deleteMatching(false, true),
    },
    {
      key: 'delete-all',
      label: scope
        ? t('accounts.menu.delete_all_in', { provider: scope })
        : t('accounts.menu.delete_all'),
      danger: true,
      disabled: bulkDisabled,
      onSelect: () => deleteMatching(false, false),
    },
  ];

  /* ---------- the managed account ---------- */

  const managed = manageKey ? (allAccounts.find((a) => a.authIndex === manageKey) ?? null) : null;
  const managedFile = manageKey ? fileByKey.get(manageKey) : undefined;
  const sheetOpen = manageOpen && managed !== null;

  // An account that disappears while managed (deleted from the sheet, or elsewhere) closes the
  // sheet for good: the same credential uploaded again must not reopen it. Its Manage button is
  // gone too, so focus moves to the page title.
  const { closePrefixProxyEditor } = editor;
  const { closeModels } = models;
  const managedGone = manageKey !== null && managed === null && filesLoaded;
  useEffect(() => {
    if (!managedGone) return;
    closePrefixProxyEditor();
    closeModels();
    setManageOpen(false);
    setManageKey(null);
    titleRef.current?.focus();
  }, [closeModels, closePrefixProxyEditor, managedGone]);

  const futureOptions = (Object.keys(FUTURE_USAGE) as FutureUsage[]).map((value) => ({
    value,
    label: t(`taproot_quota.controls.future_${value}`),
  }));
  const statusOptions = STATUS_FILTERS.map((value) => ({
    value,
    label: t(`accounts.status.${value}`),
  }));

  const firstLoad = (!quotaData && quotaLoading) || (filesLoading && files.length === 0);
  const loadError = quotaError || filesError;
  const now = pageNow(clock, quotaData?.now);

  return (
    <div className={`${quotaStyles.page} ${styles.page}`}>
      <header className={quotaStyles.header}>
        <div className={quotaStyles.titleBlock}>
          <h1 ref={titleRef} tabIndex={-1} className={`${quotaStyles.title} ${styles.title}`}>
            {t('accounts.title')}
          </h1>
          {!firstLoad && (
            <span className={quotaStyles.meta}>
              {t('accounts.meta', { count: counts.all, reporting })}
              {problems > 0 && (
                <span className={styles.metaProblem}>
                  {' · '}
                  {t('accounts.meta_problem', { count: problems })}
                </span>
              )}
            </span>
          )}
        </div>
        <div className={`${quotaStyles.headerActions} ${styles.headerActions}`}>
          <ShowEmailsToggle />
          <Button variant="secondary" size="sm" onClick={refreshAll} loading={refreshBusy}>
            {!refreshBusy && <IconRefreshCw size={15} />}
            {t('taproot_quota.ledger.refresh_all')}
          </Button>
          <AccountsMenu items={menuItems} />
          <AddAccount
            open={addOpen}
            onOpenChange={setAddOpen}
            uploading={uploading}
            disableControls={disableControls}
            onUpload={handleUploadClick}
            onAdded={reloadAfterSignIn}
            startProvider={readAccountsLink(searchParams).signIn}
            onStartHandled={clearDeepLink}
          />
        </div>
      </header>

      <AuthFileRefreshResults results={refreshResults} onClose={closeRefreshResults} />
      <input
        ref={fileInputRef}
        type="file"
        accept=".json,application/json"
        multiple
        hidden
        onChange={handleFileChange}
      />

      {groups.length > 0 && (
        <div className={quotaStyles.tabsRow}>
          <ProviderTabs
            types={tabTypes}
            counts={counts}
            active={activeTab}
            resolvedTheme={resolvedTheme}
            onChange={setTab}
          />
        </div>
      )}

      <div className={quotaStyles.toolbar}>
        <Segmented<ChartView>
          label={t('taproot_quota.controls.view')}
          value={view}
          onChange={setView}
          options={[
            { value: 'remaining', label: t('taproot_quota.controls.remaining') },
            { value: 'drain', label: t('taproot_quota.controls.drain') },
          ]}
        />
        <Segmented<`${BurnWindowHours}`>
          label={t('taproot_quota.controls.burn_rate')}
          showLabel
          value={`${burnHours}`}
          onChange={(value) => setBurnHours(Number(value) as BurnWindowHours)}
          options={BURN_WINDOWS_HOURS.map((h) => ({
            value: `${h}` as `${BurnWindowHours}`,
            label: t('taproot_quota.controls.hours', { count: h }),
          }))}
        />
        <div className={quotaStyles.segmentedGroup}>
          <span className={quotaStyles.controlLabel} id="accounts-future">
            {t('taproot_quota.controls.future')}
          </span>
          <div className={quotaStyles.selectWrap}>
            <Select
              value={future}
              options={futureOptions}
              onChange={(value) => setFuture(value as FutureUsage)}
              ariaLabelledBy="accounts-future"
              size="sm"
              fullWidth={false}
            />
          </div>
        </div>
        <div className={quotaStyles.rangeNav}>
          <span className={quotaStyles.rangeLabel} aria-live="polite">
            {t('taproot_quota.controls.range', {
              from: formatDay(range.from, locale),
              to: formatDay(lastDay(range), locale),
            })}
          </span>
          <button
            type="button"
            className={quotaStyles.iconButton}
            aria-label={t('taproot_quota.controls.previous')}
            onClick={() => setRange((r) => shiftRange(r, -1))}
          >
            <IconChevronLeft size={16} />
          </button>
          <button
            type="button"
            className={quotaStyles.toggle}
            disabled={isDefaultRange(range, clock)}
            onClick={() => setRange(defaultRange(clock))}
          >
            {t('taproot_quota.controls.today')}
          </button>
          <button
            type="button"
            className={quotaStyles.iconButton}
            aria-label={t('taproot_quota.controls.next')}
            onClick={() => setRange((r) => shiftRange(r, 1))}
          >
            <IconChevronLeft size={16} className={quotaStyles.flip} />
          </button>
        </div>
      </div>

      {loadError && (
        <div className={quotaStyles.error} role="alert">
          {mask(t('accounts.load_failed', { message: loadError }))}
        </div>
      )}

      {firstLoad ? (
        <div className={quotaStyles.cards} aria-busy="true">
          <Skeleton height={420} rounded={16} />
          <Skeleton height={320} rounded={16} />
        </div>
      ) : groups.length === 0 ? (
        <EmptyState
          title={t('accounts.empty_title')}
          description={t('accounts.empty_desc')}
          action={
            <div className={styles.emptyActions}>
              <Button size="sm" onClick={() => setAddOpen(true)} disabled={disableControls}>
                {t('accounts.add.button')}
              </Button>
              <Button
                variant="secondary"
                size="sm"
                onClick={handleUploadClick}
                disabled={disableControls || uploading}
              >
                {t('accounts.add.upload')}
              </Button>
            </div>
          }
        />
      ) : (
        <div className={quotaStyles.cards}>
          {visible.map((group) => (
            <ProviderCard
              key={group.provider}
              group={group}
              now={now}
              range={range}
              view={view}
              burnWindowHours={burnHours}
              future={future}
              showEmails={showEmails}
              resolvedTheme={resolvedTheme}
              liveEntries={liveByProvider.get(group.provider) ?? []}
            />
          ))}
          <Ledger
            groups={visible}
            fileByIndex={fileByKey}
            showEmails={showEmails}
            resolvedTheme={resolvedTheme}
            liveByAuthIndex={liveByAuthIndex}
            query={query}
            onQueryChange={setQuery}
            matchesQuery={matchesAccountSearch}
            rowFilter={(row, file) => matchesStatusFilter(statusFilter, row, file)}
            filtersActive={statusFilter !== 'all' || activeTab !== 'all'}
            onClearFilters={clearFilters}
            onRenderedRowsChange={setRenderedKeys}
            toolbarExtra={
              <Select
                value={statusFilter}
                onChange={(value) => setStatusFilter(value as StatusFilter)}
                options={statusOptions}
                ariaLabel={t('accounts.status.label')}
                size="sm"
                fullWidth={false}
              />
            }
            renderLeading={
              selecting
                ? (row, file) =>
                    file && !isRuntimeOnlyAuthFile(file) ? (
                      <SelectionCheckbox
                        checked={selectedFiles.has(file.name)}
                        onChange={() => toggleSelect(file.name)}
                        ariaLabel={t('accounts.select', { name: accountName(row, showEmails) })}
                      />
                    ) : null
                : undefined
            }
            renderTrailing={(row, file) => {
              const name = accountName(row, showEmails);
              const busy = file
                ? statusUpdating[getAuthFileRefreshKey(file)] === true ||
                  manualRefreshing[getAuthFileRefreshKey(file)] === true
                : false;
              return (
                <AccountControls
                  health={accountHealth(row, file, now)}
                  name={name}
                  statusMessage={file ? mask(getAuthFileStatusMessage(file)) : ''}
                  statusData={statusDataFor(file)}
                  enabled={file && !isRuntimeOnlyAuthFile(file) ? !file.disabled : null}
                  toggleDisabled={disableControls || busy}
                  onToggle={(enabled) => file && void handleStatusToggle(file, enabled)}
                  onManage={() => openManage(row.authIndex)}
                />
              );
            }}
          />
        </div>
      )}

      <AccountSheet
        open={sheetOpen}
        account={managed}
        file={managedFile}
        liveEntry={manageKey ? liveByAuthIndex.get(manageKey) : undefined}
        now={now}
        disableControls={disableControls}
        statusData={statusDataFor(managedFile)}
        refresh={quotaRefresh}
        actions={{
          deleting,
          statusUpdating,
          manualRefreshing,
          cooldownResetting,
          handleStatusToggle,
          handleManualRefresh,
          handleCooldownReset,
          handleDownload,
          handleDelete,
        }}
        models={models}
        editor={editor}
        onCopyText={(text) => void copyText(text)}
        onClose={() => setManageOpen(false)}
      />

      <BatchActionBar
        active={selecting}
        selectionCount={selectionCount}
        selectableCount={shownFiles.length}
        disableControls={disableControls}
        batchStatusDisabled={disableControls || selectionCount === 0 || batchStatusUpdating}
        onSelectAll={() => selectAllVisible(shownFiles)}
        onInvert={() => invertVisibleSelection(shownFiles)}
        onDeselectAll={deselectAll}
        onDone={stopSelecting}
        onDownload={() => void batchDownload([...selectedFiles])}
        onEnable={() => void batchSetStatus([...selectedFiles], true)}
        onDisable={() => void batchSetStatus([...selectedFiles], false)}
        onDelete={() => batchDelete([...selectedFiles])}
      />
    </div>
  );
}
