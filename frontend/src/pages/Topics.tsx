import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { AnimatePresence } from "framer-motion";
import { Plus, Loader2 } from "lucide-react";

import { api, type Topic } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { usePrefs } from "@/lib/prefs";
import { QK } from "@/lib/queryKeys";
import { AddTopicCard } from "@/components/topics/AddTopicCard";
import { EditTopicCard } from "@/components/topics/EditTopicCard";
import { ResetTopicCard } from "@/components/topics/ResetTopicCard";
import type { ClientRef } from "@/components/topics/ClientBadge";
import type { NotifierRef } from "@/components/topics/NotifierBadge";
import { TopicRow } from "@/components/topics/TopicRow";
import { BulkActionBar } from "@/components/topics/BulkActionBar";
import { DensityToggle } from "@/components/topics/DensityToggle";
import { TopicsEmptyState } from "@/components/topics/TopicsEmptyState";
import { useCheckStatus } from "@/lib/check-status";

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
  // Topics currently being reset. Held separately from `selected` because
  // finishing a bulk reset clears the selection, and the card must stay
  // mounted afterwards to show its result.
  const [resetting, setResetting] = useState<Topic[] | null>(null);

  const del = useMutation({
    mutationFn: (id: string) => api.del<void>(`/topics/${id}`),
    onSuccess: (_data, id) => {
      useCheckStatus.getState().clear(id);
      qc.invalidateQueries({ queryKey: QK.topics });
    },
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

  const onResetDone = () => {
    for (const topic of resetting ?? []) {
      useCheckStatus.getState().clear(topic.ID);
      qc.invalidateQueries({ queryKey: QK.topicStatus(topic.ID) });
      qc.invalidateQueries({ queryKey: QK.topicEvents(topic.ID) });
    }
    qc.invalidateQueries({ queryKey: QK.topics });
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
        {/* An empty array is truthy — topics.filter() can yield one if the
            selected topics vanished from a concurrent refetch — and would
            render a card titled "Reset " with an empty key. */}
        {resetting && resetting.length > 0 && (
          <ResetTopicCard
            key={resetting.map((t) => t.ID).join(",")}
            topics={resetting}
            onClose={() => setResetting(null)}
            onDone={onResetDone}
          />
        )}
      </AnimatePresence>

      {selected.size > 0 && (
        <BulkActionBar
          count={selected.size}
          onPause={() => bulk("pause")}
          onResume={() => bulk("resume")}
          onReset={() => setResetting(topics.filter((t) => selected.has(t.ID)))}
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
          <TopicsEmptyState onAdd={() => setShowAdd(true)} />
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
                <TopicRow
                  key={t.ID}
                  topic={t}
                  compact={compact}
                  selected={selected.has(t.ID)}
                  deletePending={del.isPending && del.variables === t.ID}
                  lookups={{ clientById, defaultClient, notifierById }}
                  actions={{
                    onToggleSelect: () => toggleOne(t.ID),
                    onEdit: () => setEditing(t),
                    onReset: () => setResetting([t]),
                    onDelete: () => del.mutate(t.ID),
                  }}
                />
              ))}
            </div>
          </>
        )}
      </Card>
    </div>
  );
}
