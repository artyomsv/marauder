import { useEffect, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import {
  Plus,
  Trash2,
  Loader2,
  AlertTriangle,
  Pause,
  Play,
  Rows3,
  Rows4,
  Check,
  X,
} from "lucide-react";

import { api, type Topic } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Badge } from "@/components/ui/badge";
import { cn, formatRelative } from "@/lib/utils";
import { usePrefs } from "@/lib/prefs";
import { DeleteConfirm } from "@/components/shared/DeleteConfirm";
import { QK } from "@/lib/queryKeys";
import { useDebouncedValue } from "@/lib/hooks/useDebouncedValue";
import { useArmedConfirm } from "@/lib/hooks/useArmedConfirm";

type TopicsList = { topics: Topic[] | null };

export function TopicsPage() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: QK.topics,
    queryFn: () => api.get<TopicsList>("/topics"),
  });
  const density = usePrefs((s) => s.density);
  const setDensity = usePrefs((s) => s.setDensity);
  const [showAdd, setShowAdd] = useState(false);
  const [selected, setSelected] = useState<Set<string>>(new Set());

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/topics/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.topics }),
  });
  const pause = useMutation({
    mutationFn: (id: string) => api.post<void>(`/topics/${id}/pause`),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.topics }),
  });
  const resume = useMutation({
    mutationFn: (id: string) => api.post<void>(`/topics/${id}/resume`),
    onSuccess: () => qc.invalidateQueries({ queryKey: QK.topics }),
  });

  const topics = data?.topics ?? [];
  const allSelected = topics.length > 0 && selected.size === topics.length;

  const toggleAll = () => {
    if (allSelected) {
      setSelected(new Set());
    } else {
      setSelected(new Set(topics.map((t) => t.ID)));
    }
  };
  const toggleOne = (id: string) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setSelected(next);
  };
  const bulk = async (op: "pause" | "resume" | "delete") => {
    const ids = Array.from(selected);
    const fn = op === "pause" ? pause : op === "resume" ? resume : del;
    await Promise.all(ids.map((id) => fn.mutateAsync(id)));
    setSelected(new Set());
  };

  const compact = density === "compact";

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
        <div className="flex items-center gap-2">
          <DensityToggle density={density} setDensity={setDensity} />
          <Button onClick={() => setShowAdd(true)}>
            <Plus className="size-4" />
            Add topic
          </Button>
        </div>
      </header>

      <AnimatePresence>
        {showAdd && (
          <AddTopicCard
            onClose={() => setShowAdd(false)}
            onCreated={() => {
              setShowAdd(false);
              qc.invalidateQueries({ queryKey: QK.topics });
            }}
          />
        )}
      </AnimatePresence>

      {selected.size > 0 && (
        <BulkActionBar
          count={selected.size}
          onPause={() => bulk("pause")}
          onResume={() => bulk("resume")}
          onDelete={() => bulk("delete")}
          onClear={() => setSelected(new Set())}
        />
      )}

      <Card>
        {isLoading ? (
          <div className="flex items-center justify-center gap-2 p-12 text-sm text-muted-foreground">
            <Loader2 className="size-4 animate-spin" />
            Loading topics...
          </div>
        ) : topics.length === 0 ? (
          <EmptyState onAdd={() => setShowAdd(true)} />
        ) : (
          <>
            <div className="flex items-center gap-3 border-b border-border/60 px-4 py-2 text-xs text-muted-foreground">
              <input
                type="checkbox"
                checked={allSelected}
                onChange={toggleAll}
                className="size-4 cursor-pointer"
                aria-label="Select all"
              />
              <span>{topics.length} topics</span>
            </div>
            <div className="divide-y divide-border/60">
              {topics.map((t) => (
                <motion.div
                  key={t.ID}
                  initial={{ opacity: 0 }}
                  animate={{ opacity: 1 }}
                  className={cn(
                    "group flex items-center gap-4 hover:bg-accent/5",
                    compact ? "p-2" : "p-4",
                    selected.has(t.ID) && "bg-primary/5",
                  )}
                >
                  <input
                    type="checkbox"
                    checked={selected.has(t.ID)}
                    onChange={() => toggleOne(t.ID)}
                    className="size-4 cursor-pointer"
                    aria-label="Select topic"
                  />
                  <StatusIndicator status={t.Status} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate font-medium">{t.DisplayName}</span>
                      <Badge variant="outline" className="font-mono">
                        {t.TrackerName}
                      </Badge>
                    </div>
                    {!compact && (
                      <div className="truncate font-mono text-xs text-muted-foreground">
                        {t.URL}
                      </div>
                    )}
                    {t.LastError && (
                      <div className="mt-1 flex items-center gap-1.5 text-xs text-destructive">
                        <AlertTriangle className="size-3" />
                        {t.LastError}
                      </div>
                    )}
                  </div>
                  {!compact && (
                    <div className="hidden lg:block text-right">
                      <div className="text-xs text-muted-foreground">checked</div>
                      <div className="text-sm">
                        {formatRelative(t.LastCheckedAt)}
                      </div>
                    </div>
                  )}
                  {!compact && (
                    <div className="hidden xl:block text-right">
                      <div className="text-xs text-muted-foreground">updated</div>
                      <div className="text-sm">
                        {formatRelative(t.LastUpdatedAt)}
                      </div>
                    </div>
                  )}
                  <div className="opacity-0 group-hover:opacity-100">
                    <DeleteConfirm
                      onConfirm={() => del.mutate(t.ID)}
                      isPending={del.isPending && del.variables === t.ID}
                      label="Delete topic"
                    />
                  </div>
                </motion.div>
              ))}
            </div>
          </>
        )}
      </Card>
    </div>
  );
}

function DensityToggle({
  density,
  setDensity,
}: {
  density: "comfortable" | "compact";
  setDensity: (d: "comfortable" | "compact") => void;
}) {
  return (
    <div className="inline-flex rounded-md border border-border/60 bg-background/40 p-0.5">
      <button
        type="button"
        aria-label="Comfortable density"
        onClick={() => setDensity("comfortable")}
        className={cn(
          "flex size-8 items-center justify-center rounded-sm transition-colors",
          density === "comfortable"
            ? "bg-primary/15 text-primary"
            : "text-muted-foreground hover:text-foreground",
        )}
      >
        <Rows3 className="size-4" />
      </button>
      <button
        type="button"
        aria-label="Compact density"
        onClick={() => setDensity("compact")}
        className={cn(
          "flex size-8 items-center justify-center rounded-sm transition-colors",
          density === "compact"
            ? "bg-primary/15 text-primary"
            : "text-muted-foreground hover:text-foreground",
        )}
      >
        <Rows4 className="size-4" />
      </button>
    </div>
  );
}

interface BulkActionBarProps {
  count: number;
  onPause: () => void;
  onResume: () => void;
  onDelete: () => void;
  onClear: () => void;
}

function BulkActionBar({
  count,
  onPause,
  onResume,
  onDelete,
  onClear,
}: BulkActionBarProps) {
  const { armed, arm, disarm, confirmAndDisarm } = useArmedConfirm({ timeoutMs: 4000 });

  return (
    <motion.div
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      className="flex items-center gap-3 rounded-lg border border-primary/30 bg-primary/10 px-4 py-3 text-sm"
    >
      <span className="font-medium">{count} selected</span>
      <span className="ml-auto flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={onPause}>
          <Pause className="size-4" />
          Pause
        </Button>
        <Button variant="outline" size="sm" onClick={onResume}>
          <Play className="size-4" />
          Resume
        </Button>
        {armed ? (
          <span
            role="group"
            aria-label="Confirm bulk delete"
            className="inline-flex items-center gap-1 rounded-md border border-destructive/40 bg-destructive/15 px-2 py-1 text-xs font-medium text-destructive"
          >
            <span>Delete {count}?</span>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-destructive hover:bg-destructive/15 hover:text-destructive"
              onClick={() => confirmAndDisarm(onDelete)}
            >
              <Check className="size-3.5" />
              Yes
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-muted-foreground hover:text-foreground"
              onClick={disarm}
            >
              <X className="size-3.5" />
              No
            </Button>
          </span>
        ) : (
          <Button variant="destructive" size="sm" onClick={arm}>
            <Trash2 className="size-4" />
            Delete
          </Button>
        )}
        <Button variant="ghost" size="sm" onClick={onClear}>
          Clear
        </Button>
      </span>
    </motion.div>
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

interface TrackerMatch {
  tracker_name: string;
  display_name: string;
  qualities?: string[];
  default_quality?: string;
  supports_episode_filter: boolean;
  requires_credentials: boolean;
  uses_cloudflare: boolean;
}

interface AddTopicCardProps {
  onClose: () => void;
  onCreated: () => void;
}

function AddTopicCard({ onClose, onCreated }: AddTopicCardProps) {
  const [url, setUrl] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [quality, setQuality] = useState<string>("");
  const [startSeason, setStartSeason] = useState<string>("");
  const [startEpisode, setStartEpisode] = useState<string>("");
  const [error, setError] = useState<string | null>(null);

  // Debounce the URL → /trackers/match lookup so we don't hammer the
  // backend on every keystroke. 350 ms is the conventional sweet spot
  // between responsive (under 500 ms) and not-spammy.
  const debouncedUrl = useDebouncedValue(url, 350);
  const trackerMatchQuery = useQuery({
    queryKey: QK.trackerMatch(debouncedUrl),
    queryFn: () =>
      api.get<TrackerMatch>(`/trackers/match?url=${encodeURIComponent(debouncedUrl)}`),
    enabled: debouncedUrl.length >= 8,
    staleTime: 60_000,
    retry: false,
  });
  const match = trackerMatchQuery.data ?? null;
  const matchError = trackerMatchQuery.isError
    ? "No tracker plugin matches this URL."
    : null;

  // Auto-populate `quality` once a default arrives from a fresh match,
  // but never overwrite a value the user already picked.
  useEffect(() => {
    if (match?.default_quality && !quality) {
      setQuality(match.default_quality);
    }
  }, [match, quality]);

  const create = useMutation({
    mutationFn: () =>
      api.post<Topic>("/topics", {
        url,
        display_name: displayName || undefined,
        quality: quality || undefined,
        start_season: startSeason ? parseInt(startSeason, 10) : undefined,
        start_episode: startEpisode ? parseInt(startEpisode, 10) : undefined,
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
            {match && (
              <p className="text-xs text-success">
                ✓ Detected: <span className="font-medium">{match.display_name}</span>
              </p>
            )}
            {matchError && (
              <p className="text-xs text-muted-foreground">{matchError}</p>
            )}
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

          {match?.qualities && match.qualities.length > 0 && (
            <div className="space-y-1.5">
              <Label htmlFor="quality">Quality</Label>
              <select
                id="quality"
                value={quality}
                onChange={(e) => setQuality(e.target.value)}
                className="flex h-10 w-full rounded-md border border-input bg-background/50 px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2"
              >
                {match.qualities.map((q) => (
                  <option key={q} value={q}>
                    {q}
                  </option>
                ))}
              </select>
              <p className="text-xs text-muted-foreground">
                Marauder will pick this quality variant when the tracker offers
                more than one.
              </p>
            </div>
          )}

          {match?.supports_episode_filter && (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1.5">
                <Label htmlFor="start-season">Start from season (optional)</Label>
                <Input
                  id="start-season"
                  type="number"
                  min={1}
                  value={startSeason}
                  onChange={(e) => setStartSeason(e.target.value)}
                  placeholder="e.g. 2"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="start-episode">Start from episode (optional)</Label>
                <Input
                  id="start-episode"
                  type="number"
                  min={1}
                  value={startEpisode}
                  onChange={(e) => setStartEpisode(e.target.value)}
                  placeholder="e.g. 5"
                />
              </div>
              <p className="text-xs text-muted-foreground sm:col-span-2">
                Episodes before this point will be skipped — only newer
                episodes are downloaded.
              </p>
            </div>
          )}

          {match?.requires_credentials && (
            <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
              This tracker requires login credentials.{" "}
              <a href="/accounts" className="font-semibold underline-offset-4 hover:underline">
                Add a {match.display_name} account →
              </a>
            </div>
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
