import { useId, useState } from "react";

import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { SELECT_CLASS } from "./SeasonEpisodePicker";

interface CategoryFieldProps {
  value: string;
  onChange: (value: string) => void;
  // Existing client categories to offer (empty when the client has none or
  // doesn't support listing them — the field then behaves as plain free-text).
  suggestions: string[];
}

// Sentinel option value for "I want to type my own category".
const CUSTOM = "__custom__";

// Category picker for the topic form. When the selected client exposes its
// existing categories (qBittorrent), they are offered in a native <select>
// (an always-visible dropdown, consistent with the form's other pickers) with
// a "Custom category…" option that reveals a free-text box. Category is a path
// segment in Marauder, so a brand-new value is always allowed — the dropdown
// only makes the common case obvious, it never constrains the input. When the
// client exposes no categories, the field is a plain free-text input.
export function CategoryField({ value, onChange, suggestions }: CategoryFieldProps) {
  const selectId = useId();
  const inputId = useId();
  const hasSuggestions = suggestions.length > 0;

  // Whether the user explicitly chose to type a custom value. We also fall into
  // the custom view when the current value isn't one of the suggestions (e.g.
  // editing a topic whose stored category isn't a known client category).
  const [custom, setCustom] = useState(false);
  const valueInList = suggestions.includes(value);
  const showCustomInput = custom || (!!value && !valueInList);

  const helpText = hasSuggestions
    ? "Pick an existing client category, or choose Custom to enter your own."
    : "Nested under the client's base download folder.";

  if (!hasSuggestions) {
    return (
      <div className="space-y-1.5">
        <Label htmlFor={inputId}>Category (optional)</Label>
        <Input
          id={inputId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="tv"
        />
        <p className="text-xs text-muted-foreground">{helpText}</p>
      </div>
    );
  }

  const handleSelect = (e: React.ChangeEvent<HTMLSelectElement>) => {
    const picked = e.target.value;
    if (picked === CUSTOM) {
      setCustom(true);
      onChange("");
      return;
    }
    // A real suggestion (or the "No category" blank) — leave custom mode.
    setCustom(false);
    onChange(picked);
  };

  return (
    <div className="space-y-1.5">
      <Label htmlFor={selectId}>Category (optional)</Label>
      <select
        id={selectId}
        value={showCustomInput ? CUSTOM : value}
        onChange={handleSelect}
        className={SELECT_CLASS}
      >
        <option value="">No category</option>
        {suggestions.map((c) => (
          <option key={c} value={c}>
            {c}
          </option>
        ))}
        <option value={CUSTOM}>✎ Custom category…</option>
      </select>
      {showCustomInput && (
        <Input
          id={inputId}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder="e.g. tv/anime"
          // Focus when the user just chose Custom, but not when an existing
          // out-of-list value pre-opens this view on an edit form.
          autoFocus={custom}
        />
      )}
      <p className="text-xs text-muted-foreground">{helpText}</p>
    </div>
  );
}
