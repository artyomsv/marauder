import { Globe } from "lucide-react";

import { LOCALES, useI18n, type Locale } from "@/i18n";

/**
 * A tiny dropdown for switching the UI locale. Sits in the top header.
 */
export function LocaleSwitcher() {
  const locale = useI18n((s) => s.locale);
  const setLocale = useI18n((s) => s.setLocale);

  return (
    <div className="relative inline-flex items-center gap-1 text-xs text-muted-foreground">
      <Globe className="size-3.5" />
      <select
        aria-label="Language"
        value={locale}
        onChange={(e) => setLocale(e.target.value as Locale)}
        className="cursor-pointer appearance-none bg-transparent pr-1 text-xs text-muted-foreground hover:text-foreground focus:outline-none"
      >
        {LOCALES.map((l) => (
          <option key={l.code} value={l.code} className="text-foreground bg-background">
            {l.label}
          </option>
        ))}
      </select>
    </div>
  );
}
