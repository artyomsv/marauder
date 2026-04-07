import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { Plus, Pencil, Loader2, CheckCircle2, Server, AlertCircle } from "lucide-react";

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

type ClientView = {
  id: string;
  client_name: string;
  display_name: string;
  is_default: boolean;
  config?: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

type ClientsList = { clients: ClientView[] | null };

export function ClientsPage() {
  const qc = useQueryClient();
  const { data: clientsData, isLoading } = useQuery({
    queryKey: QK.clients,
    queryFn: () => api.get<ClientsList>("/clients"),
  });
  const { data: systemInfo } = useSystemInfo();
  const [showAdd, setShowAdd] = useState(false);
  const [editingId, setEditingId] = useState<string | null>(null);
  const clients = clientsData?.clients ?? [];
  const availablePlugins = systemInfo?.clients ?? [];

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/clients/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.clients }),
  });

  const test = useMutation({
    mutationFn: (id: string) => api.post<{ ok: boolean }>(`/clients/${id}/test`),
  });

  return (
    <div className="space-y-8">
      <header className="flex items-start justify-between gap-4">
        <div>
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-primary">
            delivery
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">Clients</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Where Marauder hands torrents off when a topic updates.
          </p>
        </div>
        <Button onClick={() => setShowAdd(true)}>
          <Plus className="size-4" />
          Add client
        </Button>
      </header>

      <AnimatePresence>
        {showAdd && (
          <AddClientCard
            plugins={availablePlugins}
            onClose={() => setShowAdd(false)}
            onCreated={() => {
              setShowAdd(false);
              qc.invalidateQueries({ queryKey: QK.clients });
            }}
          />
        )}
        {editingId && (
          <EditClientCard
            key={editingId}
            id={editingId}
            onClose={() => setEditingId(null)}
            onSaved={() => {
              setEditingId(null);
              qc.invalidateQueries({ queryKey: QK.clients });
            }}
          />
        )}
      </AnimatePresence>

      {isLoading ? (
        <Card>
          <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Loading clients...
          </div>
        </Card>
      ) : clients.length === 0 ? (
        <Card>
          <div className="flex flex-col items-center gap-3 p-16 text-center">
            <div className="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20">
              <Server className="size-6" />
            </div>
            <div className="text-lg font-medium">No clients yet</div>
            <div className="max-w-sm text-sm text-muted-foreground">
              Add a torrent client (qBittorrent, Transmission, Deluge, or
              a download folder) so Marauder has somewhere to send updates.
            </div>
            <Button className="mt-3" onClick={() => setShowAdd(true)}>
              <Plus className="size-4" />
              Add your first client
            </Button>
          </div>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2">
          {clients.map((c) => (
            <ResourceCard
              key={c.id}
              title={c.display_name}
              badges={
                <>
                  <Badge variant="outline" className="font-mono">
                    {c.client_name}
                  </Badge>
                  {c.is_default && <Badge variant="success">default</Badge>}
                </>
              }
              actions={
                <>
                  <Button
                    variant="ghost"
                    size="icon"
                    onClick={() => setEditingId(c.id)}
                    aria-label="Edit client"
                  >
                    <Pencil className="size-4" />
                  </Button>
                  <DeleteConfirm
                    onConfirm={() => del.mutate(c.id)}
                    isPending={del.isPending && del.variables === c.id}
                    label="Delete client"
                  />
                </>
              }
            >
              <div className="mt-4 flex items-center gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  onClick={() => test.mutate(c.id)}
                  disabled={test.isPending}
                >
                  {test.isPending ? (
                    <Loader2 className="size-4 animate-spin" />
                  ) : test.isSuccess && test.variables === c.id ? (
                    <CheckCircle2 className="size-4 text-success" />
                  ) : test.isError && test.variables === c.id ? (
                    <AlertCircle className="size-4 text-destructive" />
                  ) : (
                    <CheckCircle2 className="size-4" />
                  )}
                  Test connection
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

type Plugin = { name: string; display_name: string };

function AddClientCard({
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
  const [isDefault, setIsDefault] = useState(false);
  const [config, setConfig] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);

  const fields = fieldsForPlugin(pluginName);

  const create = useMutation({
    mutationFn: () =>
      api.post("/clients", {
        client_name: pluginName,
        display_name: displayName,
        is_default: isDefault,
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
          <h3 className="text-base font-semibold">Add a torrent client</h3>

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="space-y-1.5">
              <Label htmlFor="plugin">Plugin</Label>
              <select
                id="plugin"
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
              <Label htmlFor="display">Display name</Label>
              <Input
                id="display"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder="e.g. Living room qBit"
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
                {f.helpText && (
                  <p className="text-xs text-muted-foreground">{f.helpText}</p>
                )}
              </div>
            ))}
          </div>

          <p className="text-xs text-muted-foreground">
            Need help with the URL format?{" "}
            <a
              href="https://github.com/artyomsv/marauder/blob/main/docs/clients.md"
              target="_blank"
              rel="noreferrer"
              className="text-primary underline-offset-4 hover:underline"
            >
              Read the client setup guide →
            </a>
          </p>

          <label className="inline-flex items-center gap-2 text-sm">
            <input
              type="checkbox"
              checked={isDefault}
              onChange={(e) => setIsDefault(e.target.checked)}
            />
            Use as default client for new topics
          </label>

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

type Field = {
  key: string;
  label: string;
  placeholder?: string;
  password?: boolean;
  /** Inline help text rendered below the input. Markdown is not supported;
   *  use plain strings. */
  helpText?: string;
};

// Hard-coded field hints. The backend ConfigSchema is the source of truth;
// this is just a UX shortcut so the form renders without a JSON-Schema
// renderer. v0.5 will switch to schema-driven rendering.
function fieldsForPlugin(name: string): Field[] {
  switch (name) {
    case "qbittorrent":
      return [
        {
          key: "url",
          label: "URL",
          placeholder: "http://qbittorrent:8080",
          helpText:
            "Host and port of the qBittorrent Web UI — no path. Default port is 8080. Example: http://192.168.1.10:8080",
        },
        { key: "username", label: "Username", placeholder: "admin" },
        { key: "password", label: "Password", password: true },
        { key: "category", label: "Category (optional)" },
      ];
    case "downloadfolder":
      return [
        {
          key: "path",
          label: "Folder path",
          placeholder: "/downloads",
          helpText:
            "Filesystem path the backend can write to. SABnzbd / NZBGet watch folders work here too.",
        },
      ];
    case "transmission":
      return [
        {
          key: "url",
          label: "RPC URL",
          placeholder: "http://192.168.1.10:9091/transmission/rpc",
          helpText:
            "Use the full RPC URL ending in /transmission/rpc. Default Transmission Web UI port is 9091; some packages (e.g. transmission-daemon) use 8083 or 9091. Example: http://192.168.2.65:8083/transmission/rpc",
        },
        { key: "username", label: "Username (optional)" },
        { key: "password", label: "Password (optional)", password: true },
      ];
    case "deluge":
      return [
        {
          key: "url",
          label: "Web URL",
          placeholder: "http://deluge:8112",
          helpText:
            "Host and port of the Deluge Web UI. Default port is 8112. The plugin appends /json automatically.",
        },
        { key: "password", label: "Password", password: true },
      ];
    case "utorrent":
      return [
        {
          key: "url",
          label: "Web UI URL",
          placeholder: "http://192.168.1.10:8080/gui/",
          helpText:
            "Full µTorrent Web UI URL ending in /gui/. Default port is 8080.",
        },
        { key: "username", label: "Username", placeholder: "admin" },
        { key: "password", label: "Password", password: true },
      ];
    default:
      return [];
  }
}

// --- EditClientCard ----------------------------------------------------

function EditClientCard({
  id,
  onClose,
  onSaved,
}: {
  id: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { data, isLoading, isError } = useQuery({
    queryKey: QK.client(id),
    queryFn: () => api.get<ClientView>(`/clients/${id}`),
  });

  const [displayName, setDisplayName] = useState("");
  const [isDefault, setIsDefault] = useState(false);
  const [config, setConfig] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);

  // Hydrate form once the GET completes.
  useEffect(() => {
    if (!data) return;
    setDisplayName(data.display_name);
    setIsDefault(data.is_default);
    const cfg = (data.config ?? {}) as Record<string, unknown>;
    const flat: Record<string, string> = {};
    for (const [k, v] of Object.entries(cfg)) {
      flat[k] = typeof v === "string" ? v : String(v ?? "");
    }
    setConfig(flat);
  }, [data]);

  const save = useMutation({
    mutationFn: () =>
      api.put<ClientView>(`/clients/${id}`, {
        client_name: data?.client_name,
        display_name: displayName,
        is_default: isDefault,
        config,
      }),
    onSuccess: () => onSaved(),
    onError: (err) => setError(err instanceof Error ? err.message : "Failed"),
  });

  const fields = data ? fieldsForPlugin(data.client_name) : [];

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
            Edit client {data && <span className="font-mono text-xs text-muted-foreground">({data.client_name})</span>}
          </h3>

          {isLoading && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> Loading current config...
            </div>
          )}

          {isError && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              Failed to load client config.
            </div>
          )}

          {data && (
            <>
              <div className="grid gap-4 sm:grid-cols-1">
                <div className="space-y-1.5">
                  <Label htmlFor="edit-display">Display name</Label>
                  <Input
                    id="edit-display"
                    value={displayName}
                    onChange={(e) => setDisplayName(e.target.value)}
                    required
                  />
                </div>
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                {fields.map((f) => (
                  <div key={f.key} className="space-y-1.5">
                    <Label htmlFor={`edit-${f.key}`}>{f.label}</Label>
                    <Input
                      id={`edit-${f.key}`}
                      type={f.password ? "password" : "text"}
                      value={config[f.key] ?? ""}
                      onChange={(e) =>
                        setConfig((c) => ({ ...c, [f.key]: e.target.value }))
                      }
                      placeholder={f.placeholder}
                    />
                    {f.helpText && (
                      <p className="text-xs text-muted-foreground">{f.helpText}</p>
                    )}
                  </div>
                ))}
              </div>

              <label className="inline-flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={isDefault}
                  onChange={(e) => setIsDefault(e.target.checked)}
                />
                Use as default client for new topics
              </label>
            </>
          )}

          {error && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {error}
            </div>
          )}

          <div className="flex justify-end gap-2">
            <Button type="button" variant="outline" onClick={onClose}>
              Cancel
            </Button>
            <Button type="submit" disabled={save.isPending || !data}>
              {save.isPending && <Loader2 className="size-4 animate-spin" />}
              Test &amp; save
            </Button>
          </div>
        </form>
      </Card>
    </motion.div>
  );
}
