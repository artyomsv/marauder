import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import {
  AlertCircle,
  CheckCircle2,
  KeyRound,
  Loader2,
  Pencil,
  Plus,
  RefreshCw,
} from "lucide-react";

import { api, ApiError } from "@/lib/api";
import { useSystemInfo } from "@/lib/hooks/useSystemInfo";
import { useT } from "@/i18n";
import { QK } from "@/lib/queryKeys";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { DeleteConfirm } from "@/components/shared/DeleteConfirm";
import { ResourceCard } from "@/components/shared/ResourceCard";

/**
 * Tracker accounts page (route: /accounts).
 *
 * Lets the user add and rotate per-tracker credentials. Required for
 * forum trackers like LostFilm, RuTracker, Kinozal — anything that
 * gates content behind a session cookie.
 */

type CredentialView = {
  id: string;
  tracker_name: string;
  display_name: string;
  username: string;
  created_at: string;
  updated_at: string;
};

type CredList = { credentials: CredentialView[] | null };

export function CredentialsPage() {
  const qc = useQueryClient();
  const { data: credsData, isLoading } = useQuery({
    queryKey: QK.credentials,
    queryFn: () => api.get<CredList>("/credentials"),
  });
  const { data: systemInfo } = useSystemInfo();

  const [showAdd, setShowAdd] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const credentials = credsData?.credentials ?? [];
  const trackers = systemInfo?.trackers ?? [];

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/credentials/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.credentials }),
  });

  const test = useMutation({
    mutationFn: (id: string) => api.post<{ ok: boolean }>(`/credentials/${id}/test`),
  });

  return (
    <div className="space-y-8">
      <header className="flex items-start justify-between gap-4">
        <div>
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-muted-foreground">
            tracker accounts
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">Tracker accounts</h1>
          <p className="mt-2 max-w-2xl text-sm text-muted-foreground">
            Forum trackers like LostFilm, RuTracker, and Kinozal gate their
            content behind a session cookie. Add your account here once and
            Marauder will reuse it for every topic on that tracker. Passwords
            are encrypted at rest with the master key — they never appear in
            this page.
          </p>
        </div>
        <Button onClick={() => setShowAdd(true)}>
          <Plus className="size-4" />
          Add account
        </Button>
      </header>

      <AnimatePresence>
        {showAdd && (
          <AddCredentialCard
            trackers={trackers}
            existing={credentials}
            onClose={() => setShowAdd(false)}
            onCreated={() => {
              setShowAdd(false);
              qc.invalidateQueries({ queryKey: QK.credentials });
            }}
          />
        )}
        {editingId && (
          <EditCredentialCard
            key={editingId}
            credential={credentials.find((c) => c.id === editingId)!}
            onClose={() => setEditingId(null)}
            onSaved={() => {
              setEditingId(null);
              qc.invalidateQueries({ queryKey: QK.credentials });
            }}
          />
        )}
      </AnimatePresence>

      {isLoading ? (
        <Card>
          <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Loading accounts...
          </div>
        </Card>
      ) : credentials.length === 0 ? (
        <Card>
          <div className="flex flex-col items-center gap-3 p-16 text-center">
            <div className="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20">
              <KeyRound className="size-6" />
            </div>
            <div className="text-lg font-medium">No tracker accounts yet</div>
            <div className="max-w-sm text-sm text-muted-foreground">
              Add an account for any tracker that needs login (LostFilm,
              RuTracker, Kinozal, etc.). Marauder validates the credentials
              before saving — if Login fails the credential is not stored.
            </div>
            <Button className="mt-3" onClick={() => setShowAdd(true)}>
              <Plus className="size-4" />
              Add your first account
            </Button>
          </div>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {credentials.map((c) => (
            <ResourceCard
              key={c.id}
              title={c.display_name}
              badges={
                <>
                  <Badge variant="outline" className="font-mono">
                    {c.tracker_name}
                  </Badge>
                  <span className="text-xs text-muted-foreground">
                    {c.username}
                  </span>
                </>
              }
              actions={
                <>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => setEditingId(c.id)}
                    aria-label="Edit credential"
                  >
                    <Pencil className="size-4" />
                  </Button>
                  <DeleteConfirm
                    onConfirm={() => del.mutate(c.id)}
                    isPending={del.isPending && del.variables === c.id}
                    label="Delete credential"
                  />
                </>
              }
            >
              <div className="mt-4 flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => test.mutate(c.id)}
                  disabled={test.isPending && test.variables === c.id}
                >
                  {test.isPending && test.variables === c.id ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : test.isSuccess && test.variables === c.id ? (
                    <CheckCircle2 className="size-4 text-success" />
                  ) : test.isError && test.variables === c.id ? (
                    <AlertCircle className="size-4 text-destructive" />
                  ) : (
                    <CheckCircle2 className="size-4" />
                  )}
                  Test login
                </Button>
              </div>
              {test.isError && test.variables === c.id && (
                <div className="mt-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive">
                  {(test.error as Error)?.message}
                </div>
              )}
            </ResourceCard>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------

type Tracker = { name: string; display_name: string; supports_interactive_login: boolean };

// CaptchaState groups every captcha-step field into ONE useState object
// so AddCredentialCard stays within the project's 8-useState limit.
interface CaptchaState {
  challengeId: string;
  captchaImage: string;
  answer: string;
  error: string | null;
}

function AddCredentialCard({
  trackers,
  existing,
  onClose,
  onCreated,
}: {
  trackers: Tracker[];
  existing: CredentialView[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const t = useT();
  // Surface only trackers that don't already have a credential. The
  // unique (user_id, tracker_name) constraint would reject duplicates
  // anyway; this saves a roundtrip.
  const taken = new Set(existing.map((c) => c.tracker_name));
  const available = trackers.filter((t) => !taken.has(t.name));
  const [trackerName, setTrackerName] = useState(available[0]?.name ?? "");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  // null until a tracker hands back a captcha challenge.
  const [captcha, setCaptcha] = useState<CaptchaState | null>(null);

  // Capability is sourced by name from the same /system/info tracker list
  // that populates the dropdown — no extra request, no error-string
  // sniffing. True → interactive (captcha) flow; false → plain create.
  const supportsInteractiveLogin =
    available.find((tr) => tr.name === trackerName)?.supports_interactive_login ?? false;

  // Plain create — used for trackers that don't support interactive login.
  const create = useMutation({
    mutationFn: () =>
      api.post<CredentialView>("/credentials", {
        tracker_name: trackerName,
        username,
        password,
      }),
    onSuccess: () => onCreated(),
    onError: (err) => setError(problemDetail(err)),
  });

  // Step 1 of the interactive flow: begin. logged_in → done; captcha →
  // show the challenge. Only reached for interactive-capable trackers.
  const begin = useMutation({
    mutationFn: () =>
      api.interactiveBegin({ tracker_name: trackerName, username, password }),
    onSuccess: (res) => {
      if (res.status === "logged_in") {
        onCreated();
        return;
      }
      setCaptcha({
        challengeId: res.challenge_id ?? "",
        captchaImage: res.captcha_image ?? "",
        answer: "",
        error: null,
      });
    },
    onError: (err) => setError(problemDetail(err)),
  });

  // Fetches a fresh captcha image on the same pending session. Swaps the
  // image + challenge id but leaves the `error` slot to the caller: the
  // manual refresh button clears it, the auto-refresh after a wrong answer
  // keeps the "incorrect code" message. Routing the auto-refresh through
  // this mutation (not a bare api call) gives it a loading state and a
  // single error path.
  const refresh = useMutation({
    mutationFn: () =>
      api.interactiveRefresh({
        tracker_name: trackerName,
        challenge_id: captcha!.challengeId,
      }),
    onSuccess: (fresh) =>
      setCaptcha((prev) =>
        prev
          ? { ...prev, challengeId: fresh.challenge_id, captchaImage: fresh.captcha_image, answer: "" }
          : prev,
      ),
    onError: (err) =>
      setCaptcha((prev) => (prev ? { ...prev, error: problemDetail(err) } : prev)),
  });

  // Step 2: submit the captcha answer. Wrong answer → inline message AND a
  // fresh image (via the refresh mutation) so the user can retry the same
  // flow without re-entering credentials.
  const complete = useMutation({
    mutationFn: (answer: string) =>
      api.interactiveComplete({
        tracker_name: trackerName,
        challenge_id: captcha!.challengeId,
        answer,
      }),
    onSuccess: () => onCreated(),
    onError: (err) => {
      if (err instanceof ApiError && err.problem.detail === "captcha_incorrect") {
        setCaptcha((prev) =>
          prev ? { ...prev, error: t("credentials.captchaIncorrect") } : prev,
        );
        // Swap in a fresh image; refresh.onError surfaces any failure here.
        refresh.mutate();
        return;
      }
      setCaptcha((prev) => (prev ? { ...prev, error: problemDetail(err) } : prev));
    },
  });

  const submitting = begin.isPending || create.isPending;

  return (
    <motion.div
      initial={{ opacity: 0, y: -8, height: 0 }}
      animate={{ opacity: 1, y: 0, height: "auto" }}
      exit={{ opacity: 0, y: -8, height: 0 }}
      transition={{ duration: 0.2 }}
    >
      <Card className="overflow-hidden">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setError(null);
            if (supportsInteractiveLogin) {
              begin.mutate();
            } else {
              create.mutate();
            }
          }}
          className="space-y-4 p-6"
        >
          <h3 className="text-base font-semibold">Add a tracker account</h3>
          <p className="text-xs text-muted-foreground">
            Marauder will attempt to log in with these credentials before
            saving them. If Login fails, the credential is not stored.
          </p>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="cred-tracker">Tracker</Label>
              <select
                id="cred-tracker"
                value={trackerName}
                onChange={(e) => setTrackerName(e.target.value)}
                disabled={!!captcha}
                className="flex h-10 w-full rounded-md border border-input bg-background/50 px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:cursor-not-allowed disabled:opacity-50"
                required
              >
                {available.length === 0 && <option value="">No trackers left</option>}
                {available.map((tr) => (
                  <option key={tr.name} value={tr.name}>
                    {tr.display_name}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="cred-username">Username / email</Label>
              <Input
                id="cred-username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                disabled={!!captcha}
                autoComplete="username"
                required
              />
            </div>
            <div className="space-y-1.5 sm:col-span-2">
              <Label htmlFor="cred-password">Password</Label>
              <Input
                id="cred-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                disabled={!!captcha}
                autoComplete="new-password"
                required
              />
              <p className="text-xs text-muted-foreground">
                Encrypted at rest with the master key. Marauder admins cannot
                decrypt it without the master key file.
              </p>
            </div>
          </div>

          {captcha && (
            <CaptchaChallenge
              imageSrc={captcha.captchaImage}
              answer={captcha.answer}
              errorText={captcha.error}
              isRefreshing={refresh.isPending}
              onAnswerChange={(value) =>
                setCaptcha((prev) => (prev ? { ...prev, answer: value } : prev))
              }
              onRefresh={() => {
                setCaptcha((prev) => (prev ? { ...prev, error: null } : prev));
                refresh.mutate();
              }}
            />
          )}

          {error && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              {t("credentials.captchaCancel")}
            </Button>
            {captcha ? (
              <Button
                type="button"
                disabled={complete.isPending || refresh.isPending || !captcha.answer}
                onClick={() => complete.mutate(captcha.answer)}
              >
                {complete.isPending && <Loader2 className="size-4 animate-spin" />}
                {t("credentials.captchaSubmit")}
              </Button>
            ) : (
              <Button type="submit" disabled={submitting || !trackerName}>
                {submitting && <Loader2 className="size-4 animate-spin" />}
                Login &amp; save
              </Button>
            )}
          </div>
        </form>
      </Card>
    </motion.div>
  );
}

// Pulls the most useful human-readable message out of an unknown error,
// preferring the RFC-7807 problem detail then title.
function problemDetail(err: unknown): string {
  if (err instanceof ApiError) return err.problem.detail || err.problem.title;
  return String(err);
}

// CaptchaChallenge renders the relayed captcha image, the answer input,
// a refresh control, and an inline error slot. The image is a data URL
// produced server-side — the browser never contacts the tracker.
function CaptchaChallenge({
  imageSrc,
  answer,
  errorText,
  isRefreshing,
  onAnswerChange,
  onRefresh,
}: {
  imageSrc: string;
  answer: string;
  errorText: string | null;
  isRefreshing: boolean;
  onAnswerChange: (value: string) => void;
  onRefresh: () => void;
}) {
  const t = useT();
  return (
    <div className="space-y-2 rounded-md border border-input bg-background/40 p-4">
      <Label htmlFor="cred-captcha">{t("credentials.captchaTitle")}</Label>
      <div className="flex items-center gap-3">
        <img
          src={imageSrc}
          alt={t("credentials.captchaImageAlt")}
          className="h-12 rounded border border-input bg-white"
        />
        <Button
          type="button"
          variant="ghost"
          size="sm"
          onClick={onRefresh}
          disabled={isRefreshing}
        >
          <RefreshCw className={`size-4${isRefreshing ? " animate-spin" : ""}`} />
          {t("credentials.captchaRefresh")}
        </Button>
      </div>
      <Input
        id="cred-captcha"
        value={answer}
        onChange={(e) => onAnswerChange(e.target.value)}
        placeholder={t("credentials.captchaPlaceholder")}
        autoComplete="off"
        autoFocus
      />
      {errorText && <p className="text-sm text-destructive">{errorText}</p>}
    </div>
  );
}

function EditCredentialCard({
  credential,
  onClose,
  onSaved,
}: {
  credential: CredentialView;
  onClose: () => void;
  onSaved: () => void;
}) {
  const [username, setUsername] = useState(credential.username);
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);

  const save = useMutation({
    mutationFn: () =>
      api.put<CredentialView>(`/credentials/${credential.id}`, {
        username,
        password,
      }),
    onSuccess: () => onSaved(),
    onError: (err) => {
      const detail =
        err instanceof ApiError ? err.problem.detail || err.problem.title : String(err);
      setError(detail);
    },
  });

  return (
    <motion.div
      initial={{ opacity: 0, y: -8, height: 0 }}
      animate={{ opacity: 1, y: 0, height: "auto" }}
      exit={{ opacity: 0, y: -8, height: 0 }}
      transition={{ duration: 0.2 }}
    >
      <Card className="overflow-hidden">
        <form
          onSubmit={(e) => {
            e.preventDefault();
            setError(null);
            save.mutate();
          }}
          className="space-y-4 p-6"
        >
          <h3 className="text-base font-semibold">
            Edit account{" "}
            <span className="font-mono text-xs text-muted-foreground">
              ({credential.tracker_name})
            </span>
          </h3>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="edit-cred-username">Username / email</Label>
              <Input
                id="edit-cred-username"
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                autoComplete="username"
                required
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="edit-cred-password">New password</Label>
              <Input
                id="edit-cred-password"
                type="password"
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                autoComplete="new-password"
                placeholder="Leave blank to keep current"
              />
            </div>
          </div>

          {error && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending}>
              {save.isPending && <Loader2 className="size-4 animate-spin" />}
              Save
            </Button>
          </div>
        </form>
      </Card>
    </motion.div>
  );
}
