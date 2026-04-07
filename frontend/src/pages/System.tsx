import { useQuery } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { Activity, Cpu, Database, Pause, Play, AlertCircle } from "lucide-react";

import { api } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";

type RunSummary = {
  started_at: string;
  ended_at: string;
  checked: number;
  updated: number;
  errors: number;
};

type SystemStatus = {
  scheduler: {
    paused: boolean;
    last_run: RunSummary | null;
    history: RunSummary[];
  };
  runtime: {
    goroutines: number;
    alloc_bytes: number;
    sys_bytes: number;
    heap_objects: number;
    gc_cycles: number;
  };
  version: { version: string; commit: string; buildDate: string };
};

function formatBytes(b: number): string {
  if (b < 1024) return `${b} B`;
  if (b < 1024 * 1024) return `${(b / 1024).toFixed(1)} KB`;
  if (b < 1024 * 1024 * 1024) return `${(b / (1024 * 1024)).toFixed(1)} MB`;
  return `${(b / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

export function SystemPage() {
  const { data, isLoading } = useQuery({
    queryKey: ["system-status"],
    queryFn: () => api.get<SystemStatus>("/system/status"),
    refetchInterval: 5_000,
  });

  return (
    <div className="space-y-8">
      <header>
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
        >
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-primary">
            health
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">System</h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Live state of the scheduler, the Go runtime, and recent
            activity. Auto-refreshes every 5 seconds.
          </p>
        </motion.div>
      </header>

      {isLoading || !data ? (
        <Card>
          <div className="p-12 text-center text-sm text-muted-foreground">
            Loading...
          </div>
        </Card>
      ) : (
        <>
          <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
            <StatCard
              icon={data.scheduler.paused ? Pause : Play}
              label="Scheduler"
              value={data.scheduler.paused ? "Paused" : "Running"}
              accent={
                data.scheduler.paused
                  ? "from-warning/40 to-warning/10"
                  : "from-success/40 to-success/10"
              }
            />
            <StatCard
              icon={Cpu}
              label="Goroutines"
              value={data.runtime.goroutines.toString()}
              accent="from-primary/40 to-primary/10"
            />
            <StatCard
              icon={Database}
              label="Heap"
              value={formatBytes(data.runtime.alloc_bytes)}
              accent="from-accent/40 to-accent/10"
            />
            <StatCard
              icon={Activity}
              label="GC cycles"
              value={data.runtime.gc_cycles.toString()}
              accent="from-success/40 to-success/10"
            />
          </section>

          <section>
            <h2 className="mb-3 text-lg font-semibold tracking-tight">
              Last scheduler run
            </h2>
            <Card>
              {data.scheduler.last_run ? (
                <div className="grid gap-6 p-6 sm:grid-cols-3">
                  <Stat
                    label="checked"
                    value={data.scheduler.last_run.checked}
                  />
                  <Stat
                    label="updated"
                    value={data.scheduler.last_run.updated}
                    color="text-success"
                  />
                  <Stat
                    label="errors"
                    value={data.scheduler.last_run.errors}
                    color={
                      data.scheduler.last_run.errors > 0
                        ? "text-destructive"
                        : "text-muted-foreground"
                    }
                  />
                  <div className="sm:col-span-3 text-xs font-mono text-muted-foreground">
                    {data.scheduler.last_run.started_at} →{" "}
                    {data.scheduler.last_run.ended_at}
                  </div>
                </div>
              ) : (
                <div className="flex flex-col items-center gap-2 p-12 text-center text-sm text-muted-foreground">
                  <Activity className="size-5 opacity-50" />
                  No runs yet. The scheduler will tick on its next interval.
                </div>
              )}
            </Card>
          </section>

          <section>
            <h2 className="mb-3 text-lg font-semibold tracking-tight">
              Run history
            </h2>
            <Card>
              {data.scheduler.history.length === 0 ? (
                <div className="p-8 text-center text-sm text-muted-foreground">
                  No history yet.
                </div>
              ) : (
                <div className="divide-y divide-border/60">
                  {[...data.scheduler.history].reverse().map((r, i) => (
                    <div
                      key={i}
                      className="flex items-center gap-4 p-4 hover:bg-accent/5"
                    >
                      <div className="font-mono text-xs text-muted-foreground">
                        {new Date(r.started_at).toLocaleTimeString()}
                      </div>
                      <div className="flex-1 text-sm">
                        checked <strong>{r.checked}</strong>, updated{" "}
                        <strong className="text-success">{r.updated}</strong>,
                        errors{" "}
                        <strong
                          className={
                            r.errors > 0 ? "text-destructive" : "text-muted-foreground"
                          }
                        >
                          {r.errors}
                        </strong>
                      </div>
                      {r.errors > 0 && (
                        <Badge variant="destructive">
                          <AlertCircle className="mr-1 size-3" />
                          errors
                        </Badge>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </Card>
          </section>

          <section>
            <h2 className="mb-3 text-lg font-semibold tracking-tight">
              Build
            </h2>
            <Card>
              <div className="grid gap-4 p-6 sm:grid-cols-3">
                <KV label="version" value={data.version.version} mono />
                <KV label="commit" value={data.version.commit} mono />
                <KV label="built" value={data.version.buildDate} mono />
              </div>
            </Card>
          </section>
        </>
      )}
    </div>
  );
}

function StatCard({
  icon: Icon,
  label,
  value,
  accent,
}: {
  icon: typeof Activity;
  label: string;
  value: string;
  accent: string;
}) {
  return (
    <Card className="relative overflow-hidden">
      <div
        className={`pointer-events-none absolute -right-10 -top-10 size-40 rounded-full bg-gradient-to-br ${accent} blur-2xl`}
      />
      <div className="relative flex items-start justify-between p-6">
        <div>
          <div className="text-xs uppercase tracking-wider text-muted-foreground">
            {label}
          </div>
          <div className="mt-2 font-mono text-2xl font-semibold">{value}</div>
        </div>
        <div className="flex size-10 items-center justify-center rounded-lg border border-border/60 bg-background/40">
          <Icon className="size-5 text-foreground/80" />
        </div>
      </div>
    </Card>
  );
}

function Stat({
  label,
  value,
  color,
}: {
  label: string;
  value: number;
  color?: string;
}) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className={`mt-1 font-mono text-3xl font-semibold ${color ?? ""}`}>
        {value}
      </div>
    </div>
  );
}

function KV({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <div className="text-xs uppercase tracking-wider text-muted-foreground">
        {label}
      </div>
      <div className={`mt-1 ${mono ? "font-mono text-sm" : "text-sm"}`}>
        {value || "-"}
      </div>
    </div>
  );
}
