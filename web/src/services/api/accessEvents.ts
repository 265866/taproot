/** Fired when the access guard answers 403 `not_admin`: this device lost Admin mid-session. */
export const ADMIN_REVOKED_EVENT = 'taproot-admin-revoked';

/** Tells the connection store that the server stopped admitting this caller. */
export function reportAccessDenial(status: number | undefined, code: string | undefined): void {
  if (typeof window === 'undefined') return;
  if (status === 401) {
    window.dispatchEvent(new Event('unauthorized'));
  } else if (status === 403 && code === 'not_admin') {
    window.dispatchEvent(new Event(ADMIN_REVOKED_EVENT));
  }
}
