/**
 * A starting name for this device, guessed from the browser, for the access request form. The
 * person can change it; an unknown browser gets no guess.
 */
export function guessDeviceName(userAgent: string, maxTouchPoints = 0): string {
  const ua = userAgent.toLowerCase();
  if (ua.includes('iphone')) return 'iPhone';
  // iPadOS Safari reports itself as a Mac; only a touch screen tells them apart.
  if (ua.includes('ipad') || (ua.includes('macintosh') && maxTouchPoints > 1)) return 'iPad';
  if (ua.includes('android')) return 'Android phone';
  if (ua.includes('windows')) return 'Windows PC';
  if (ua.includes('macintosh') || ua.includes('mac os x')) return 'Mac';
  if (ua.includes('linux') && !ua.includes('cros')) return 'Linux PC';
  return '';
}

/** The guess for the browser this page runs in. */
export function browserDeviceName(): string {
  if (typeof navigator === 'undefined') return '';
  return guessDeviceName(navigator.userAgent ?? '', navigator.maxTouchPoints ?? 0);
}
