import { type SeasonInfo } from "@/lib/api";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { cn } from "@/lib/utils";

// Native <select> styling, shared across the topic form so every dropdown
// matches the rest of the form chrome.
export const SELECT_CLASS =
  "flex h-10 w-full rounded-md border border-input bg-background/50 px-3 py-2 text-sm ring-offset-background focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2";

interface SeasonEpisodePickerProps {
  catalogReady: boolean;
  seasons: SeasonInfo[];
  selectedSeasonEpisodes: number[];
  startSeason: string;
  startEpisode: string;
  onSeasonChange: (value: string) => void;
  onEpisodeChange: (value: string) => void;
}

// Renders the season/episode constraint. When the tracker's released
// catalog is available we show dependent dropdowns (season → that season's
// episodes); otherwise we fall back to free-text number inputs so
// non-catalog trackers keep working unchanged.
export function SeasonEpisodePicker({
  catalogReady,
  seasons,
  selectedSeasonEpisodes,
  startSeason,
  startEpisode,
  onSeasonChange,
  onEpisodeChange,
}: SeasonEpisodePickerProps) {
  if (!catalogReady) {
    return (
      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="start-season">Start from season (optional)</Label>
          <Input
            id="start-season"
            type="number"
            min={1}
            value={startSeason}
            onChange={(e) => onSeasonChange(e.target.value)}
            placeholder="e.g. 2"
          />
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="start-episode">Start from episode (optional)</Label>
          <Input
            id="start-episode"
            type="number"
            min={1}
            value={startEpisode}
            onChange={(e) => onEpisodeChange(e.target.value)}
            placeholder="e.g. 5"
          />
        </div>
        <p className="text-xs text-muted-foreground sm:col-span-2">
          Episodes before this point will be skipped — only newer episodes are
          downloaded.
        </p>
      </div>
    );
  }

  const firstSeason = seasons[0].number;
  const lastSeason = seasons[seasons.length - 1].number;

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      <p className="text-xs text-muted-foreground sm:col-span-2">
        {seasons.length} seasons available · S{firstSeason}–S{lastSeason}
      </p>
      <div className="space-y-1.5">
        <Label htmlFor="start-season">Start from season (optional)</Label>
        <select
          id="start-season"
          value={startSeason}
          onChange={(e) => onSeasonChange(e.target.value)}
          className={SELECT_CLASS}
        >
          <option value="">From the start</option>
          {seasons.map((s) => (
            <option key={s.number} value={String(s.number)}>
              Season {s.number}
            </option>
          ))}
        </select>
      </div>
      <div className="space-y-1.5">
        <Label htmlFor="start-episode">Start from episode (optional)</Label>
        <select
          id="start-episode"
          value={startEpisode}
          disabled={!startSeason}
          onChange={(e) => onEpisodeChange(e.target.value)}
          className={cn(SELECT_CLASS, "disabled:cursor-not-allowed disabled:opacity-50")}
        >
          <option value="">From the start</option>
          {selectedSeasonEpisodes.map((ep) => (
            <option key={ep} value={String(ep)}>
              Episode {ep}
            </option>
          ))}
        </select>
      </div>
      <p className="text-xs text-muted-foreground sm:col-span-2">
        Episodes before this point will be skipped — only newer episodes are
        downloaded.
      </p>
    </div>
  );
}
