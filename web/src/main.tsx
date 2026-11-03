import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import '@/styles/global.scss';
import { INLINE_LOGO_JPEG } from '@/assets/logoInline';
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

const faviconEl = document.querySelector<HTMLLinkElement>('link[rel="icon"]');
if (faviconEl) {
  faviconEl.href = INLINE_LOGO_JPEG;
  faviconEl.type = 'image/jpeg';
} else {
  const newFavicon = document.createElement('link');
  newFavicon.rel = 'icon';
  newFavicon.type = 'image/jpeg';
  newFavicon.href = INLINE_LOGO_JPEG;
  document.head.appendChild(newFavicon);
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>
);
