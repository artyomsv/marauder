import { useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2 } from "lucide-react";

import { api, type CredentialView, type SeasonInfo } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { QK } from "@/lib/queryKeys";
import { useDebouncedValue } from "@/lib/hooks/useDebouncedValue";
import { SeasonEpisodePicker, SELECT_CLASS } from "./SeasonEpisodePicker";
import { PosterImage } from "./PosterImage";

// Shape of GET /trackers/match. Drives which optional sections the form
// renders (quality, season/episode filter, credentials hint).
export interface TrackerMatch {
  tracker_name: string;
  display_name: string;
  qualities?: string[];
  default_quality?: string;
  supports_episode_filter: boolean;
  supports_season_catalog: boolean;
  requires_credentials: boolean;
  uses_cloudflare: boolean;
}

// Minimal shape of a torrent client for the picker. The full ClientView
// lives in Clients.tsx; the form only needs id + display_name.
interface ClientOption {
  id: string;
  display_name: string;
}

// The mutable fields the user edits. Grouped into one object so the form
// stays under the 8-useState component limit.
export interface TopicFormValues {
  url: string;
  displayName: string;
  quality: string;
  startSeason: string;
  startEpisode: string;
  clientId: string;
  downloadDir: string;
  category: string;
}

interface TopicFormProps {
  mode: "add" | "edit";
  initial: TopicFormValues;
  submitLabel: string;
  heading: string;
  isPending: boolean;
  error: string | null;
  onClose: () => void;
  onSubmit: (values: TopicFormValues) => void;
}

// Shared add/edit form for a topic. Owns the tracker-match / season-catalog
// / clients queries plus the editable field state, and hands the assembled
// values back to the caller's onSubmit. The caller owns the mutation so it
// can target POST /topics or PUT /topics/{id} as appropriate.
export function TopicForm({
  mode,
  initial,
  submitLabel,
  heading,
  isPending,
  error,
  onClose,
  onSubmit,
}: TopicFormProps) {
  const isEdit = mode === "edit";
  const [url] = useState(initial.url);
  const [displayName, setDisplayName] = useState(initial.displayName);
  const [quality, setQuality] = useState(initial.quality);
  const [startSeason, setStartSeason] = useState(initial.startSeason);
  const [startEpisode, setStartEpisode] = useState(initial.startEpisode);
  // URL is editable only when adding; the edit form pins it to the topic's
  // current URL. We track free-text URL separately for the add flow.
  const [addUrl, setAddUrl] = useState(initial.url);

  const effectiveUrl = isEdit ? url : addUrl;

  const clientsQuery = useQuery({
    queryKey: QK.clients,
    queryFn: () => api.get<{ clients: ClientOption[] | null }>("/clients"),
    staleTime: 60_000,
  });
  const clients = clientsQuery.data?.clients ?? [];

  // In edit mode the URL never changes, so the debounce is a no-op pass
  // through. In add mode it throttles the /trackers/match lookup.
  const debouncedUrl = useDebouncedValue(effectiveUrl, 350);
  const trackerMatchQuery = useQuery({
    queryKey: QK.trackerMatch(debouncedUrl),
    queryFn: () =>
      api.get<TrackerMatch>(`/trackers/match?url=${encodeURIComponent(debouncedUrl)}`),
    enabled: debouncedUrl.length >= 8,
    staleTime: 60_000,
    retry: false,
  });
  const match = trackerMatchQuery.data ?? null;

  // Resolve a real title + poster image for the preview card. Only fired once
  // a tracker actually claimed the URL (match present), to avoid a wasted
  // page fetch on every keystroke of an unrecognised URL.
  const previewQuery = useQuery({
    queryKey: QK.trackerPreview(debouncedUrl),
    queryFn: () => api.previewTracker(debouncedUrl),
    enabled: !isEdit && debouncedUrl.length >= 8 && !!match,
    staleTime: 60_000,
    retry: false,
  });
  const preview = previewQuery.data ?? null;

  const seasonsQuery = useQuery({
    queryKey: QK.trackerSeasons(debouncedUrl),
    queryFn: () =>
      api.get<{ seasons: SeasonInfo[] }>(
        `/trackers/seasons?url=${encodeURIComponent(debouncedUrl)}`,
      ),
    enabled: debouncedUrl.length >= 8 && !!match?.supports_season_catalog,
    staleTime: 60_000,
    retry: false,
  });
  const seasons = seasonsQuery.data?.seasons ?? [];
  const catalogReady =
    !!match?.supports_season_catalog && seasons.length > 0 && !seasonsQuery.isError;
  const selectedSeasonEpisodes =
    seasons.find((s) => String(s.number) === startSeason)?.episodes ?? [];

  const credentialsQuery = useQuery({
    queryKey: QK.credentials,
    queryFn: () => api.get<{ credentials: CredentialView[] | null }>("/credentials"),
    staleTime: 60_000,
  });
  const hasCredential = (credentialsQuery.data?.credentials ?? []).some(
    (c) => c.tracker_name === match?.tracker_name,
  );
  const matchError =
    !isEdit && trackerMatchQuery.isError ? "No tracker plugin matches this URL." : null;

  // Auto-populate quality once a default arrives from a fresh match, but
  // never overwrite a value the user already picked or one prefilled from
  // an existing topic.
  useEffect(() => {
    if (match?.default_quality && !quality) {
      setQuality(match.default_quality);
    }
  }, [match, quality]);

  // Prefill the display name from the resolved preview title, but never
  // overwrite a name the user typed or one prefilled from an existing topic.
  useEffect(() => {
    if (!isEdit && preview?.title && !displayName) {
      setDisplayName(preview.title);
    }
  }, [preview, isEdit, displayName]);

  // Reset the season/episode selection whenever the URL changes in the add
  // flow. Skipped in edit mode where the URL is fixed and the initial
  // season/episode must survive (otherwise prefill would be wiped).
  useEffect(() => {
    if (isEdit) return;
    setStartSeason("");
    setStartEpisode("");
  }, [debouncedUrl, isEdit]);

  // Delivery field (client/dir/category) state lives in a single object to
  // respect the useState ceiling.
  const [delivery, setDelivery] = useState({
    clientId: initial.clientId,
    downloadDir: initial.downloadDir,
    category: initial.category,
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    onSubmit({
      url: effectiveUrl,
      displayName,
      quality,
      startSeason,
      startEpisode,
      clientId: delivery.clientId,
      downloadDir: delivery.downloadDir,
      category: delivery.category,
    });
  };

  return (
    <form onSubmit={handleSubmit} className="space-y-4 p-6">
      <h3 className="text-base font-semibold">{heading}</h3>

      <div className="space-y-1.5">
        <Label htmlFor="url">URL or magnet link</Label>
        {isEdit ? (
          <Input id="url" value={url} readOnly disabled className="font-mono text-xs" />
        ) : (
          <Input
            id="url"
            required
            value={addUrl}
            onChange={(e) => setAddUrl(e.target.value)}
            placeholder="magnet:?xt=urn:btih:... or https://tracker.example.com/.../file.torrent"
          />
        )}
        {match && !isEdit && (
          <p className="text-xs text-success">
            ✓ Detected: <span className="font-medium">{match.display_name}</span>
          </p>
        )}
        {matchError && <p className="text-xs text-muted-foreground">{matchError}</p>}
      </div>

      {!isEdit && preview && (preview.title || preview.image_url) && (
        <div className="flex items-center gap-3 rounded-md border border-border/60 bg-muted/30 p-3">
          <PosterImage
            src={preview.image_url}
            alt={preview.title || "preview"}
            className="h-16 w-12 shrink-0 rounded object-cover"
          />
          <div className="min-w-0">
            <div className="text-xs text-muted-foreground">Preview</div>
            <div className="truncate text-sm font-medium">{preview.title || "—"}</div>
          </div>
        </div>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="display">Display name (optional)</Label>
        <Input
          id="display"
          value={displayName}
          onChange={(e) => setDisplayName(e.target.value)}
          placeholder="Leave blank to auto-detect"
        />
      </div>

      {match?.qualities && match.qualities.length > 0 && (
        <div className="space-y-1.5">
          <Label htmlFor="quality">Quality</Label>
          <select
            id="quality"
            value={quality}
            onChange={(e) => setQuality(e.target.value)}
            className={SELECT_CLASS}
          >
            {match.qualities.map((q) => (
              <option key={q} value={q}>
                {q}
              </option>
            ))}
          </select>
          <p className="text-xs text-muted-foreground">
            Marauder will pick this quality variant when the tracker offers more
            than one.
          </p>
        </div>
      )}

      {match?.supports_episode_filter && (
        <SeasonEpisodePicker
          catalogReady={catalogReady}
          seasons={seasons}
          selectedSeasonEpisodes={selectedSeasonEpisodes}
          startSeason={startSeason}
          startEpisode={startEpisode}
          onSeasonChange={(v) => {
            setStartSeason(v);
            setStartEpisode("");
          }}
          onEpisodeChange={setStartEpisode}
        />
      )}

      {match?.requires_credentials && !hasCredential && (
        <div className="rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning">
          This tracker requires login credentials.{" "}
          <a
            href="/accounts"
            className="font-semibold underline-offset-4 hover:underline"
          >
            Add a {match.display_name} account →
          </a>
        </div>
      )}

      {match?.requires_credentials && hasCredential && (
        <div className="rounded-md border border-success/40 bg-success/10 px-3 py-2 text-xs text-success">
          ✓ Using your {match.display_name} account.
        </div>
      )}

      <div className="space-y-1.5">
        <Label htmlFor="client">Client (optional)</Label>
        <select
          id="client"
          value={delivery.clientId}
          onChange={(e) => setDelivery((d) => ({ ...d, clientId: e.target.value }))}
          className={SELECT_CLASS}
        >
          <option value="">Use default client</option>
          {clients.map((c) => (
            <option key={c.id} value={c.id}>
              {c.display_name}
            </option>
          ))}
        </select>
      </div>

      <div className="grid gap-4 sm:grid-cols-2">
        <div className="space-y-1.5">
          <Label htmlFor="download-dir">Download folder (optional)</Label>
          <Input
            id="download-dir"
            value={delivery.downloadDir}
            onChange={(e) =>
              setDelivery((d) => ({ ...d, downloadDir: e.target.value }))
            }
            placeholder="/downloads/tv"
          />
          <p className="text-xs text-muted-foreground">
            Full path; overrides the client folder and category below.
          </p>
        </div>
        <div className="space-y-1.5">
          <Label htmlFor="category">Category (optional)</Label>
          <Input
            id="category"
            value={delivery.category}
            onChange={(e) => setDelivery((d) => ({ ...d, category: e.target.value }))}
            placeholder="tv"
          />
          <p className="text-xs text-muted-foreground">
            Nested under the client's base download folder.
          </p>
        </div>
      </div>

      {error && (
        <div className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {error}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onClose}>
          Cancel
        </Button>
        <Button type="submit" disabled={isPending}>
          {isPending && <Loader2 className="size-4 animate-spin" />}
          {submitLabel}
        </Button>
      </div>
    </form>
  );
}
