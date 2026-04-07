import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { Plus, Trash2, Loader2, CheckCircle2, Server, AlertCircle } from "lucide-react";

import { api, type SystemInfo } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";

type ClientView = {
  id: string;
  client_name: string;
  display_name: string;
  is_default: boolean;
  created_at: string;
  updated_at: string;
};

type ClientsList = { clients: ClientView[] | null };

export function ClientsPage() {
  const qc = useQueryClient();
  const { data: clientsData, isLoading } = useQuery({
    queryKey: ["clients"],
    queryFn: () => api.get<ClientsList>("/clients"),
  });
  const { data: systemInfo } = useQuery({
    queryKey: ["system-info"],
    queryFn: () => api.get<SystemInfo>("/system/info", { auth: false }),
  });
  const [showAdd, setShowAdd] = useState(false);
  const clients = clientsData?.clients ?? [];
  const availablePlugins = systemInfo?.clients ?? [];

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/clients/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["clients"] }),
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
              qc.invalidateQueries({ queryKey: ["clients"] });
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
                      <div className="text-base font-semibold">
                        {c.display_name}
                      </div>
                      <div className="mt-0.5 flex items-center gap-2">
                        <Badge variant="outline" className="font-mono">
                          {c.client_name}
                        </Badge>
                        {c.is_default && (
                          <Badge variant="success">default</Badge>
                        )}
                      </div>
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="text-destructive opacity-0 group-hover:opacity-100"
                      onClick={() => del.mutate(c.id)}
                      aria-label="Delete client"
                    >
                      <Trash2 className="size-4" />
                    </Button>
                  </div>
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
};

// Hard-coded field hints. The backend ConfigSchema is the source of truth;
// this is just a UX shortcut so the form renders without a JSON-Schema
// renderer for v0.1. v0.2 will derive these from the API response.
function fieldsForPlugin(name: string): Field[] {
  switch (name) {
    case "qbittorrent":
      return [
        { key: "url", label: "URL", placeholder: "http://qbittorrent:6611" },
        { key: "username", label: "Username", placeholder: "admin" },
        { key: "password", label: "Password", password: true },
        { key: "category", label: "Category (optional)" },
      ];
    case "downloadfolder":
      return [
        { key: "path", label: "Folder path", placeholder: "/downloads" },
      ];
    case "transmission":
      return [
        { key: "url", label: "RPC URL", placeholder: "http://transmission:9091/transmission/rpc" },
        { key: "username", label: "Username (optional)" },
        { key: "password", label: "Password (optional)", password: true },
      ];
    case "deluge":
      return [
        { key: "url", label: "Web URL", placeholder: "http://deluge:8112" },
        { key: "password", label: "Password", password: true },
      ];
    default:
      return [];
  }
}
