import { useEffect, useState } from 'react';
import { copy } from '../copy';
import { Icon } from './Icon';

type ThemeMode = 'system' | 'light' | 'dark';
type ResolvedTheme = 'light' | 'dark';

const STORAGE_KEY = 'seekwell-theme';
const LIGHT_QUERY = '(prefers-color-scheme: light)';
const DARK_THEME_COLOR = '#0B0D10';
const LIGHT_THEME_COLOR = '#f5f7f9';

function normalizeMode(value: string | null): ThemeMode {
  return value === 'light' || value === 'dark' || value === 'system' ? value : 'system';
}

function readStoredMode(): ThemeMode {
  try {
    return normalizeMode(window.localStorage.getItem(STORAGE_KEY));
  } catch {
    return 'system';
  }
}

function lightQuery(): MediaQueryList | null {
  try {
    if (typeof window.matchMedia !== 'function') return null;
    return window.matchMedia(LIGHT_QUERY);
  } catch {
    return null;
  }
}

function resolveTheme(mode: ThemeMode): ResolvedTheme {
  if (mode !== 'system') return mode;
  return lightQuery()?.matches ? 'light' : 'dark';
}

function applyTheme(theme: ResolvedTheme) {
  const root = document.documentElement;
  root.dataset.theme = theme;
  root.style.colorScheme = theme;
  const meta = document.querySelector('meta[name="theme-color"]');
  if (meta) {
    meta.setAttribute('content', theme === 'light' ? LIGHT_THEME_COLOR : DARK_THEME_COLOR);
  }
}

export function ThemeControl() {
  const [mode, setMode] = useState<ThemeMode>(readStoredMode);

  useEffect(() => {
    applyTheme(resolveTheme(mode));
    if (mode !== 'system') return;

    const query = lightQuery();
    const syncSystemTheme = () => applyTheme(resolveTheme('system'));
    const handleMediaChange = (event: MediaQueryListEvent) => {
      applyTheme(event.matches ? 'light' : 'dark');
    };
    const handleVisibilityChange = () => {
      if (document.visibilityState === 'visible') syncSystemTheme();
    };

    if (query) {
      if (typeof query.addEventListener === 'function') {
        query.addEventListener('change', handleMediaChange);
      } else {
        query.addListener(handleMediaChange);
      }
    }
    window.addEventListener('focus', syncSystemTheme);
    document.addEventListener('visibilitychange', handleVisibilityChange);

    return () => {
      if (query) {
        if (typeof query.removeEventListener === 'function') {
          query.removeEventListener('change', handleMediaChange);
        } else {
          query.removeListener(handleMediaChange);
        }
      }
      window.removeEventListener('focus', syncSystemTheme);
      document.removeEventListener('visibilitychange', handleVisibilityChange);
    };
  }, [mode]);

  const selectMode = (next: ThemeMode) => {
    applyTheme(resolveTheme(next));
    setMode(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Storage can be disabled; the in-memory choice still applies.
    }
  };

  return (
    <div className="theme-control" role="group" aria-label={copy.themeLabel}>
      <span className="theme-control__label" aria-hidden="true">
        {copy.themeLabel}
      </span>
      <button
        type="button"
        className="theme-control__button"
        aria-label={copy.themeSystem}
        aria-pressed={mode === 'system'}
        title={copy.themeSystem}
        onClick={() => selectMode('system')}
      >
        <Icon name="themeSystem" />
      </button>
      <button
        type="button"
        className="theme-control__button"
        aria-label={copy.themeLight}
        aria-pressed={mode === 'light'}
        title={copy.themeLight}
        onClick={() => selectMode('light')}
      >
        <Icon name="themeLight" />
      </button>
      <button
        type="button"
        className="theme-control__button"
        aria-label={copy.themeDark}
        aria-pressed={mode === 'dark'}
        title={copy.themeDark}
        onClick={() => selectMode('dark')}
      >
        <Icon name="themeDark" />
      </button>
    </div>
  );
}
