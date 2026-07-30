import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { AlertTriangle, RotateCcw } from "lucide-react";

import { api, type Topic } from "@/lib/api";
import { mapWithConcurrency } from "@/lib/concurrency";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useT } from "@/i18n";

// A reset is the heaviest thing one click can trigger: per topic it removes
// torrents from the user's client, may delete their data, and arms an
// immediate re-check. Bulk-resetting a large selection unbounded would fire
// all of that at the client at once, so the fan-out is capped.
const RESET_CONCURRENCY = 4;

interface ResetTopicCardProps {
  // One entry for a row reset, many for a bulk reset.
  topics: Topic[];
  onClose: () => void;
  // Called once the reset finishes so the page can invalidate its queries and
  // clear its selection. The card deliberately stays open afterwards to show
  // the result — this app has no toast layer, so a client failure would
  // otherwise disappear unseen.
  onDone: () => void;
}

interface ResetOutcome {
  removed: number;
  warnings: string[];
  // Topics whose request failed outright, so nothing was reset for them. A 200
  // carrying warnings is not counted here: client removal is fail-open, and the
  // topic was still queued for a fresh check.
  failed: number;
}

export function ResetTopicCard({ topics, onClose, onDone }: ResetTopicCardProps) {
  const t = useT();
  const [deleteData, setDeleteData] = useState(false);
  const [outcome, setOutcome] = useState<ResetOutcome | null>(null);

  const reset = useMutation({
    mutationFn: async (): Promise<ResetOutcome> => {
      // Each topic is caught individually: one unreachable client must not
      // discard the results of every other topic in a bulk reset.
      const results = await mapWithConcurrency(topics, RESET_CONCURRENCY, async (topic) => {
        try {
          const res = await api.resetTopic(topic.ID, deleteData);
          return {
            removed: res.removed,
            warnings: res.warnings.map((w) => `${topic.DisplayName}: ${w}`),
            failed: 0,
          };
        } catch (err) {
          const message = err instanceof Error ? err.message : "reset failed";
          return { removed: 0, warnings: [`${topic.DisplayName}: ${message}`], failed: 1 };
        }
      });
      return {
        removed: results.reduce((sum, r) => sum + r.removed, 0),
        warnings: results.flatMap((r) => r.warnings),
        failed: results.reduce((sum, r) => sum + r.failed, 0),
      };
    },
    onSuccess: (res) => {
      setOutcome(res);
      onDone();
    },
  });

  const bulk = topics.length > 1;
  const title = bulk
    ? t("topics.reset.titleBulk", { count: topics.length })
    : t("topics.reset.title", { name: topics[0]?.DisplayName ?? "" });

  return (
    <motion.div
      initial={{ opacity: 0, y: -8, height: 0 }}
      animate={{ opacity: 1, y: 0, height: "auto" }}
      exit={{ opacity: 0, y: -8, height: 0 }}
      transition={{ duration: 0.2 }}
    >
      <Card className="overflow-hidden p-4">
        <div className="flex items-center gap-2 font-medium">
          <RotateCcw className="size-4 text-primary" />
          {title}
        </div>

        {outcome ? (
          <ResetResult outcome={outcome} total={topics.length} onClose={onClose} />
        ) : (
          <>
            <p className="mt-2 text-sm text-muted-foreground">{t("topics.reset.body")}</p>
            <label className="mt-3 flex items-center gap-2 text-sm">
              <input
                type="checkbox"
                className="size-4 cursor-pointer"
                checked={deleteData}
                onChange={(e) => setDeleteData(e.target.checked)}
              />
              {t("topics.reset.deleteData")}
            </label>
            <div className="mt-4 flex items-center gap-2">
              <Button
                variant="destructive"
                size="sm"
                disabled={reset.isPending}
                onClick={() => reset.mutate()}
              >
                {reset.isPending ? t("topics.reset.pending") : t("topics.reset.confirm")}
              </Button>
              <Button variant="ghost" size="sm" onClick={onClose} disabled={reset.isPending}>
                {t("topics.reset.cancel")}
              </Button>
            </div>
          </>
        )}
      </Card>
    </motion.div>
  );
}

interface ResetResultProps {
  outcome: ResetOutcome;
  // How many topics the reset was attempted on, so the result can tell
  // "everything failed" apart from "some of it failed".
  total: number;
  onClose: () => void;
}

function ResetResult({ outcome, total, onClose }: ResetResultProps) {
  const t = useT();
  // Nothing was reset, so "Queued for a fresh check" would be a flat
  // contradiction of the warnings printed directly beneath it.
  const everythingFailed = outcome.failed === total;
  return (
    <>
      {!everythingFailed && (
        <p className="mt-2 text-sm">{t("topics.reset.done", { count: outcome.removed })}</p>
      )}
      {outcome.warnings.length > 0 && (
        <div className="mt-3 rounded-md border border-warning/40 bg-warning/10 p-3 text-sm">
          <div className="flex items-center gap-2 font-medium">
            <AlertTriangle className="size-4" />
            {t("topics.reset.warnings")}
          </div>
          <ul className="mt-2 list-disc space-y-1 pl-5 text-muted-foreground">
            {/* Index-qualified: two clients of the same plugin type failing
                identically under one topic produce byte-identical strings. */}
            {outcome.warnings.map((w, i) => (
              <li key={`${i}-${w}`}>{w}</li>
            ))}
          </ul>
        </div>
      )}
      <div className="mt-4">
        <Button variant="outline" size="sm" onClick={onClose}>
          {t("topics.reset.close")}
        </Button>
      </div>
    </>
  );
}
