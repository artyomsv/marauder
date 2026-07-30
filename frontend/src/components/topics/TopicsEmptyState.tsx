import { Plus } from "lucide-react";

import { Button } from "@/components/ui/button";

export function TopicsEmptyState({ onAdd }: { onAdd: () => void }) {
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
