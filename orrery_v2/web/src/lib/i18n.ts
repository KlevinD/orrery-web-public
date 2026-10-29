import { writable } from "svelte/store";
import { enUS } from "./i18n/locales/en-US";
import { zhCN, type TranslationKey } from "./i18n/locales/zh-CN";

export type Locale = "zh-CN" | "en-US";
export type { TranslationKey } from "./i18n/locales/zh-CN";

const STORAGE_KEY = "orrery.locale";

export const dictionaries: Record<
  Locale,
  Readonly<Record<TranslationKey, string>>
> = {
  "zh-CN": zhCN,
  "en-US": enUS,
};

export function normalizeLocale(value: string | null | undefined): Locale {
  return value === "en-US" ? "en-US" : "zh-CN";
}

export function createTranslator(locale: Locale) {
  return (
    key: TranslationKey,
    params: Record<string, string | number> = {},
  ) => {
    let message =
      dictionaries[locale][key] ?? dictionaries["zh-CN"][key] ?? key;
    for (const [name, value] of Object.entries(params)) {
      message = message.replaceAll(`{${name}}`, String(value));
    }
    return message;
  };
}

const canUseStorage =
  typeof window !== "undefined" &&
  typeof window.localStorage?.getItem === "function" &&
  typeof window.localStorage?.setItem === "function";
const initialLocale = canUseStorage
  ? normalizeLocale(window.localStorage.getItem(STORAGE_KEY))
  : "zh-CN";

export const locale = writable<Locale>(initialLocale);

function syncDocumentLocale(next: Locale) {
  if (typeof document !== "undefined") {
    document.documentElement.lang = next;
  }
}

syncDocumentLocale(initialLocale);

export function setLocale(next: Locale) {
  locale.set(next);
  syncDocumentLocale(next);
  if (canUseStorage) window.localStorage.setItem(STORAGE_KEY, next);
}

export function translateValue(value: string, localeValue: Locale) {
  const key = value as TranslationKey;
  return key in dictionaries[localeValue]
    ? dictionaries[localeValue][key]
    : value;
}
