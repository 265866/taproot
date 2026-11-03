import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@/styles/global.scss';
import '@/i18n';
import { purgeLegacyStorage } from '@/utils/legacyStorage';
import App from './App.tsx';

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
