import { useQuery } from "@tanstack/react-query";

import { api } from "@/lib/api";
import { Label } from "@/components/ui/label";
import { QK } from "@/lib/queryKeys";
import { SELECT_CLASS } from "./SeasonEpisodePicker";

// Minimal shape of a configured notifier for the picker — id + label only.
export interface NotifierOption {
  id: string;
  display_name: string;
}

interface NotifierSelectProps {
  value: string;
  onChange: (value: string) => void;
}

// Owns the QK.notifiers query and renders the notifier picker <select>.
// Renders a "Use default notifiers" option plus one option per configured notifier.
export function NotifierSelect({ value, onChange }: NotifierSelectProps) {
  const notifiersQuery = useQuery({
    queryKey: QK.notifiers,
    queryFn: () => api.get<{ notifiers: NotifierOption[] | null }>("/notifiers"),
    staleTime: 60_000,
  });
  const notifiers = notifiersQuery.data?.notifiers ?? [];

  return (
    <div className="space-y-1.5">
      <Label htmlFor="notifier">Notifier (optional)</Label>
      <select
        id="notifier"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        className={SELECT_CLASS}
      >
        <option value="">Use default notifiers</option>
        {notifiers.map((n) => (
          <option key={n.id} value={n.id}>
            {n.display_name}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">
        Route this topic's release alerts to one notifier instead of all of them.
      </p>
    </div>
  );
}
