import { createContext, useContext, useLayoutEffect, type ReactNode } from 'react';
import type { ThemePref } from './types';

const darkMedia = '(prefers-color-scheme: dark)';
const ThemeContext = createContext<ThemePref>('');

/** Media condition for the dark source in the brand's picture elements. */
export function useDarkArtwork(): string {
  const pref = useContext(ThemeContext);
  return pref === '' ? darkMedia : pref === 'dark' ? 'all' : 'not all';
}

export function ThemeProvider({ pref, children }: { pref: ThemePref; children: ReactNode }) {
  useLayoutEffect(() => {
    const root = document.documentElement;
    if (pref) {
      root.dataset.theme = pref;
      return;
    }
    const system = window.matchMedia(darkMedia);
    const apply = () => { root.dataset.theme = system.matches ? 'dark' : 'light'; };
    apply();
    system.addEventListener('change', apply);
    return () => system.removeEventListener('change', apply);
  }, [pref]);

  return <ThemeContext.Provider value={pref}>{children}</ThemeContext.Provider>;
}
