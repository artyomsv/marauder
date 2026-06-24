import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { motion, AnimatePresence } from "framer-motion";
import {
  Plus,
  Trash2,
  Loader2,
  AlertTriangle,
  Pause,
  Play,
  Pencil,
  Rows3,
  Rows4,
  Check,
  X,
  Tv,
} from "lucide-react";

import { api, type Topic } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { cn, formatRelative } from "@/lib/utils";
import { usePrefs } from "@/lib/prefs";
import { DeleteConfirm } from "@/components/shared/DeleteConfirm";
import { QK } from "@/lib/queryKeys";
import { useArmedConfirm } from "@/lib/hooks/useArmedConfirm";
import { AddTopicCard } from "@/components/topics/AddTopicCard";
import { EditTopicCard } from "@/components/topics/EditTopicCard";
import { ClientBadge, type ClientRef } from "@/components/topics/ClientBadge";
import { NotifierBadge, type NotifierRef } from "@/components/topics/NotifierBadge";
import { DeliveryStatus } from "@/components/topics/DeliveryStatus";
import { PosterImage } from "@/components/topics/PosterImage";
import { TopicUrl } from "@/components/topics/TopicUrl";

// Re-exported so existing imports (and tests) that reference AddTopicCard
// from this page module keep resolving after the extraction.
export { AddTopicCard } from "@/components/topics/AddTopicCard";

type TopicsList = { topics: Topic[] | null };
type ClientsList = { clients: ClientRef[] | null };
interface NotifiersList { notifiers: NotifierRef[] | null }

export function TopicsPage() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: QK.topics,
    queryFn: () => api.get<TopicsList>("/topics"),
  });
  const { data: clientsData } = useQuery({
    queryKey: QK.clients,
    queryFn: () => api.get<ClientsList>("/clients"),
  });
  const clients = clientsData?.clients ?? [];
  const clientById = new Map(clients.map((c) => [c.id, c]));
  const defaultClient = clients.find((c) => c.is_default) ?? null;
  const { data: notifiersData } = useQuery({
    queryKey: QK.notifiers,
    queryFn: () => api.get<NotifiersList>("/notifiers"),
    staleTime: 60_000,
  });
  const notifierById = new Map((notifiersData?.notifiers ?? []).map((n) => [n.id, n]));
  const density = usePrefs((s) => s.density);
  const setDensity = usePrefs((s) => s.setDensity);
  const [showAdd, setShowAdd] = useState(false);
  const [editing, setEditing] = useState<Topic | null>(null);
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
        {editing && (
          <EditTopicCard
            key={editing.ID}
            topic={editing}
            onClose={() => setEditing(null)}
            onSaved={() => {
              setEditing(null);
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
                  {!compact && (
                    <PosterImage
                      src={t.ImageURL}
                      alt={t.DisplayName}
                      ghost
                      className="h-12 w-9 shrink-0 rounded object-cover"
                    />
                  )}
                  <div className="min-w-0 flex-1">
                    <div className="flex items-start gap-2">
                      <span className="min-w-0 break-words font-medium">
                        {t.DisplayName}
                      </span>
                    </div>
                    {!compact && <TopicUrl url={t.URL} />}
                    {t.LastError && (
                      <div className="mt-1 flex items-center gap-1.5 text-xs text-destructive">
                        <AlertTriangle className="size-3" />
                        {t.LastError}
                      </div>
                    )}
                    {!compact && <DeliveryStatus topicId={t.ID} />}
                    <div className="mt-2 flex flex-wrap items-center gap-2">
                      <Badge variant="default" className="shrink-0 font-mono">
                        {t.TrackerName}
                      </Badge>
                      {t.Extra?.source === "sonarr" && (
                        <Badge
                          variant="secondary"
                          className="shrink-0 gap-1 font-normal"
                          title="Auto-imported by the Sonarr integration"
                        >
                          <Tv className="size-3" />
                          Sonarr
                        </Badge>
                      )}
                      <ClientBadge
                        topic={t}
                        clientById={clientById}
                        defaultClient={defaultClient}
                      />
                      <NotifierBadge topic={t} notifierById={notifierById} />
                    </div>
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
                  <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100">
                    <Button
                      variant="ghost"
                      size="icon"
                      aria-label="Edit topic"
                      onClick={() => setEditing(t)}
                    >
                      <Pencil className="size-4" />
                    </Button>
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
