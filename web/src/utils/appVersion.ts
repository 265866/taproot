/** The dashboard's version as the sidebar shows it: "0.4.0" from "taproot-v0.4.0" or "v0.4.0". */
export function displayVersion(raw: string | null | undefined): string {
  const trimmed = raw?.trim() ?? '';
  return trimmed.replace(/^taproot-/, '').replace(/^v(?=\d)/, '') || 'dev';
}
