import { useEffect, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { Loader2 } from "lucide-react";

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { fieldsForPlugin } from "@/pages/Notifiers";

interface EditNotifierCardProps {
  id: string;
  onClose: () => void;
  onSaved: () => void;
}

export function EditNotifierCard({ id, onClose, onSaved }: EditNotifierCardProps) {
  const { data, isLoading, isError } = useQuery({
    queryKey: QK.notifier(id),
    queryFn: () => api.getNotifier(id),
  });

  const [displayName, setDisplayName] = useState("");
  const [events, setEvents] = useState<string[]>([]);
  const [isDefault, setIsDefault] = useState(false);
  const [config, setConfig] = useState<Record<string, string>>({});
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!data) return;
    setDisplayName(data.display_name);
    setEvents(data.events);
    setIsDefault(data.is_default);
    const flat: Record<string, string> = {};
    for (const [k, v] of Object.entries(data.config ?? {})) {
      flat[k] = typeof v === "string" ? v : String(v ?? "");
    }
    setConfig(flat);
  }, [data]);

  const save = useMutation({
    mutationFn: () =>
      api.updateNotifier(id, {
        display_name: displayName,
        events,
        is_default: isDefault,
        config,
      }),
    onSuccess: () => onSaved(),
    onError: (err) => setError(err instanceof Error ? err.message : "Failed"),
  });

  const fields = data ? fieldsForPlugin(data.notifier_name) : [];

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
            Edit notifier{" "}
            {data && (
              <span className="font-mono text-xs text-muted-foreground">
                ({data.notifier_name})
              </span>
            )}
          </h3>

          {isLoading && (
            <div className="flex items-center gap-2 text-sm text-muted-foreground">
              <Loader2 className="size-4 animate-spin" /> Loading current config...
            </div>
          )}
          {isError && (
            <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              Failed to load notifier config.
            </div>
          )}

          {data && (
            <>
              <div className="space-y-1.5">
                <Label htmlFor="edit-ndisplay">Display name</Label>
                <Input
                  id="edit-ndisplay"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  required
                />
              </div>

              <div className="grid gap-4 sm:grid-cols-2">
                {fields.map((f) => (
                  <div key={f.key} className="space-y-1.5">
                    <Label htmlFor={`edit-n-${f.key}`}>{f.label}</Label>
                    <Input
                      id={`edit-n-${f.key}`}
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

              <div className="flex flex-wrap items-center gap-4 text-sm">
                <span className="text-muted-foreground">Notify on:</span>
                {(["updated", "error"] as const).map((ev) => (
                  <label key={ev} className="inline-flex items-center gap-1.5">
                    <input
                      type="checkbox"
                      checked={events.includes(ev)}
                      onChange={(e) =>
                        setEvents((prev) =>
                          e.target.checked ? [...prev, ev] : prev.filter((x) => x !== ev),
                        )
                      }
                    />
                    {ev === "updated" ? "new releases" : "errors"}
                  </label>
                ))}
              </div>

              <label className="inline-flex items-center gap-2 text-sm">
                <input
                  type="checkbox"
                  checked={isDefault}
                  onChange={(e) => setIsDefault(e.target.checked)}
                />
                Use as a default notifier (one per type)
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
              Save changes
            </Button>
          </div>
        </form>
      </Card>
    </motion.div>
  );
}
