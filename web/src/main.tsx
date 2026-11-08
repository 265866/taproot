import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@/styles/global.scss';
import '@/i18n';
import { purgeLegacyStorage } from '@/utils/legacyStorage';
import { preloadPage } from '@/router/pageImports';
import App from './App.tsx';

// Start downloading the page's chunk now, so it arrives while the connection gate waits.
preloadPage(window.location.hash.replace(/^#/, '').split('?')[0] || '/');

document.title = 'Taproot';
document.documentElement.setAttribute('translate', 'no');
document.documentElement.classList.add('notranslate');

try {
  purgeLegacyStorage(window.localStorage);
} catch {
  // Storage can be unavailable (privacy mode); nothing to purge then.
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
