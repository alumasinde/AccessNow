export type Theme = 'light' | 'dark';

const KEY = 'accessnow.theme';

class ThemeStore {
  value = $state<Theme>('light');

  init() {
    if (typeof window === 'undefined') return;
    const saved = window.localStorage.getItem(KEY) as Theme | null;
    const preferred = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    this.apply(saved ?? preferred);
  }

  toggle() {
    this.apply(this.value === 'light' ? 'dark' : 'light');
  }

  apply(theme: Theme) {
    this.value = theme;
    document.documentElement.dataset.theme = theme;
    window.localStorage.setItem(KEY, theme);
  }
}

export const theme = new ThemeStore();
