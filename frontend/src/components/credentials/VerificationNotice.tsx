import { ShieldQuestion } from "lucide-react";

import { useT } from "@/i18n";

interface VerificationNoticeProps {
  /**
   * Outcome of the login round-trip the last request performed.
   * `undefined` means no round-trip happened (nothing to report),
   * `true` means the plugin confirmed the session, and `false` means the
   * sign-in did not fail but nothing actually verified it.
   */
  verified: boolean | undefined;
}

/**
 * Warns that a credential was accepted without its session being confirmed.
 *
 * Some tracker plugins have no reliable logged-in marker and report
 * `ErrVerifyUnsupported` rather than claiming a check they did not make. Those
 * logins are usable but unproven, and collapsing them into the same green tick
 * as a verified one is what let a credential for an entirely different site sit
 * in the UI looking healthy.
 */
export function VerificationNotice({ verified }: VerificationNoticeProps) {
  const t = useT();
  if (verified !== false) return null;
  return (
    <div
      role="status"
      className="mt-2 flex items-start gap-2 rounded-md border border-warning/40 bg-warning/10 px-3 py-2 text-xs text-warning"
    >
      <ShieldQuestion className="mt-px size-4 shrink-0" />
      <span>{t("credentials.unverified")}</span>
    </div>
  );
}
