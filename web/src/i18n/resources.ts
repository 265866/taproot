import { mergeLocaleModules, type LocaleTree } from './merge';

// Every feature owns src/i18n/locales/en/<feature>.json; adding a file needs no registration.
const modules = import.meta.glob<LocaleTree>('./locales/en/*.json', {
  eager: true,
  import: 'default',
});

export const en = mergeLocaleModules(modules);
