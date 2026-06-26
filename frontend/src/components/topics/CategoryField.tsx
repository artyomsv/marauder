import { useId } from "react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";

interface CategoryFieldProps {
  value: string;
  onChange: (value: string) => void;
  // Existing client categories to suggest (empty when the client has none or
  // doesn't support listing them — the field then behaves as plain free-text).
  suggestions: string[];
}

// Category picker for the topic form. When the selected client exposes its
// existing categories (qBittorrent), they are offered as a native datalist
// combobox: the user can pick one or type a brand-new value. Category is a
// path segment in Marauder, so free-text always stays valid — the datalist
// never constrains the input.
export function CategoryField({ value, onChange, suggestions }: CategoryFieldProps) {
  const hasSuggestions = suggestions.length > 0;
  // Unique ids so the field stays self-contained even if two topic forms ever
  // co-mount (the datalist/label associations can't clash).
  const inputId = useId();
  const listId = useId();
  return (
    <div className="space-y-1.5">
      <Label htmlFor={inputId}>Category (optional)</Label>
      <Input
        id={inputId}
        list={hasSuggestions ? listId : undefined}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder="tv"
      />
      {hasSuggestions && (
        <datalist id={listId}>
          {suggestions.map((c) => (
            <option key={c} value={c} />
          ))}
        </datalist>
      )}
      <p className="text-xs text-muted-foreground">
        {hasSuggestions
          ? "Pick an existing client category or type a new one. Nested under the client's base download folder."
          : "Nested under the client's base download folder."}
      </p>
    </div>
  );
}
