/**
 * The browser's localStorage, or undefined when there is none or the browser blocks site data:
 * reading the `localStorage` property itself then throws a SecurityError.
 */
export function browserStorage(): Storage | undefined {
  try {
    return typeof localStorage === 'undefined' ? undefined : localStorage;
  } catch {
    return undefined;
  }
}
