import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { motion } from "framer-motion";

import { api, type Topic } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { TopicForm, type TopicFormValues } from "./TopicForm";

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
};

// Card wrapping the shared TopicForm for creating a new topic via
// POST /topics. Owns the create mutation; the form owns the field state.
export function AddTopicCard({ onClose, onCreated }: AddTopicCardProps) {
  const [error, setError] = useState<string | null>(null);

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
        <TopicForm
          mode="add"
          heading="Add a new topic"
          submitLabel="Add topic"
          initial={EMPTY}
          isPending={create.isPending}
          error={error}
          onClose={onClose}
          onSubmit={(v) => {
            setError(null);
            create.mutate(v);
          }}
        />
      </Card>
    </motion.div>
  );
}
