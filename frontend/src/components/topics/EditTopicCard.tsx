import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { motion } from "framer-motion";

import { api, type Topic } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { TopicForm, type TopicFormValues } from "./TopicForm";

interface EditTopicCardProps {
  topic: Topic;
  onClose: () => void;
  onSaved: () => void;
}

// Build the form's initial values from an existing topic. The capability
// fields live in the (PascalCase-serialized) topic's lowercase Extra blob;
// client_id / download_dir / category are top-level PascalCase fields.
function initialFrom(topic: Topic): TopicFormValues {
  const extra = topic.Extra ?? {};
  const season = extra.start_season;
  const episode = extra.start_episode;
  return {
    url: topic.URL,
    displayName: topic.DisplayName ?? "",
    quality: extra.quality ?? "",
    startSeason: season != null ? String(season) : "",
    startEpisode: episode != null ? String(episode) : "",
    clientId: topic.ClientID ?? "",
    downloadDir: topic.DownloadDir ?? "",
    category: topic.Category ?? "",
  };
}

// Card wrapping the shared TopicForm for editing an existing topic via
// PUT /topics/{id}. URL/tracker are immutable (shown read-only by the
// form). Owns the update mutation; the form owns the field state.
export function EditTopicCard({ topic, onClose, onSaved }: EditTopicCardProps) {
  const [error, setError] = useState<string | null>(null);

  const update = useMutation({
    mutationFn: (v: TopicFormValues) =>
      api.updateTopic(topic.ID, {
        display_name: v.displayName,
        client_id: v.clientId || null,
        // Backend Update overwrites DownloadDir/Category as plain strings,
        // so send the raw values — "" unambiguously clears the column,
        // matching the always-overwrite semantics.
        download_dir: v.downloadDir,
        category: v.category,
        quality: v.quality || undefined,
        start_season: v.startSeason ? parseInt(v.startSeason, 10) : undefined,
        start_episode: v.startEpisode ? parseInt(v.startEpisode, 10) : undefined,
      }),
    onSuccess: () => onSaved(),
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
        <TopicForm
          mode="edit"
          heading="Edit topic"
          submitLabel="Save changes"
          initial={initialFrom(topic)}
          isPending={update.isPending}
          error={error}
          onClose={onClose}
          onSubmit={(v) => {
            setError(null);
            update.mutate(v);
          }}
        />
      </Card>
    </motion.div>
  );
}
