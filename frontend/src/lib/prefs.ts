/**
 * Local UI preferences (theme, density, etc).
 *
 * v0.4 keeps these in localStorage; v0.5 will sync them to the server
 * via PATCH /users/me when the user-preferences API ships.
 *
 * `setTheme` also toggles the `.dark` class on <html> so Tailwind 4
 * picks up the change immediately. The boot script in `index.html`
 * applies the persisted theme synchronously to avoid a FOUC flash.
 */
import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Density = "comfortable" | "compact";
export type Theme = "light" | "dark";

interface PrefsState {
  density: Density;
  theme: Theme;
  setDensity: (d: Density) => void;
  setTheme: (t: Theme) => void;
}

const applyThemeClass = (theme: Theme) => {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  if (theme === "dark") root.classList.add("dark");
  else root.classList.remove("dark");
};

export const usePrefs = create<PrefsState>()(
  persist(
    (set) => ({
      density: "comfortable",
      theme: "dark",
      setDensity: (d) => set({ density: d }),
      setTheme: (t) => {
        applyThemeClass(t);
        set({ theme: t });
      },
    }),
    {
      name: "marauder-prefs",
      onRehydrateStorage: () => (state) => {
        if (state) applyThemeClass(state.theme);
      },
    },
  ),
);
