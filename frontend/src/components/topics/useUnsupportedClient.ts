import { useSystemInfo } from "@/lib/hooks/useSystemInfo";

interface ClientRef {
  id: string;
  display_name: string;
  client_name: string;
}

type ClientCapability = "supports_file_selection" | "supports_rename";

// Name of the receiving client when it lacks a capability, so a setting can
// say it will not work there: file selection for the update policy
// (issue #205), rename for the folder name (issue #225). null when the client
// supports it or is not known.
export function useUnsupportedClient(
  clients: ClientRef[],
  effectiveClientId: string,
  capability: ClientCapability = "supports_file_selection",
): string | null {
  const info = useSystemInfo().data;
  const client = clients.find((c) => c.id === effectiveClientId);
  const plugin = info?.clients?.find((p) => p.name === client?.client_name);
  return client && plugin && !plugin[capability] ? client.display_name : null;
}
