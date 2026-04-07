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
  Trash2,
} from "lucide-react";

import { api, ApiError, type SystemInfo } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";

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
    queryKey: ["credentials"],
    queryFn: () => api.get<CredList>("/credentials"),
  });
  const { data: systemInfo } = useQuery({
    queryKey: ["system-info"],
    queryFn: () => api.get<SystemInfo>("/system/info", { auth: false }),
  });

  const [showAdd, setShowAdd] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const credentials = credsData?.credentials ?? [];
  const trackers = systemInfo?.trackers ?? [];

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/credentials/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["credentials"] }),
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
              qc.invalidateQueries({ queryKey: ["credentials"] });
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
              qc.invalidateQueries({ queryKey: ["credentials"] });
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
            <motion.div
              key={c.id}
              initial={{ opacity: 0, y: 8 }}
              animate={{ opacity: 1, y: 0 }}
            >
              <Card className="group relative overflow-hidden">
                <div className="pointer-events-none absolute -right-12 -top-12 size-40 rounded-full bg-gradient-to-br from-primary/30 to-accent/10 blur-2xl" />
                <div className="relative p-6">
                  <div className="mb-2 flex items-start justify-between gap-2">
                    <div>
                      <div className="text-base font-semibold">{c.display_name}</div>
                      <div className="mt-0.5 flex items-center gap-2">
                        <Badge variant="outline" className="font-mono">
                          {c.tracker_name}
                        </Badge>
                        <span className="text-xs text-muted-foreground">
                          {c.username}
                        </span>
                      </div>
                    </div>
                    <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100">
                      <Button
                        variant="ghost"
                        size="icon"
                        onClick={() => setEditingId(c.id)}
                        aria-label="Edit credential"
                      >
                        <Pencil className="size-4" />
                      </Button>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="text-destructive"
                        onClick={() => del.mutate(c.id)}
                        aria-label="Delete credential"
                      >
                        <Trash2 className="size-4" />
                      </Button>
                    </div>
                  </div>
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
                </div>
              </Card>
            </motion.div>
          ))}
        </div>
      )}
    </div>
  );
}

// ---------------------------------------------------------------------

type Tracker = { name: string; display_name: string };

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
  // Surface only trackers that don't already have a credential. The
  // unique (user_id, tracker_name) constraint would reject duplicates
  // anyway; this saves a roundtrip.
  const taken = new Set(existing.map((c) => c.tracker_name));
  const available = trackers.filter((t) => !taken.has(t.name));
  const [trackerName, setTrackerName] = useState(available[0]?.name ?? "");
  const [username, setUsername] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      api.post<CredentialView>("/credentials", {
        tracker_name: trackerName,
        username,
        password,
      }),
    onSuccess: () => onCreated(),
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
            create.mutate();
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
                className="flex h-10 w-full rounded-md border border-input bg-background/50 px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                required
              >
                {available.length === 0 && <option value="">No trackers left</option>}
                {available.map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.display_name}
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
                autoComplete="new-password"
                required
              />
              <p className="text-xs text-muted-foreground">
                Encrypted at rest with the master key. Marauder admins cannot
                decrypt it without the master key file.
              </p>
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
            <Button type="submit" disabled={create.isPending || !trackerName}>
              {create.isPending && <Loader2 className="size-4 animate-spin" />}
              Login &amp; save
            </Button>
          </div>
        </form>
      </Card>
    </motion.div>
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
