import { ApiError, type SonarrConfig, type SonarrConfigUpdate } from "@/lib/api";

/** Local editable form state for the Sonarr integration config. */
export interface SonarrForm {
  enabled: boolean;
  sonarr_url: string;
  api_key: string;
  poll_interval_sec: number;
  allowed_trackers: string[];
  default_client_id: string;
  default_category: string;
  default_download_dir: string;
  update_existing: boolean;
}

export const EMPTY_FORM: SonarrForm = {
  enabled: false,
  sonarr_url: "",
  api_key: "",
  poll_interval_sec: 900,
  allowed_trackers: [],
  default_client_id: "",
  default_category: "",
  default_download_dir: "",
  update_existing: false,
};

/** Seed the form from the server view. The API key never round-trips. */
export function fromConfig(c: SonarrConfig): SonarrForm {
  return {
    enabled: c.enabled,
    sonarr_url: c.sonarr_url,
    api_key: "", // blank means "keep stored"
    poll_interval_sec: c.poll_interval_sec || 900,
    allowed_trackers: c.allowed_trackers ?? [],
    default_client_id: c.default_client_id ?? "",
    default_category: c.default_category,
    default_download_dir: c.default_download_dir,
    update_existing: c.update_existing,
  };
}

export function toUpdate(f: SonarrForm): SonarrConfigUpdate {
  return {
    enabled: f.enabled,
    sonarr_url: f.sonarr_url.trim(),
    api_key: f.api_key,
    poll_interval_sec: f.poll_interval_sec,
    allowed_trackers: f.allowed_trackers,
    default_client_id: f.default_client_id || null,
    default_category: f.default_category.trim(),
    default_download_dir: f.default_download_dir.trim(),
    update_existing: f.update_existing,
  };
}

export function errText(e: unknown): string {
  return e instanceof ApiError ? e.problem.detail || e.problem.title : String(e);
}
