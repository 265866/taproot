# Repository Guidelines

## Project Scope & Structure

This is taproot's dashboard: a React 19 + TypeScript + Vite frontend, hard-forked from Cli-Proxy-API-Management-Center and served by taproot's Go server at `/`. It no longer follows upstream UI changes. `internal/taproot/PLAN.md` at the repository root defines the dashboard scope, the taproot API contract, and which workstream owns which folder; read it before structural work.

The dashboard talks to two same-origin APIs and never holds a credential:

- The CLIProxyAPI v8 Management API under `/v8/management`. Taproot's access guard admits allowlisted Tailscale callers and injects the management key server-side, so requests carry no `Authorization` header. Do not add v0 fallbacks or legacy config adapters. Plugin resources and custom HTTP extensions keep their backend-declared paths.
- Taproot's own API under `/taproot/api/v1`, through the typed client in `src/services/api/taproot.ts`.

There is no login page, management-key prompt, or key storage. `src/router/ConnectionGate.tsx` connects once and shows progress, access denied, or server unreachable. `src/utils/legacyStorage.ts` deletes keys the upstream UI left in localStorage on the same origin.

Removed for good (do not reintroduce): Quick Start, AI Providers and API-key provider editing, sponsor or affiliate content, Kimi China, Devin, Vertex, SIWC, Config panel API-key editing, and non-English locales. Their backend code stays in the Go tree; only the dashboard drops them.

- `src/features/`: feature-owned pages, components, hooks, types, and logic: `dashboard`, `accounts`, `authFiles` (credential data hooks, the details form, and policy editors under `policy/`), `oauth` (provider sign-in flows), `routing` (OAuth credentials only), `config`, and `plugins`.
- `accounts` (at `#/accounts`) is one page for every account. It composes the other features rather than owning their logic: the pool charts and ledger from `taproot-quota`, credential actions from `authFiles`, and sign-in from `oauth`. Its `model.ts` joins taproot's accounts with the credential files; once the file list loads it decides which accounts exist and which are enabled. `#/quota`, `#/auth-files`, and `#/oauth` redirect there (`LEGACY_REDIRECTS` in `src/router/routes.ts`).
- `src/features/taproot-{quota,usage,logs,backups,settings}/`: taproot sections. Each belongs to the workstream named in the plan. `taproot-quota` has no page of its own: Accounts renders its pool charts and ledger. It keeps its pure projection model in `model/` and the live provider adapters in `live/`. `taproot-logs` also holds the application log tail (`appLogs/`, the `?tab=app` tab of `#/request-logs`), which replaced the previous `#/logs` viewer.
- `src/pages/`: remaining route pages outside the feature layout (system, and the provider model rule editors for OAuth excluded models and model aliases, at `#/routing/oauth-excluded` and `#/routing/oauth-model-alias`; Routing lists the rules).
- `src/components/`, `src/hooks/`, `src/utils/`: shared UI, hooks, and utilities. Keep feature-specific code inside its feature.
- `src/services/api/`: API clients, domain endpoints, and backend data normalization.
- `src/stores/`: Zustand state. `useAuthStore` holds the connection state (status, failure reason, server version, plugin support), not credentials.
- `src/types/`: shared types. `src/styles/`: global styles and theme tokens.
- `src/App.tsx`: hash-router setup. `src/router/routes.ts`: route paths shared by the route table, sidebar, and links. `src/router/MainRoutes.tsx`: route table. `src/components/layout/MainLayout.tsx`: shell and sidebar navigation.
- `src/assets/`: bundled assets, including provider icons in `icons/`.

## Internationalization

The UI is English only and the i18n layer stays: keep user-facing text, including accessible labels, in catalogs.

- Each feature owns `src/i18n/locales/en/<feature>.json`. A file owns its top-level keys (for example `taproot-usage.json` owns `taproot_usage`), and call sites use `t('taproot_usage.title')`.
- `src/i18n/resources.ts` merges every file in that folder at startup with `import.meta.glob`, so adding a feature file needs no registration. Two files defining the same top-level key throw at startup.
- Shell strings (nav, sidebar, header, theme, connection gate) live in `shell.json`; cross-feature strings in `common.json`.
- Tests read the merged catalog with `import { en } from '@/i18n/resources'`. `bunfig.toml` preloads `tests/helpers/viteGlobPlugin.ts`, which expands the same glob under `bun test`.

## Build, Test, and Development Commands

Use Bun; `package.json` pins `bun@1.4.3`. Keep dependency changes consistent with `bun.lock`; do not introduce another package manager's lockfile.

- `bun install --frozen-lockfile`: install locked dependencies.
- `bun run dev`: start Vite at `http://localhost:5173`. It proxies `/v8`, `/v0`, and `/taproot` to `TAPROOT_DEV_SERVER` (default `http://127.0.0.1:8317`). Until the server injects the management key itself, start the local server with `MANAGEMENT_PASSWORD=<key>` and set `TAPROOT_DEV_MANAGEMENT_KEY=<key>` for Vite; the proxy adds the header in development only.
- `bun run build`: TypeScript compilation followed by the production Vite build.
- `bun run preview`: serve the built output locally.
- `bun run test`: run all Bun tests. `bun test tests/apiError.test.ts` runs one file.
- `bun run lint`: run ESLint over TypeScript/TSX files.
- `bun run type-check`: run `tsc --noEmit`; the main TypeScript config includes `src`, not `tests`.
- `bun run verify`: run tests, lint, and build.
- `bun run format`: format all `src/**/*.{ts,tsx,css,scss}`. Prefer formatting only the files you change; several upstream files are not Prettier-clean.

## Deployment Constraints

The production artifact is a single `dist/index.html` with JS, CSS, and assets inlined by `vite-plugin-singlefile`. Taproot embeds it (see `internal/taproot/web`) and serves it at `/` with `Cache-Control: no-store`. Upstream's `/management.html` is disabled in deployment.

Preserve hash routing and single-file deployment. Changes to assets, imports, code splitting, or build configuration must not introduce required external build artifacts. Do not edit generated `dist/` files. App version is injected as `__APP_VERSION__` from `VERSION`, then git tags, then the package version, falling back to `dev`.

## API Contracts & State

- Treat backend contracts as the source of truth. The backend is the Go module at the repository root; inspect it before changing endpoint names, payloads, provider keys, OAuth callback parameters, auth-file semantics, or plugin/config contracts.
- Reuse `apiClient` from `src/services/api/client.ts` for Management API requests through domain modules. It centralizes the API prefix, error normalization, and response-header handling. Use `taprootApi` from `src/services/api/taproot.ts` for taproot endpoints. Do not add ad hoc requests in components.
- Preserve the client's events: `unauthorized` marks the connection as access denied, `server-version-update` carries version/build metadata, and `server-plugin-support-update` carries plugin capability. Keep plugin routes and nav items gated by backend support.
- Normalize backend fields on read and serialize on write in the API layer; consult `transformers.ts` and the relevant domain module. Keep raw backend field-name handling out of ordinary UI components.
- `useConfigStore.fetchConfig(forceRefresh?: boolean)` uses a full-config TTL cache and in-flight request deduplication. Reuse it and invalidate or update caches after mutations using existing store actions.
- Keep stale-request guards (`apiClient.getConnectionRevision()`) around read/modify/write sequences.

## Coding Style & UI Conventions

Use 2-space indentation, semicolons, single quotes, ES5 trailing commas, and 100-character line width. Prefer typed React components and `unknown` with narrowing for untrusted data; avoid `any` unless an unavoidable boundary requires it. Use the `@/` alias for `src` imports.

Component files use PascalCase, hooks use `useName`, and API modules use domain names such as `oauth.ts`. SCSS Modules sit beside their page or component as `Name.module.scss`. Vite injects `src/styles/variables.scss` into SCSS automatically.

The visual direction follows `internal/taproot/docs/quota-reference.png`, taken to monochrome: a pure black page where cards are the page color, separated by 1px hairlines, with generous spacing and large numerals for headline figures. The interface has no hue of its own; a neutral gray marks selection and focus, and the primary button is near-white with black text. Provider and status colors are the only color on the page. Dark is the default theme; the light, white, and auto themes get the same neutral treatment (gray pills, a near-black primary button) and must keep working. Use the tokens in `src/styles/themes.scss` instead of hard-coded colors:

- Surfaces and text: `--bg-secondary` (page), `--bg-primary` (cards; pure black in dark, so a card needs its `--border-color` hairline), `--bg-tertiary` and `--bg-hover` (hover, active), `--floating-surface` (menus, popovers, tooltips; lifted a step with a border so they read as floating), `--border-color`, `--text-primary`, `--text-secondary`, `--text-tertiary`, and `--input-bg` for inputs. Text tokens hold WCAG AA on every surface in every theme; `tests/themeContrast.test.ts` enforces it.
- Interface accent (neutral): `--accent` (focus outlines, toggles, checkboxes, the selected tab underline), `--accent-hover`, `--accent-subtle` (the selected pill fill), `--accent-text` (selected text), `--accent-border`, and `--focus-ring`. `--primary-color` maps to `--accent`; `--primary-contrast` is the ink drawn on it, such as a checked toggle's thumb.
- Links: link text is no longer distinguished by color, so a text link needs an underline (or a trailing arrow and heavier weight, as panel links use) to stay recognizable.
- Selection states: a selected tab's underline uses `--accent`; a selected segment or chip uses `--accent-subtle` with `--accent-text`. Never mark a selection or a primary action with a `--text-primary` fill.
- Provider accents: `--provider-claude`, `--provider-codex`, and their `-soft` fills. Provider hues stay reserved for provider data. Chart series palettes are muted cool hues that keep clear of them and of the status colors, so Claude orange is the only warm series; `tests/usagePalette.test.ts` checks this and the series' mutual separation.
- Display type: `--font-size-page-title`, `--font-size-numeral-xl`, `--font-size-numeral-lg`, and the `.numeral-xl` and `.numeral-lg` classes.
- Primary actions, including feature-owned pills, use `--button-primary-bg`, `--button-primary-hover`, and `--button-primary-text` (white with black text in dark, near-black with white text in light). Toggles and checkboxes use `--primary-color`.
- The taproot mark: `src/components/common/TaprootMark.tsx` draws the bare glyph in `--mark-color`; only the favicon (`src/assets/taprootMark.ts`, repeated in `index.html`) sits on a dark tile.

Charts use Recharts. Reuse shared components from `src/components/ui/` before adding primitives. Preserve keyboard interaction, accessible names, focus behavior, and reduced-motion handling. For motion, reuse `--ease-out-strong`, `--dur-press`, `--dur-hover`, and the helpers in `src/hooks/motion.ts`.

## Testing & Verification

Tests live under `tests/` as `*.test.ts` and use `bun:test`; fixtures are in `tests/fixtures/` and helpers in `tests/helpers/`. Suites cover pure logic, React server-side static rendering via `renderToStaticMarkup`, source/contract checks, and real HTTP against `Bun.serve`. There is no browser DOM harness; static markup tests do not verify browser interactions.

For code changes, add or update regression tests, run focused tests while iterating, and run `bun run verify` before handoff. For UI changes, also load the affected route in a real browser against a running server and keep screenshots or notes. Report commands actually run, failures, and anything not verified.

## Security

Never commit management keys, provider credentials, auth files, request logs, backups, or other secrets; redact them from logs, screenshots, and fixtures. The browser must never receive or store the management key. Do not weaken the access model, plugin trust checks, or session isolation for convenience.

Account emails are hidden by default everywhere. Auth file names carry the email (`claude-<hash>-<email>.json`), and so can error text, logs, bodies, and session ids. Render any such text through `mask` from `src/hooks/useEmailMask.ts`, including `title`, `aria-label`, and DOM ids; toasts, confirmations, modal and sheet titles, and `Select` labels already mask strings. Search, filters, API calls, copy, and download keep the real values. Error banners and `EmptyState` text come from the server, so they are masked too (`tests/emailRedactionErrors.test.ts` scans for unmasked ones). A filter field whose value holds an email shows `MaskedValueChip` while hidden, an edit field shows it masked and read-only until emails are shown (see `AuthFileDetailsForm`), and a value that can hold one goes into the URL through `encodeUrlValue` (`src/utils/urlValue.ts`). The preference lives in `src/stores/usePrivacyStore.ts`, is session-only, and pages that show masked values carry `ShowEmailsToggle`. Settings shows Tailscale logins as they are.

## Commits & Guidance Maintenance

Use Conventional Commits, such as `feat(web): add the usage page` or `fix(web): keep disabled credentials dimmed`. Keep changes focused and include UI screenshots or notes and verification results in pull requests.

Maintain shared guidance in this `AGENTS.md`. `CLAUDE.md` is ignored and untracked; shared guidance must not depend on it.
