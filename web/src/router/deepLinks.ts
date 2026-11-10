/**
 * The query parameters that open a page at one item, as links from the Dashboard write them
 * (`features/dashboard/model/links.ts`). Each page reads its own here.
 */

/** Accounts: `?provider=codex` opens that provider's tab; `?signIn=codex` also starts its sign-in. */
export function readAccountsLink(params: URLSearchParams): {
  provider: string | null;
  signIn: string | null;
} {
  const signIn = params.get('signIn');
  return { provider: params.get('provider') ?? signIn, signIn };
}

/**
 * Accounts' address once it has acted on the link: a started sign-in or a chosen tab is not part
 * of the page's address, so a reload must not start the sign-in again. Other parameters stay.
 */
export function withoutAccountsLink(params: URLSearchParams): URLSearchParams {
  const next = new URLSearchParams(params);
  next.delete('signIn');
  next.delete('provider');
  return next;
}

/** Access: `?focus=<node ID>` scrolls to that device's request and focuses it. */
export const readAccessFocus = (params: URLSearchParams): string | null => params.get('focus');

/** Settings: `?section=alerts` scrolls to that section and focuses its heading. */
export const readSettingsSection = (params: URLSearchParams): string | null =>
  params.get('section');
