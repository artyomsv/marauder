import { useQuery } from "@tanstack/react-query";

import { api, type SystemInfo } from "@/lib/api";
import { QK } from "@/lib/queryKeys";

const FIVE_MINUTES_MS = 5 * 60_000;

/**
 * Shared accessor for the public `/system/info` endpoint.
 *
 * Used by the AppShell version chip in the sidebar header and by the
 * Settings About card. The endpoint is unauthenticated and the response
 * is small, so a 5-minute stale time keeps it cheap and consistent
 * across mounts.
 */
export function useSystemInfo() {
  return useQuery({
    queryKey: QK.systemInfo,
    queryFn: () => api.get<SystemInfo>("/system/info", { auth: false }),
    staleTime: FIVE_MINUTES_MS,
  });
}
