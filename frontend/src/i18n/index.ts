/**
 * Tiny i18n module — no external dependency.
 *
 * Strings live in JSON-like records keyed by a dotted path. Locale is
 * persisted to localStorage. The `t()` function falls back to English
 * when a key is missing in the active locale, and falls back to the
 * key itself when missing in English (which makes missing translations
 * visible in the UI rather than blank).
 */

import { create } from "zustand";
import { persist } from "zustand/middleware";

import en from "./en";
import ru from "./ru";

export type Locale = "en" | "ru";

const dictionaries: Record<Locale, Record<string, string>> = { en, ru };

type I18nState = {
  locale: Locale;
  setLocale: (l: Locale) => void;
};

export const useI18n = create<I18nState>()(
  persist(
    (set) => ({
      locale: "en",
      setLocale: (l) => set({ locale: l }),
    }),
    { name: "marauder-locale" },
  ),
);

/**
 * Look up a translation. Use it inside a React component as:
 *
 *   const t = useT();
 *   return <h1>{t("login.title")}</h1>;
 */
export function useT(): (key: string, vars?: Record<string, string | number>) => string {
  const locale = useI18n((s) => s.locale);
  return (key, vars) => {
    const dict = dictionaries[locale] ?? dictionaries.en;
    let str = dict[key] ?? dictionaries.en[key] ?? key;
    if (vars) {
      for (const [k, v] of Object.entries(vars)) {
        str = str.replace(new RegExp(`\\{${k}\\}`, "g"), String(v));
      }
    }
    return str;
  };
}

export const LOCALES: { code: Locale; label: string }[] = [
  { code: "en", label: "English" },
  { code: "ru", label: "Русский" },
];
