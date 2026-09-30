import { useSystemInfo } from "@/lib/hooks/useSystemInfo";

interface ClientRef {
  id: string;
  display_name: string;
  client_name: string;
}

// Name of the receiving client when it cannot pause or select files, so the
// update-policy settings can say they will not work there (issue #205).
// null when the client supports them or is not known.
export function useUnsupportedClient(clients: ClientRef[], effectiveClientId: string): string | null {
  const info = useSystemInfo().data;
  const client = clients.find((c) => c.id === effectiveClientId);
  const plugin = info?.clients?.find((p) => p.name === client?.client_name);
  return client && plugin && !plugin.supports_file_selection ? client.display_name : null;
}
