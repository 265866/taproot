/**
 * Renders the Accounts page as static markup with fixed data. The two data hooks are replaced,
 * because effects (and so every API read) never run under static rendering, and the live quota
 * card's stylesheet echoes class names, because bun cannot load SCSS modules. The pool chart is
 * left out: Recharts needs a window.
 */

import { mock } from 'bun:test';
import { join } from 'node:path';
import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { MemoryRouter } from 'react-router-dom';
import type { UseAuthFilesDataResult } from '../../src/features/authFiles/hooks/useAuthFilesData';
import type { ProviderGroup } from '../../src/features/taproot-quota/model/fromApi';
import type { AuthFileItem } from '../../src/types/authFile';

const classEcho = () => ({
  default: new Proxy({}, { get: (_, key) => (typeof key === 'string' ? key : undefined) }),
});
mock.module(
  join(import.meta.dir, '../../src/features/taproot-quota/live/components/QuotaBody.module.scss'),
  classEcho
);

const M = 60_000;
const H = 60 * M;
const D = 24 * H;
export const NOW = Date.now();

export const CLAUDE: AuthFileItem = {
  name: 'claude-ab12cd34-anthropic1@mail.example.org.json',
  type: 'claude',
  provider: 'claude',
  email: 'anthropic1@mail.example.org',
  authIndex: 'a1',
  priority: 10,
  note: 'Shared with ops-person@example.org',
  status: 'error',
  statusMessage: 'refresh failed for anthropic1@mail.example.org',
  unavailable: true,
};
export const CODEX: AuthFileItem = {
  name: 'codex-1a2b3c4d-second.person@example.com-team.json',
  type: 'codex',
  provider: 'codex',
  email: 'second.person@example.com',
  authIndex: 'a2',
  priority: 0,
  disabled: true,
};
/** A credential taproot has not listed yet. */
export const NEW_FILE: AuthFileItem = {
  name: 'claude-9f8e7d6c-zebrafinch.alpha@mail.example.com.json',
  type: 'claude',
  provider: 'claude',
  email: 'zebrafinch.alpha@mail.example.com',
  authIndex: 'a3',
};

/** Every identifying fragment in the fixtures; none may appear while emails are hidden. */
export const SECRETS = [
  'anthropic1',
  'second.person',
  'ops-person',
  'zebrafinch',
  'example.org',
  'example.com',
];
export const leaks = (html: string) => SECRETS.filter((secret) => html.includes(secret));

export const GROUPS: ProviderGroup[] = [
  {
    provider: 'claude',
    hasQuotaData: true,
    accounts: [
      {
        authIndex: 'a1',
        provider: 'claude',
        label: 'anthropic1@mail.example.org',
        email: 'anthropic1@mail.example.org',
        plan: 'max',
        disabled: false,
        lastSeen: NOW - 5 * M,
        windows: [
          {
            window: 'seven_day',
            utilization: 0.39,
            resetsAt: NOW + 2 * D,
            observedAt: NOW - M,
            periodMs: 7 * D,
          },
        ],
        series: [],
      },
      {
        // Deleted since taproot's last account sweep: no file, so no row.
        authIndex: 'gone',
        provider: 'claude',
        label: 'quokkabeta@example.com',
        email: 'quokkabeta@example.com',
        plan: '',
        disabled: false,
        lastSeen: null,
        windows: [],
        series: [],
      },
    ],
  },
  {
    provider: 'codex',
    hasQuotaData: false,
    accounts: [
      {
        authIndex: 'a2',
        provider: 'codex',
        label: 'second.person@example.com',
        email: 'second.person@example.com',
        plan: 'team',
        // Taproot's copy lags the switch; the file says disabled.
        disabled: false,
        lastSeen: null,
        windows: [],
        series: [],
      },
    ],
  },
];

export const FILES = [CLAUDE, CODEX, NEW_FILE];

const noop = () => {};
const asyncNoop = async () => {};
const fakeData: UseAuthFilesDataResult = {
  files: FILES,
  selectedFiles: new Set(),
  selectionCount: 0,
  loading: false,
  refreshing: false,
  error: '',
  uploading: false,
  deleting: null,
  deletingAll: false,
  statusUpdating: {},
  manualRefreshing: {},
  refreshingAllCredentials: false,
  refreshResults: null,
  closeRefreshResults: noop,
  handleRefreshAllCredentials: noop,
  cooldownResetting: {},
  batchStatusUpdating: false,
  fileInputRef: { current: null },
  loadFiles: asyncNoop,
  handleUploadClick: noop,
  handleFileChange: asyncNoop,
  handleDelete: noop,
  handleDeleteAll: noop,
  handleDownload: asyncNoop,
  handleManualRefresh: asyncNoop,
  handleCooldownReset: noop,
  handleStatusToggle: asyncNoop,
  toggleSelect: noop,
  selectAllVisible: noop,
  invertVisibleSelection: noop,
  deselectAll: noop,
  batchDownload: asyncNoop,
  batchSetStatus: asyncNoop,
  batchDelete: noop,
};

mock.module(join(import.meta.dir, '../../src/features/taproot-quota/useQuotaData.ts'), () => ({
  useQuotaData: () => ({
    data: { groups: GROUPS, now: NOW },
    loading: false,
    error: '',
    refresh: asyncNoop,
  }),
}));
mock.module(
  join(import.meta.dir, '../../src/features/authFiles/hooks/useAuthFilesData.ts'),
  () => ({ useAuthFilesData: () => fakeData })
);
// Recharts measures the window; the pool chart is not under test here.
mock.module(
  join(import.meta.dir, '../../src/features/taproot-quota/components/PoolChart.tsx'),
  () => ({ PoolChart: () => null })
);

const { AccountsPage } = await import('../../src/features/accounts/AccountsPage');

export const renderAccountsPage = () =>
  renderToStaticMarkup(createElement(MemoryRouter, null, createElement(AccountsPage)));
