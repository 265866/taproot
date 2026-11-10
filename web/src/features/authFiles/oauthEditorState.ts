import { normalizeProviderKey } from './constants';

type ModelAliasDraftEntry = {
  id?: string;
  name?: string;
  alias?: string;
  fork?: boolean;
  forceMapping?: boolean;
};

export const getStringSetSignature = (values: Iterable<string>): string =>
  JSON.stringify(
    Array.from(new Set(values), (value) => value.trim())
      .filter(Boolean)
      .sort()
  );

export const getModelAliasDraftSignature = (entries: ModelAliasDraftEntry[]): string =>
  JSON.stringify(
    entries
      .map((entry) => ({
        name: entry.name ?? '',
        alias: entry.alias ?? '',
        fork: entry.fork === true,
        forceMapping: typeof entry.forceMapping === 'boolean' ? entry.forceMapping : undefined,
      }))
      .filter(
        (entry) =>
          entry.name !== '' ||
          entry.alias !== '' ||
          entry.fork !== true ||
          entry.forceMapping !== undefined
      )
  );

/** The field a half-filled alias row lacks; a blank or complete row lacks nothing. */
export const getMissingMappingField = (
  entry: Pick<ModelAliasDraftEntry, 'name' | 'alias'>
): 'name' | 'alias' | undefined => {
  const hasName = Boolean(entry.name?.trim());
  const hasAlias = Boolean(entry.alias?.trim());
  if (hasName === hasAlias) return undefined;
  return hasName ? 'alias' : 'name';
};

export const isOAuthEditorDirty = (
  initialProvider: string,
  currentProvider: string,
  baselineContentSignature: string,
  currentContentSignature: string
): boolean =>
  normalizeProviderKey(initialProvider) !== normalizeProviderKey(currentProvider) ||
  baselineContentSignature !== currentContentSignature;

type EscapeKeyEvent = Pick<KeyboardEvent, 'key' | 'defaultPrevented' | 'target'>;

const isEditableTarget = (target: EventTarget | null): boolean => {
  const element = target as { tagName?: string; isContentEditable?: boolean } | null;
  if (!element) return false;
  if (element.isContentEditable) return true;
  return ['INPUT', 'TEXTAREA', 'SELECT'].includes(String(element.tagName ?? '').toUpperCase());
};

/**
 * Escape leaves an editor page only when nothing else claims the key: a field the user is typing
 * in, a list or menu that closed on it (and prevented its default), or an open dialog.
 */
export const isEditorBackEscape = (event: EscapeKeyEvent, layerOpen: boolean): boolean =>
  event.key === 'Escape' &&
  !event.defaultPrevented &&
  !layerOpen &&
  !isEditableTarget(event.target);
