import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Loader2, Search as SearchIcon } from "lucide-react";

import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { useT } from "@/i18n";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";

interface SearchResultRow {
  tracker_name: string;
  tracker_display_name: string;
  title: string;
  url: string;
  size?: string;
  seeders: number;
  category?: string;
}

interface SearchResponse {
  results: SearchResultRow[];
  errors: { tracker_name: string; error: string }[];
}

interface TrackerSearchProps {
  onSelect: (url: string) => void;
}

// Cross-tracker release search (issue #129). Explicit submit only — every
// search fans out real scraping requests server-side, so no
// search-as-you-type. A picked result is handed up to AddTopicCard, which
// switches back to URL mode with the URL prefilled.
export function TrackerSearch({ onSelect }: TrackerSearchProps) {
  const t = useT();
  const [query, setQuery] = useState("");
  const [submitted, setSubmitted] = useState("");

  const search = useQuery({
    queryKey: QK.trackerSearch(submitted),
    queryFn: () =>
      api.get<SearchResponse>(`/trackers/search?q=${encodeURIComponent(submitted)}`),
    enabled: submitted.length > 0,
    staleTime: 60_000,
    retry: false,
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const q = query.trim();
    if (q) setSubmitted(q);
  };

  const results = search.data?.results ?? [];
  const trackerErrors = search.data?.errors ?? [];

  return (
    <div className="space-y-3">
      <form onSubmit={handleSubmit} className="flex gap-2">
        <Input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("topics.search.placeholder")}
          aria-label={t("topics.search.tab")}
          autoFocus
        />
        <Button type="submit" disabled={search.isFetching || !query.trim()}>
          {search.isFetching ? (
            <Loader2 className="size-4 animate-spin" />
          ) : (
            <SearchIcon className="size-4" />
          )}
          {t("topics.search.button")}
        </Button>
      </form>

      {search.isError && (
        <p className="text-xs text-destructive">{t("topics.search.failed")}</p>
      )}

      {results.length > 0 && (
        <ul className="max-h-80 divide-y divide-border/60 overflow-y-auto rounded-md border border-border/60">
          {results.map((r) => (
            <li key={`${r.tracker_name}-${r.url}`}>
              <button
                type="button"
                onClick={() => onSelect(r.url)}
                className="flex w-full items-center gap-3 px-3 py-2 text-left hover:bg-muted/50"
              >
                <span className="min-w-0 flex-1 truncate text-sm">{r.title}</span>
                <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium text-muted-foreground">
                  {r.tracker_display_name}
                </span>
                {r.size && (
                  <span className="shrink-0 text-xs text-muted-foreground">{r.size}</span>
                )}
                <span className="shrink-0 text-xs tabular-nums text-success">
                  {r.seeders >= 0 ? `↑${r.seeders}` : "—"}
                </span>
              </button>
            </li>
          ))}
        </ul>
      )}

      {search.isSuccess && results.length === 0 && (
        <p className="py-4 text-center text-sm text-muted-foreground">
          {t("topics.search.noResults")}
        </p>
      )}

      {trackerErrors.map((e) => (
        <p key={e.tracker_name} className="text-xs text-muted-foreground">
          {e.tracker_name}:{" "}
          {e.error === "search requires credentials"
            ? t("topics.search.needsAccount")
            : e.error}
        </p>
      ))}
    </div>
  );
}
