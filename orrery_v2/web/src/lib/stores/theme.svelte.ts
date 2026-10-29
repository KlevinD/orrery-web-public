export type ThemeMode = 'dark' | 'light'
export type ThemeBrand = 'default' | 'ocean' | 'forest' | 'purple'

const MODE_STORAGE_KEY = 'orrery.theme.mode'
const BRAND_STORAGE_KEY = 'orrery.theme.brand'
const canUseDom = typeof window !== 'undefined' && typeof document !== 'undefined'

function normalizeMode(value: string | null | undefined): ThemeMode {
  return value === 'dark' ? 'dark' : 'light'
}

function normalizeBrand(value: string | null | undefined): ThemeBrand {
  if (value === 'ocean' || value === 'forest' || value === 'purple') return value
  return 'default'
}

function initialMode(): ThemeMode {
  return normalizeMode(readStorage(MODE_STORAGE_KEY))
}

function initialBrand(): ThemeBrand {
  return normalizeBrand(readStorage(BRAND_STORAGE_KEY))
}

function readStorage(key: string) {
  if (!canUseDom || typeof window.localStorage?.getItem !== 'function') return null
  return window.localStorage.getItem(key)
}

function writeStorage(key: string, value: string) {
  if (!canUseDom || typeof window.localStorage?.setItem !== 'function') return
  window.localStorage.setItem(key, value)
}

export const theme = $state({
  mode: initialMode(),
  brand: initialBrand()
})

export function applyTheme(mode: ThemeMode = theme.mode, brand: ThemeBrand = theme.brand) {
  if (!canUseDom) return
  const root = document.documentElement
  root.classList.remove('light', 'dark')
  root.classList.add(mode)
  root.style.colorScheme = mode

  root.classList.remove('brand-ocean', 'brand-forest', 'brand-purple')
  if (brand !== 'default') {
    root.classList.add(`brand-${brand}`)
  }
}

export function setThemeMode(mode: ThemeMode) {
  theme.mode = mode
  applyTheme(mode, theme.brand)
  writeStorage(MODE_STORAGE_KEY, mode)
}

export function toggleThemeMode() {
  setThemeMode(theme.mode === 'dark' ? 'light' : 'dark')
}

export function setThemeBrand(brand: ThemeBrand) {
  theme.brand = brand
  applyTheme(theme.mode, brand)
  writeStorage(BRAND_STORAGE_KEY, brand)
}

applyTheme(theme.mode, theme.brand)
