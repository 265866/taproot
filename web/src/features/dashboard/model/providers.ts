/** Provider names are proper nouns, so they stay out of the catalogs. */
const PROVIDER_LABELS: Record<string, string> = {
  gemini: 'Gemini',
  'gemini-interactions': 'Interactions API',
  aistudio: 'AI Studio',
  codex: 'Codex',
  claude: 'Claude',
  meta: 'Muse (Meta)',
  xai: 'xAI',
  openai: 'OpenAI Compatible',
  'openai-compatibility': 'OpenAI Compatible',
  qwen: 'Qwen',
  kimi: 'Kimi',
  iflow: 'iFlow',
  antigravity: 'Antigravity',
};

/** A provider's display name; an unknown id is capitalized, and `unknown` or empty gets `unknownLabel`. */
export function providerLabel(id: string, unknownLabel = 'Unknown'): string {
  if (id === 'unknown' || !id) return unknownLabel;
  return PROVIDER_LABELS[id] ?? id.charAt(0).toUpperCase() + id.slice(1);
}

/** Provider colors mark identity only; providers without a color of their own stay neutral. */
export function providerColor(id: string): string {
  if (id === 'claude' || id === 'codex') return `var(--provider-${id})`;
  return 'var(--text-tertiary)';
}

/** The provider a model name belongs to, for the families taproot colors; otherwise null. */
export function modelProvider(model: string): 'claude' | 'codex' | null {
  const name = model.toLowerCase();
  if (name.startsWith('claude')) return 'claude';
  if (name.startsWith('gpt') || name.includes('codex')) return 'codex';
  return null;
}
