import { useEffect, useState, type ChangeEvent } from 'react';
import { copy } from '../copy';

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
    if (!query) return;
    const handleChange = () => applyTheme(resolveTheme('system'));
    if (typeof query.addEventListener === 'function') {
      query.addEventListener('change', handleChange);
      return () => query.removeEventListener('change', handleChange);
    }
    query.addListener(handleChange);
    return () => query.removeListener(handleChange);
  }, [mode]);

  const handleChange = (event: ChangeEvent<HTMLSelectElement>) => {
    const next = normalizeMode(event.target.value);
    applyTheme(resolveTheme(next));
    setMode(next);
    try {
      window.localStorage.setItem(STORAGE_KEY, next);
    } catch {
      // Storage can be disabled; the in-memory choice still applies.
    }
  };

  return (
    <label className="theme-control">
      <span className="theme-control__label" aria-hidden="true">
        {copy.themeLabel}
      </span>
      <select
        className="theme-control__select"
        aria-label={copy.themeLabel}
        value={mode}
        onChange={handleChange}
      >
        <option value="system">{copy.themeSystem}</option>
        <option value="light">{copy.themeLight}</option>
        <option value="dark">{copy.themeDark}</option>
      </select>
    </label>
  );
}
