import type { ReactNode } from "react";
import { useEventStream } from "@/lib/hooks/useEventStream";

// EventStreamProvider runs the single app-wide SSE connection for as long as
// it's mounted (inside the authenticated layout). It renders its children
// unchanged — it's a lifecycle host, not a context provider.
export function EventStreamProvider({ children }: { children: ReactNode }) {
  useEventStream();
  return <>{children}</>;
}
