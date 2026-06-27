import { describe, it, expect } from "vitest";

import { ApiError, type SonarrInstance } from "@/lib/api";
import { fromInstance, toBody, instanceToBody, errText } from "./sonarrForm";

const instance: SonarrInstance = {
  id: "i1",
  name: "TV",
  enabled: true,
  sonarr_url: "http://sonarr:8989",
  api_key_set: true,
  poll_interval_sec: 600,
  allowed_trackers: ["rutracker"],
  default_client_id: "c1",
  default_category: "tv-sonarr",
  default_download_dir: "/tv",
  update_existing: true,
  last_seen_at: "2026-06-27T00:00:00Z",
  created_at: "2026-06-27T00:00:00Z",
  updated_at: "2026-06-27T00:00:00Z",
};

describe("sonarrForm helpers", () => {
  it("fromInstance copies scalars but blanks the api key", () => {
    const f = fromInstance(instance);
    expect(f.name).toBe("TV");
    expect(f.default_client_id).toBe("c1");
    expect(f.poll_interval_sec).toBe(600);
    expect(f.api_key).toBe(""); // never round-trips
  });

  it("toBody trims the name and coerces an empty client id to null", () => {
    const f = { ...fromInstance(instance), name: "  Anime  ", default_client_id: "" };
    const body = toBody(f);
    expect(body.name).toBe("Anime");
    expect(body.default_client_id).toBeNull();
  });

  it("instanceToBody preserves the stored key (blank) and normalises trackers", () => {
    const body = instanceToBody({ ...instance, allowed_trackers: undefined as never });
    expect(body.api_key).toBe("");
    expect(body.allowed_trackers).toEqual([]);
    expect(body.enabled).toBe(true);
  });

  it("errText prefers the problem detail, falling back to String()", () => {
    const apiErr = new ApiError({ title: "Bad", detail: "name is required", status: 422 });
    expect(errText(apiErr)).toBe("name is required");
    expect(errText("boom")).toBe("boom");
  });
});
