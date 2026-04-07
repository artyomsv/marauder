import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { Shield, CheckCircle2, XCircle } from "lucide-react";

import { api } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

type AuditEntry = {
  ID: number;
  UserID: string | null;
  Actor: string;
  Action: string;
  TargetType: string;
  TargetID: string;
  Result: "success" | "failure";
  IP: string;
  UserAgent: string;
  Details: Record<string, unknown> | null;
  CreatedAt: string;
};

type AuditList = { entries: AuditEntry[] | null };

export function AuditPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["audit"],
    queryFn: () => api.get<AuditList>("/system/audit"),
    refetchInterval: 10_000,
  });
  const entries = data?.entries ?? [];

  return (
    <div className="space-y-8">
      <header>
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
        >
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-primary">
            admin
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">
            Audit log
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Security-relevant events: logins, logouts, configuration changes.
            Auto-refreshes every 10 seconds.
          </p>
        </motion.div>
      </header>

      <Card>
        {isLoading ? (
          <div className="p-12 text-center text-sm text-muted-foreground">
            Loading...
          </div>
        ) : entries.length === 0 ? (
          <div className="flex flex-col items-center gap-3 p-16 text-center">
            <div className="flex size-14 items-center justify-center rounded-full bg-primary/10 text-primary ring-1 ring-primary/20">
              <Shield className="size-6" />
            </div>
            <div className="text-lg font-medium">No audit entries yet</div>
            <div className="max-w-sm text-sm text-muted-foreground">
              As users log in, log out, and make changes, those events will
              appear here.
            </div>
          </div>
        ) : (
          <div className="divide-y divide-border/60">
            {entries.map((e) => (
              <div
                key={e.ID}
                className="flex items-center gap-4 p-4 hover:bg-accent/5"
              >
                {e.Result === "success" ? (
                  <CheckCircle2 className="size-4 text-success" />
                ) : (
                  <XCircle className="size-4 text-destructive" />
                )}
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <Badge variant="outline" className="font-mono">
                      {e.Action}
                    </Badge>
                    {e.Actor && (
                      <span className="text-sm font-medium">
                        {e.Actor}
                      </span>
                    )}
                    {e.TargetType && (
                      <span className="text-xs text-muted-foreground">
                        on {e.TargetType}{e.TargetID ? `:${e.TargetID}` : ""}
                      </span>
                    )}
                  </div>
                  {(e.IP || e.UserAgent) && (
                    <div className="mt-1 truncate font-mono text-xs text-muted-foreground">
                      {e.IP || "?"}
                      {e.UserAgent ? ` · ${e.UserAgent}` : ""}
                    </div>
                  )}
                </div>
                <div className="text-xs font-mono text-muted-foreground">
                  {new Date(e.CreatedAt).toLocaleTimeString()}
                </div>
              </div>
            ))}
          </div>
        )}
      </Card>
    </div>
  );
}
