import { motion } from "framer-motion";
import { Pencil, RefreshCw, RotateCcw } from "lucide-react";

import type { Topic } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { cn, formatRelative } from "@/lib/utils";
import { useT } from "@/i18n";
import { DeleteConfirm } from "@/components/shared/DeleteConfirm";
import { PosterImage } from "@/components/topics/PosterImage";
import { TopicUrl } from "@/components/topics/TopicUrl";
import { TopicError } from "@/components/topics/TopicError";
import { DeliveryStatus } from "@/components/topics/DeliveryStatus";
import { TopicCheckStatus } from "@/components/topics/TopicCheckStatus";
import { TopicHistoryDisclosure } from "@/components/topics/TopicHistoryDisclosure";
import { ClientBadge, type ClientRef } from "@/components/topics/ClientBadge";
import { NotifierBadge, type NotifierRef } from "@/components/topics/NotifierBadge";
import { SonarrBadge } from "@/components/topics/SonarrBadge";
import { StatusIndicator } from "@/components/topics/StatusIndicator";

export interface TopicRowLookups {
  clientById: Map<string, ClientRef>;
  defaultClient: ClientRef | null;
  notifierById: Map<string, NotifierRef>;
}

export interface TopicRowActions {
  onToggleSelect: () => void;
  onEdit: () => void;
  onRecheck: () => void;
  onReset: () => void;
  onDelete: () => void;
}

export interface TopicRowProps {
  topic: Topic;
  compact: boolean;
  selected: boolean;
  deletePending: boolean;
  lookups: TopicRowLookups;
  actions: TopicRowActions;
}

export function TopicRow({
  topic,
  compact,
  selected,
  deletePending,
  lookups,
  actions,
}: TopicRowProps) {
  const t = useT();
  return (
    <motion.div
      initial={{ opacity: 0 }}
      animate={{ opacity: 1 }}
      className={cn(
        "group flex items-center gap-4 hover:bg-accent/5",
        compact ? "p-2" : "p-4",
        selected && "bg-primary/5",
      )}
    >
      <input
        type="checkbox"
        checked={selected}
        onChange={() => actions.onToggleSelect()}
        className="size-4 cursor-pointer"
        aria-label="Select topic"
      />
      <StatusIndicator status={topic.Status} />
      {!compact && (
        <PosterImage
          src={topic.ImageURL}
          alt={topic.DisplayName}
          ghost
          className="h-12 w-9 shrink-0 rounded object-cover"
        />
      )}
      <div className="min-w-0 flex-1">
        <div className="flex items-start gap-2">
          <span className="min-w-0 break-words font-medium">
            {topic.DisplayName}
          </span>
        </div>
        {!compact && <TopicUrl url={topic.URL} />}
        <TopicError topic={topic} />
        {!compact && <DeliveryStatus topicId={topic.ID} />}
        {/* Not gated by compact, unlike its siblings above: it's the only
            feedback a "Check now" click produces (the tick that runs it is
            up to a minute away), so compact-density users need it too. */}
        <TopicCheckStatus topicId={topic.ID} />
        {!compact && <TopicHistoryDisclosure topicId={topic.ID} />}
        <div className="mt-2 flex flex-wrap items-center gap-2">
          <Badge variant="default" className="shrink-0 font-mono">
            {topic.TrackerName}
          </Badge>
          <SonarrBadge topic={topic} />
          <ClientBadge
            topic={topic}
            clientById={lookups.clientById}
            defaultClient={lookups.defaultClient}
          />
          <NotifierBadge topic={topic} notifierById={lookups.notifierById} />
        </div>
      </div>
      {!compact && (
        <div className="hidden lg:block text-right">
          <div className="text-xs text-muted-foreground">checked</div>
          <div className="text-sm">
            {formatRelative(topic.LastCheckedAt)}
          </div>
        </div>
      )}
      {!compact && (
        <div className="hidden xl:block text-right">
          <div className="text-xs text-muted-foreground">updated</div>
          <div className="text-sm">
            {formatRelative(topic.LastUpdatedAt)}
          </div>
        </div>
      )}
      <div className="flex items-center gap-1 opacity-0 group-hover:opacity-100">
        <Button
          variant="ghost"
          size="icon"
          aria-label="Edit topic"
          onClick={() => actions.onEdit()}
        >
          <Pencil className="size-4" />
        </Button>
        {topic.Status !== "paused" && (
          <Button
            variant="ghost"
            size="icon"
            aria-label={t("topics.recheck")}
            title={t("topics.recheck")}
            onClick={actions.onRecheck}
          >
            <RefreshCw className="size-4" />
          </Button>
        )}
        <Button
          variant="ghost"
          size="icon"
          aria-label="Reset topic"
          onClick={actions.onReset}
        >
          <RotateCcw className="size-4" />
        </Button>
        <DeleteConfirm
          onConfirm={() => actions.onDelete()}
          isPending={deletePending}
          label="Delete topic"
        />
      </div>
    </motion.div>
  );
}
