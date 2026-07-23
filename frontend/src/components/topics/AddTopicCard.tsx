import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { motion } from "framer-motion";

import { api, type Topic } from "@/lib/api";
import { useT } from "@/i18n";
import { cn } from "@/lib/utils";
import { Card } from "@/components/ui/card";
import { TopicForm, type TopicFormValues } from "./TopicForm";
import { TrackerSearch } from "./TrackerSearch";

interface AddTopicCardProps {
  onClose: () => void;
  onCreated: () => void;
}

const EMPTY: TopicFormValues = {
  url: "",
  displayName: "",
  quality: "",
  startSeason: "",
  startEpisode: "",
  clientId: "",
  notifierId: "",
  downloadDir: "",
  category: "",
  // Default to the historical behaviour: keep every version. Delete-data is the
  // default once replace is enabled, matching the backend column default.
  replaceOnUpdate: false,
  replaceDeleteData: true,
};

type AddMode = "url" | "search";

interface ModeTabProps {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}

function ModeTab({ active, onClick, children }: ModeTabProps) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "rounded-md px-3 py-1.5 text-xs font-medium transition-colors",
        active
          ? "bg-muted text-foreground"
          : "text-muted-foreground hover:text-foreground",
      )}
    >
      {children}
    </button>
  );
}

// Card wrapping the shared TopicForm for creating a new topic via
// POST /topics. Owns the create mutation and the URL/search mode toggle
// (issue #129); the form owns the field state. A picked search result
// remounts TopicForm via key with the URL prefilled, so the existing
// debounced match/preview flow fires as if the URL was pasted.
export function AddTopicCard({ onClose, onCreated }: AddTopicCardProps) {
  const t = useT();
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<AddMode>("url");
  const [prefillUrl, setPrefillUrl] = useState("");

  const create = useMutation({
    mutationFn: (v: TopicFormValues) =>
      api.post<Topic>("/topics", {
        url: v.url,
        display_name: v.displayName || undefined,
        quality: v.quality || undefined,
        start_season: v.startSeason ? parseInt(v.startSeason, 10) : undefined,
        start_episode: v.startEpisode ? parseInt(v.startEpisode, 10) : undefined,
        client_id: v.clientId || undefined,
        notifier_id: v.notifierId || undefined,
        download_dir: v.downloadDir || undefined,
        category: v.category || undefined,
        replace_on_update: v.replaceOnUpdate,
        replace_delete_data: v.replaceDeleteData,
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
        <div className="flex gap-1 px-6 pt-4">
          <ModeTab active={mode === "url"} onClick={() => setMode("url")}>
            {t("topics.search.byUrl")}
          </ModeTab>
          <ModeTab active={mode === "search"} onClick={() => setMode("search")}>
            {t("topics.search.tab")}
          </ModeTab>
        </div>
        {mode === "search" ? (
          <div className="p-6 pt-4">
            <TrackerSearch
              onSelect={(url) => {
                setPrefillUrl(url);
                setMode("url");
              }}
            />
          </div>
        ) : (
          <TopicForm
            key={prefillUrl} /* remount when a search result is picked */
            mode="add"
            heading="Add a new topic"
            submitLabel="Add topic"
            initial={{ ...EMPTY, url: prefillUrl }}
            isPending={create.isPending}
            error={error}
            onClose={onClose}
            onSubmit={(v) => {
              setError(null);
              create.mutate(v);
            }}
          />
        )}
      </Card>
    </motion.div>
  );
}
