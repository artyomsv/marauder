import { motion } from "framer-motion";
import { Check, Loader2, MoreVertical, Pencil, RefreshCw, RotateCcw, Trash2 } from "lucide-react";

import type { Topic } from "@/lib/api";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { cn, formatRelative } from "@/lib/utils";
import { useT } from "@/i18n";
import { useArmedConfirm } from "@/lib/hooks/useArmedConfirm";
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
      <TopicRowActionsMenu
        status={topic.Status}
        deletePending={deletePending}
        actions={actions}
      />
    </motion.div>
  );
}

// TopicRowActionsMenu collapses the row's actions into a single overflow menu.
//
// They used to be four hover-revealed icon buttons. Adding "Check now" made the
// cost obvious: the icons crowded the row's timestamps, only one carried a
// tooltip, and two of them — RefreshCw for Check now and RotateCcw for Reset —
// are near-identical circular arrows sitting side by side, one harmless and one
// destructive. A menu gives every action a written label and costs one click
// for actions nobody performs in bulk from a row.
//
// Delete keeps its two-click arming rather than firing straight from the menu:
// the menu makes the label unambiguous, but it does not make the action less
// destructive. Radix closes on select by default, so the arming item suppresses
// that until the user resolves it.
function TopicRowActionsMenu({
  status,
  deletePending,
  actions,
}: {
  status: Topic["Status"];
  deletePending: boolean;
  actions: TopicRowActions;
}) {
  const t = useT();
  const { armed, arm, disarm, confirmAndDisarm } = useArmedConfirm({ timeoutMs: 4000 });

  return (
    <DropdownMenu onOpenChange={(open) => !open && disarm()}>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon" aria-label={t("topics.actions.menu")}>
          <MoreVertical className="size-4" />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end">
        <DropdownMenuItem onSelect={() => actions.onEdit()}>
          <Pencil className="size-4" />
          {t("topics.actions.edit")}
        </DropdownMenuItem>
        {/* The scheduler skips paused topics, so offering a check that can
            never run would be a dead control. */}
        {status !== "paused" && (
          <DropdownMenuItem onSelect={() => actions.onRecheck()}>
            <RefreshCw className="size-4" />
            {t("topics.recheck")}
          </DropdownMenuItem>
        )}
        <DropdownMenuItem onSelect={() => actions.onReset()}>
          <RotateCcw className="size-4" />
          {t("topics.actions.reset")}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        {armed ? (
          <DropdownMenuItem
            destructive
            onSelect={() => confirmAndDisarm(() => actions.onDelete())}
          >
            {deletePending ? (
              <Loader2 className="size-4 animate-spin" />
            ) : (
              <Check className="size-4" />
            )}
            {t("topics.actions.confirmDelete")}
          </DropdownMenuItem>
        ) : (
          <DropdownMenuItem
            destructive
            // Keep the menu open so the confirm step replaces this item in
            // place, instead of the menu vanishing and the click doing nothing
            // visible.
            onSelect={(e) => {
              e.preventDefault();
              arm();
            }}
          >
            <Trash2 className="size-4" />
            {t("topics.actions.delete")}
          </DropdownMenuItem>
        )}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
