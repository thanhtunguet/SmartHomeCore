import {createRoot} from 'react-dom/client';
// Fonts are bundled: the Controller's CSP (default-src 'self') blocks Google Fonts.
import '@fontsource/plus-jakarta-sans/400.css';
import '@fontsource/plus-jakarta-sans/500.css';
import '@fontsource/plus-jakarta-sans/600.css';
import '@fontsource/plus-jakarta-sans/700.css';
import '@fontsource/jetbrains-mono/400.css';
import '@fontsource/jetbrains-mono/500.css';
import '@fontsource/jetbrains-mono/600.css';
import App from './App.tsx';
import './index.css';

// Apply the saved theme before the first paint (the login screen renders before AppProvider mounts).
try {
  document.documentElement.classList.toggle('dark', localStorage.getItem('homecore_theme_v1') !== 'light');
} catch {
  /* storage unavailable: keep the default dark theme from index.html */
}

createRoot(document.getElementById('root')!).render(<App />);
