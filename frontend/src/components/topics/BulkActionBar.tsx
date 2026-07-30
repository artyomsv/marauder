import { motion } from "framer-motion";
import { Pause, Play, RotateCcw, Trash2, Check, X } from "lucide-react";

import { Button } from "@/components/ui/button";
import { useArmedConfirm } from "@/lib/hooks/useArmedConfirm";
import { useT } from "@/i18n";

export interface BulkActionBarProps {
  count: number;
  onPause: () => void;
  onResume: () => void;
  onReset: () => void;
  onDelete: () => void;
  onClear: () => void;
}

export function BulkActionBar({
  count,
  onPause,
  onResume,
  onReset,
  onDelete,
  onClear,
}: BulkActionBarProps) {
  const t = useT();
  const { armed, arm, disarm, confirmAndDisarm } = useArmedConfirm({ timeoutMs: 4000 });

  return (
    <motion.div
      initial={{ opacity: 0, y: -8 }}
      animate={{ opacity: 1, y: 0 }}
      className="flex items-center gap-3 rounded-lg border border-primary/30 bg-primary/10 px-4 py-3 text-sm"
    >
      <span className="font-medium">{t("topics.bulk.selected", { count })}</span>
      <span className="ml-auto flex items-center gap-2">
        <Button variant="outline" size="sm" onClick={onPause}>
          <Pause className="size-4" />
          {t("topics.bulk.pause")}
        </Button>
        <Button variant="outline" size="sm" onClick={onResume}>
          <Play className="size-4" />
          {t("topics.bulk.resume")}
        </Button>
        <Button variant="outline" size="sm" onClick={onReset}>
          <RotateCcw className="size-4" />
          {t("topics.bulk.reset")}
        </Button>
        {armed ? (
          <span
            role="group"
            aria-label="Confirm bulk delete"
            className="inline-flex items-center gap-1 rounded-md border border-destructive/40 bg-destructive/15 px-2 py-1 text-xs font-medium text-destructive"
          >
            <span>{t("topics.bulk.confirmDelete", { count })}</span>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-destructive hover:bg-destructive/15 hover:text-destructive"
              onClick={() => confirmAndDisarm(onDelete)}
            >
              <Check className="size-3.5" />
              {t("topics.bulk.yes")}
            </Button>
            <Button
              variant="ghost"
              size="sm"
              className="h-7 gap-1 px-2 text-muted-foreground hover:text-foreground"
              onClick={disarm}
            >
              <X className="size-3.5" />
              {t("topics.bulk.no")}
            </Button>
          </span>
        ) : (
          <Button variant="destructive" size="sm" onClick={arm}>
            <Trash2 className="size-4" />
            {t("topics.bulk.delete")}
          </Button>
        )}
        <Button variant="ghost" size="sm" onClick={onClear}>
          {t("topics.bulk.clear")}
        </Button>
      </span>
    </motion.div>
  );
}
