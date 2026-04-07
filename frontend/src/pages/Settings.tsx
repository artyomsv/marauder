import { useState } from "react";
import { motion } from "framer-motion";
import { Check, Moon, Sun } from "lucide-react";

import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useAuthStore } from "@/lib/auth-store";
import { api, ApiError } from "@/lib/api";
import { useT, useI18n, LOCALES, type Locale } from "@/i18n";
import { usePrefs, type Density, type Theme } from "@/lib/prefs";
import { cn } from "@/lib/utils";

/**
 * Real Settings page replacing the v0.4 placeholder.
 *
 * Sections (single-column, no tabs):
 *  1. Appearance — theme, language, table density (frontend-only,
 *     persisted in `marauder-prefs` localStorage via the prefs Zustand
 *     store).
 *  2. Account — username (read-only) + change password form
 *     (POST /auth/me/password) + sign out.
 *  3. About — version, license, links to GitHub.
 *
 * Server-side persistence of UI preferences is deferred (see plan
 * "deferred to a later iteration").
 */
export function SettingsPage() {
  const t = useT();
  const user = useAuthStore((s) => s.user);

  return (
    <div className="space-y-8">
      <motion.header
        initial={{ opacity: 0, y: 8 }}
        animate={{ opacity: 1, y: 0 }}
        transition={{ duration: 0.4 }}
      >
        <div className="mb-1 text-xs font-mono uppercase tracking-wider text-muted-foreground">
          {t("settings.kicker")}
        </div>
        <h1 className="text-3xl font-semibold tracking-tight">{t("settings.title")}</h1>
        <p className="mt-2 text-sm text-muted-foreground">{t("settings.blurb")}</p>
      </motion.header>

      <AppearanceCard />
      <AccountCard username={user?.username ?? ""} email={user?.email ?? ""} />
      <AboutCard />
    </div>
  );
}

// --- Appearance ---------------------------------------------------------

function AppearanceCard() {
  const t = useT();
  const theme = usePrefs((s) => s.theme);
  const setTheme = usePrefs((s) => s.setTheme);
  const density = usePrefs((s) => s.density);
  const setDensity = usePrefs((s) => s.setDensity);
  const locale = useI18n((s) => s.locale);
  const setLocale = useI18n((s) => s.setLocale);

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.appearance.title")}</CardTitle>
        <CardDescription>{t("settings.appearance.blurb")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <Row label={t("settings.appearance.theme")}>
          <SegmentedGroup<Theme>
            value={theme}
            onChange={setTheme}
            options={[
              { value: "light", label: t("settings.appearance.themeLight"), icon: <Sun className="size-3.5" /> },
              { value: "dark", label: t("settings.appearance.themeDark"), icon: <Moon className="size-3.5" /> },
            ]}
          />
        </Row>

        <Row label={t("settings.appearance.language")}>
          <SegmentedGroup<Locale>
            value={locale}
            onChange={setLocale}
            options={LOCALES.map((l) => ({ value: l.code, label: l.label }))}
          />
        </Row>

        <Row label={t("settings.appearance.density")}>
          <SegmentedGroup<Density>
            value={density}
            onChange={setDensity}
            options={[
              { value: "comfortable", label: t("settings.appearance.densityComfortable") },
              { value: "compact", label: t("settings.appearance.densityCompact") },
            ]}
          />
        </Row>
      </CardContent>
    </Card>
  );
}

// --- Account ------------------------------------------------------------

function AccountCard({ username, email }: { username: string; email: string }) {
  const t = useT();
  const refreshToken = useAuthStore((s) => s.refreshToken);
  const logout = useAuthStore((s) => s.logout);
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [message, setMessage] = useState<{ kind: "ok" | "err"; text: string } | null>(null);

  const handleChange = async (e: React.FormEvent) => {
    e.preventDefault();
    setMessage(null);
    if (next !== confirm) {
      setMessage({ kind: "err", text: t("settings.account.passwordMismatch") });
      return;
    }
    if (next.length < 8) {
      setMessage({ kind: "err", text: t("settings.account.passwordTooShort") });
      return;
    }
    setSubmitting(true);
    try {
      await api.post("/auth/me/password", { current_password: current, new_password: next });
      setMessage({ kind: "ok", text: t("settings.account.passwordChanged") });
      setCurrent("");
      setNext("");
      setConfirm("");
    } catch (err) {
      const detail = err instanceof ApiError ? err.problem.detail || err.problem.title : String(err);
      setMessage({ kind: "err", text: detail });
    } finally {
      setSubmitting(false);
    }
  };

  const handleLogout = async () => {
    if (refreshToken) {
      try {
        await api.post("/auth/logout", { refresh_token: refreshToken });
      } catch {
        // ignore — local logout proceeds either way
      }
    }
    logout();
    window.location.href = "/login";
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.account.title")}</CardTitle>
        <CardDescription>{t("settings.account.blurb")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-6">
        <Row label={t("settings.account.username")}>
          <span className="font-mono text-sm text-foreground">{username || "—"}</span>
        </Row>
        {email && (
          <Row label={t("settings.account.email")}>
            <span className="font-mono text-sm text-muted-foreground">{email}</span>
          </Row>
        )}

        <form onSubmit={handleChange} className="space-y-4 border-t border-border/50 pt-6">
          <div className="text-sm font-medium text-foreground">
            {t("settings.account.changePassword")}
          </div>
          <div className="grid gap-4 sm:grid-cols-3">
            <div className="space-y-1.5">
              <Label htmlFor="current-password">{t("settings.account.currentPassword")}</Label>
              <Input
                id="current-password"
                type="password"
                value={current}
                onChange={(e) => setCurrent(e.target.value)}
                autoComplete="current-password"
                required
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="new-password">{t("settings.account.newPassword")}</Label>
              <Input
                id="new-password"
                type="password"
                value={next}
                onChange={(e) => setNext(e.target.value)}
                autoComplete="new-password"
                required
                minLength={8}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="confirm-password">{t("settings.account.confirmPassword")}</Label>
              <Input
                id="confirm-password"
                type="password"
                value={confirm}
                onChange={(e) => setConfirm(e.target.value)}
                autoComplete="new-password"
                required
                minLength={8}
              />
            </div>
          </div>

          {message && (
            <div
              className={cn(
                "rounded-md border px-3 py-2 text-xs",
                message.kind === "ok"
                  ? "border-success/40 bg-success/10 text-success"
                  : "border-destructive/40 bg-destructive/10 text-destructive",
              )}
            >
              {message.kind === "ok" && <Check className="mr-1 inline size-3.5" />}
              {message.text}
            </div>
          )}

          <Button type="submit" disabled={submitting}>
            {submitting ? t("settings.account.saving") : t("settings.account.savePassword")}
          </Button>
        </form>

        <div className="border-t border-border/50 pt-6">
          <Button variant="outline" onClick={handleLogout}>
            {t("settings.account.signOut")}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

// --- About --------------------------------------------------------------

function AboutCard() {
  const t = useT();
  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("settings.about.title")}</CardTitle>
        <CardDescription>{t("settings.about.blurb")}</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3 text-sm">
        <Row label={t("settings.about.version")}>
          <span className="font-mono text-foreground">v0.4.0-alpha</span>
        </Row>
        <Row label={t("settings.about.license")}>
          <span className="font-mono text-foreground">MIT</span>
        </Row>
        <Row label={t("settings.about.links")}>
          <div className="flex flex-wrap gap-3 text-xs">
            <a
              href="https://marauder.cc"
              className="text-primary underline-offset-4 hover:underline"
              target="_blank"
              rel="noreferrer"
            >
              marauder.cc
            </a>
            <a
              href="https://github.com/artyomsv/marauder"
              className="text-primary underline-offset-4 hover:underline"
              target="_blank"
              rel="noreferrer"
            >
              GitHub
            </a>
            <a
              href="https://github.com/artyomsv/marauder/blob/main/CHANGELOG.md"
              className="text-primary underline-offset-4 hover:underline"
              target="_blank"
              rel="noreferrer"
            >
              Changelog
            </a>
            <a
              href="https://github.com/artyomsv/marauder/blob/main/docs/ROADMAP.md"
              className="text-primary underline-offset-4 hover:underline"
              target="_blank"
              rel="noreferrer"
            >
              Roadmap
            </a>
          </div>
        </Row>
      </CardContent>
    </Card>
  );
}

// --- Helpers ------------------------------------------------------------

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
      <span className="text-sm font-medium text-foreground">{label}</span>
      <div className="flex items-center gap-2">{children}</div>
    </div>
  );
}

interface SegmentedOption<T extends string> {
  value: T;
  label: string;
  icon?: React.ReactNode;
}

function SegmentedGroup<T extends string>({
  value,
  onChange,
  options,
}: {
  value: T;
  onChange: (v: T) => void;
  options: SegmentedOption<T>[];
}) {
  return (
    <div
      role="radiogroup"
      className="inline-flex items-center gap-1 rounded-lg border border-border/60 bg-muted/30 p-1"
    >
      {options.map((o) => {
        const active = o.value === value;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            onClick={() => onChange(o.value)}
            className={cn(
              "inline-flex items-center gap-1.5 rounded-md px-3 py-1 text-xs font-medium transition-colors",
              active
                ? "bg-background text-foreground shadow-[inset_0_0_0_1px_hsl(var(--border))]"
                : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.icon}
            <span>{o.label}</span>
          </button>
        );
      })}
    </div>
  );
}
