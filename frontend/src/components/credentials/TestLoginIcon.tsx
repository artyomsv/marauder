import { AlertCircle, CheckCircle2, Loader2, ShieldQuestion } from "lucide-react";

import { useT } from "@/i18n";

/**
 * Outcome of a "Test login" round-trip.
 *
 * `unverified` is the state that must never collapse into `verified`: the
 * sign-in did not fail, but the tracker plugin has no way to confirm the
 * session, so nothing actually proved the credential works.
 */
export type TestLoginPhase = "idle" | "pending" | "verified" | "unverified" | "failed";

export function TestLoginIcon({ phase }: { phase: TestLoginPhase }) {
  const t = useT();
  switch (phase) {
    case "pending":
      return <Loader2 role="img" aria-label={t("credentials.testPending")} className="size-4 animate-spin" />;
    case "verified":
      return <CheckCircle2 role="img" aria-label={t("credentials.testVerified")} className="size-4 text-success" />;
    case "unverified":
      return (
        <ShieldQuestion role="img" aria-label={t("credentials.unverifiedShort")} className="size-4 text-warning" />
      );
    case "failed":
      return <AlertCircle role="img" aria-label={t("credentials.testFailed")} className="size-4 text-destructive" />;
    default:
      return <CheckCircle2 aria-hidden className="size-4" />;
  }
}
