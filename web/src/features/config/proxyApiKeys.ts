import { isScalar, isSeq, parseDocument } from 'yaml';

/**
 * Whether config.yaml lists proxy (client) API keys: `access.api-keys`, or the legacy top-level
 * `api-keys` list. Taproot admits callers by Tailscale identity, so these keys would make every
 * client send one. A top-level `api-keys` map holds upstream provider keys and does not count.
 */
export function hasProxyApiKeys(yaml: string): boolean {
  const doc = parseDocument(yaml);
  const listsKey = (node: unknown) =>
    isSeq(node) &&
    node.items.some((item) => !isScalar(item) || String(item.value ?? '').trim() !== '');
  return listsKey(doc.getIn(['access', 'api-keys'], true)) || listsKey(doc.get('api-keys', true));
}
