import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { Plus, Loader2, CheckCircle2, Bell, AlertCircle } from "lucide-react";

import { api } from "@/lib/api";
import { useSystemInfo } from "@/lib/hooks/useSystemInfo";
import { QK } from "@/lib/queryKeys";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { DeleteConfirm } from "@/components/shared/DeleteConfirm";
import { ResourceCard } from "@/components/shared/ResourceCard";

type NotifierView = {
  id: string;
  notifier_name: string;
  display_name: string;
  events: string[];
  created_at: string;
  updated_at: string;
};
type NotifiersList = { notifiers: NotifierView[] | null };

export function NotifiersPage() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: QK.notifiers,
    queryFn: () => api.get<NotifiersList>("/notifiers"),
  });
  const { data: systemInfo } = useSystemInfo();
  const [showAdd, setShowAdd] = useState(false);
  const items = data?.notifiers ?? [];
  const plugins = systemInfo?.notifiers ?? [];

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/notifiers/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.notifiers }),
  });
  const test = useMutation({
    mutationFn: (id: string) => api.post<{ ok: boolean }>(`/notifiers/${id}/test`),
  });

  return (
    <div className="space-y-8">
      <header className="flex items-start justify-between gap-4">
        <div>
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-primary">
            alerts
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">Notifiers</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Where Marauder pings you when a topic updates or fails.
          </p>
        </div>
        <Button onClick={() => setShowAdd(true)}>
          <Plus className="size-4" />
          Add notifier
        </Button>
      </header>

      <AnimatePresence>
        {showAdd && (
          <AddNotifierCard
            plugins={plugins}
            onClose={() => setShowAdd(false)}
            onCreated={() => {
              setShowAdd(false);
              qc.invalidateQueries({ queryKey: QK.notifiers });
            }}
          />
        )}
      </AnimatePresence>

      {isLoading ? (
        <Card>
          <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Loading...
          </div>
        </Card>
      ) : items.length === 0 ? (
        <Card>
          <div className="flex flex-col items-center gap-3 p-16 text-center">
            <div className="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20">
              <Bell className="size-6" />
            </div>
            <div className="text-lg font-medium">No notifiers yet</div>
            <div className="max-w-sm text-sm text-muted-foreground">
              Add a Telegram bot, email server, webhook, or Pushover device
              so Marauder can let you know when topics update.
            </div>
            <Button className="mt-3" onClick={() => setShowAdd(true)}>
              <Plus className="size-4" />
              Add your first notifier
            </Button>
          </div>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {items.map((n) => (
            <ResourceCard
              key={n.id}
              glow="accent"
              title={n.display_name}
              badges={
                <>
                  <Badge variant="outline" className="font-mono">
                    {n.notifier_name}
                  </Badge>
                  {n.events.map((e) => (
                    <Badge key={e} variant="secondary">
                      {e}
                    </Badge>
                  ))}
                </>
              }
              actions={
                <DeleteConfirm
                  onConfirm={() => del.mutate(n.id)}
                  isPending={del.isPending && del.variables === n.id}
                  label="Delete notifier"
                />
              }
            >
              <div className="mt-4">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => test.mutate(n.id)}
                  disabled={test.isPending}
                >
                  {test.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : test.isSuccess && test.variables === n.id ? (
                    <CheckCircle2 className="size-4 text-success" />
                  ) : test.isError && test.variables === n.id ? (
                    <AlertCircle className="size-4 text-destructive" />
                  ) : (
                    <CheckCircle2 className="size-4" />
                  )}
                  Send test
                </Button>
              </div>
              {test.isError && test.variables === n.id && (
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

type Plugin = { name: string; display_name: string };

function AddNotifierCard({
  plugins,
  onClose,
  onCreated,
}: {
  plugins: Plugin[];
  onClose: () => void;
  onCreated: () => void;
}) {
  const [pluginName, setPluginName] = useState(plugins[0]?.name ?? "");
  const [displayName, setDisplayName] = useState("");
  const [config, setConfig] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);

  const fields = fieldsForPlugin(pluginName);

  const create = useMutation({
    mutationFn: () =>
      api.post("/notifiers", {
        notifier_name: pluginName,
        display_name: displayName,
        config,
      }),
    onSuccess: () => onCreated(),
    onError: (err) => setError(err instanceof Error ? err.message : "Failed"),
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
          <h3 className="text-base font-semibold">Add a notifier</h3>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="np">Plugin</Label>
              <select
                id="np"
                value={pluginName}
                onChange={(e) => {
                  setPluginName(e.target.value);
                  setConfig({});
                }}
                className="flex h-10 w-full rounded-md border border-input bg-background/50 px-3 py-2 text-sm shadow-[inset_0_1px_0_hsl(0_0%_100%/0.02)] ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
                required
              >
                {plugins.map((p) => (
                  <option key={p.name} value={p.name}>
                    {p.display_name}
                  </option>
                ))}
              </select>
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="nd">Display name</Label>
              <Input
                id="nd"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder="e.g. Telegram alerts"
                required
              />
            </div>
          </div>

          <div className="grid gap-4 sm:grid-cols-2">
            {fields.map((f) => (
              <div key={f.key} className="space-y-1.5">
                <Label htmlFor={f.key}>{f.label}</Label>
                <Input
                  id={f.key}
                  type={f.password ? "password" : "text"}
                  value={config[f.key] ?? ""}
                  onChange={(e) =>
                    setConfig((c) => ({ ...c, [f.key]: e.target.value }))
                  }
                  placeholder={f.placeholder}
                />
              </div>
            ))}
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
            <Button type="submit" disabled={create.isPending}>
              {create.isPending && <Loader2 className="size-4 animate-spin" />}
              Test &amp; save
            </Button>
          </div>
        </form>
      </Card>
    </motion.div>
  );
}

type Field = { key: string; label: string; placeholder?: string; password?: boolean };

function fieldsForPlugin(name: string): Field[] {
  switch (name) {
    case "telegram":
      return [
        { key: "bot_token", label: "Bot token", password: true, placeholder: "12345:abcdef..." },
        { key: "chat_id", label: "Chat ID", placeholder: "777" },
      ];
    case "email":
      return [
        { key: "smtp_host", label: "SMTP host", placeholder: "smtp.example.com" },
        { key: "smtp_port", label: "Port", placeholder: "587" },
        { key: "username", label: "Username" },
        { key: "password", label: "Password", password: true },
        { key: "from", label: "From", placeholder: "marauder@example.com" },
        { key: "to", label: "To", placeholder: "you@example.com" },
      ];
    case "webhook":
      return [{ key: "url", label: "URL", placeholder: "https://hooks.example.com/marauder" }];
    case "pushover":
      return [
        { key: "user_key", label: "User key" },
        { key: "app_token", label: "App token", password: true },
      ];
    default:
      return [];
  }
}
