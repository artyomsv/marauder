/**
 * Local UI preferences (density, etc).
 *
 * v0.4 keeps these in localStorage; v0.5 will sync them to the server
 * via PATCH /users/me when the user-preferences API ships.
 */
import { create } from "zustand";
import { persist } from "zustand/middleware";

export type Density = "comfortable" | "compact";

type PrefsState = {
  density: Density;
  setDensity: (d: Density) => void;
};

export const usePrefs = create<PrefsState>()(
  persist(
    (set) => ({
      density: "comfortable",
      setDensity: (d) => set({ density: d }),
    }),
    { name: "marauder-prefs" },
  ),
);
