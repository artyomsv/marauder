import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import { Plus, Trash2, Loader2, AlertTriangle } from "lucide-react";

import { api, type Topic } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { formatRelative } from "@/lib/utils";

type TopicsList = { topics: Topic[] | null };

export function TopicsPage() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ["topics"],
    queryFn: () => api.get<TopicsList>("/topics"),
  });
  const [showAdd, setShowAdd] = useState(false);

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/topics/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ["topics"] }),
  });

  const topics = data?.topics ?? [];

  return (
    <div className="space-y-8">
      <header className="flex items-start justify-between gap-4">
        <div>
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-primary">
            watchlist
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">Topics</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Every URL Marauder is actively monitoring for you.
          </p>
        </div>
        <Button onClick={() => setShowAdd(true)}>
          <Plus className="size-4" />
          Add topic
        </Button>
      </header>

      <AnimatePresence>
        {showAdd && (
          <AddTopicCard
            onClose={() => setShowAdd(false)}
            onCreated={() => {
              setShowAdd(false);
              qc.invalidateQueries({ queryKey: ["topics"] });
            }}
          />
        )}
      </AnimatePresence>

      <Card>
        {isLoading ? (
          <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Loading topics...
          </div>
        ) : topics.length === 0 ? (
          <EmptyState onAdd={() => setShowAdd(true)} />
        ) : (
          <div className="divide-y divide-border/60">
            {topics.map((t) => (
              <motion.div
                key={t.ID}
                initial={{ opacity: 0 }}
                animate={{ opacity: 1 }}
                className="group flex items-center gap-4 p-4 hover:bg-accent/5"
              >
                <StatusIndicator status={t.Status} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate font-medium">{t.DisplayName}</span>
                    <Badge variant="outline" className="font-mono">
                      {t.TrackerName}
                    </Badge>
                  </div>
                  <div className="truncate font-mono text-xs text-muted-foreground">
                    {t.URL}
                  </div>
                  {t.LastError && (
                    <div className="mt-1 flex items-center gap-1.5 text-xs text-destructive">
                      <AlertTriangle className="size-3" />
                      {t.LastError}
                    </div>
                  )}
                </div>
                <div className="hidden lg:block text-right">
                  <div className="text-xs text-muted-foreground">checked</div>
                  <div className="text-sm">
                    {formatRelative(t.LastCheckedAt)}
                  </div>
                </div>
                <div className="hidden xl:block text-right">
                  <div className="text-xs text-muted-foreground">updated</div>
                  <div className="text-sm">
                    {formatRelative(t.LastUpdatedAt)}
                  </div>
                </div>
                <Button
                  variant="ghost"
                  size="icon"
                  className="opacity-0 group-hover:opacity-100 text-destructive"
                  onClick={() => del.mutate(t.ID)}
                  aria-label="Delete topic"
                >
                  <Trash2 className="size-4" />
                </Button>
              </motion.div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}

function EmptyState({ onAdd }: { onAdd: () => void }) {
  return (
    <div className="flex flex-col items-center gap-3 p-16 text-center">
      <div className="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20">
        <Plus className="size-6" />
      </div>
      <div className="text-lg font-medium">No topics yet</div>
      <div className="max-w-sm text-sm text-muted-foreground">
        Paste a tracker URL, magnet link, or .torrent URL to start watching.
      </div>
      <Button className="mt-3" onClick={onAdd}>
        <Plus className="size-4" />
        Add your first topic
      </Button>
    </div>
  );
}

function AddTopicCard({
  onClose,
  onCreated,
}: {
  onClose: () => void;
  onCreated: () => void;
}) {
  const [url, setUrl] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [error, setError] = useState<string | null>(null);

  const create = useMutation({
    mutationFn: () =>
      api.post<Topic>("/topics", {
        url,
        display_name: displayName || undefined,
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
          <h3 className="text-base font-semibold">Add a new topic</h3>
          <div className="space-y-1.5">
            <Label htmlFor="url">URL or magnet link</Label>
            <Input
              id="url"
              required
              value={url}
              onChange={(e) => setUrl(e.target.value)}
              placeholder="magnet:?xt=urn:btih:... or https://tracker.example.com/.../file.torrent"
            />
          </div>
          <div className="space-y-1.5">
            <Label htmlFor="display">Display name (optional)</Label>
            <Input
              id="display"
              value={displayName}
              onChange={(e) => setDisplayName(e.target.value)}
              placeholder="Leave blank to auto-detect"
            />
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
              Add topic
            </Button>
          </div>
        </form>
      </Card>
    </motion.div>
  );
}

function StatusIndicator({ status }: { status: Topic["Status"] }) {
  const cls =
    status === "active"
      ? "bg-success"
      : status === "error"
      ? "bg-destructive"
      : "bg-muted-foreground";
  return (
    <span className="relative flex size-2.5">
      <span
        className={`absolute inline-flex h-full w-full animate-ping rounded-full ${cls} opacity-40`}
      />
      <span className={`relative inline-flex size-2.5 rounded-full ${cls}`} />
    </span>
  );
}
