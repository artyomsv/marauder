import { useState } from "react";

import { useT } from "@/i18n";
import { TopicEventsTimeline } from "@/components/topics/TopicEventsTimeline";

export function TopicHistoryDisclosure({ topicId }: { topicId: string }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  return (
    <div className="mt-1">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="text-xs text-muted-foreground hover:text-foreground"
      >
        {open ? t("topics.history.hide") : t("topics.history.show")}
      </button>
      {open && <TopicEventsTimeline topicId={topicId} />}
    </div>
  );
}
