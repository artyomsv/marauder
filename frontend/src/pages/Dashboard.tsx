import { motion } from "framer-motion";
import { useQuery } from "@tanstack/react-query";
import { Radio, AlertTriangle, Sparkles, Clock } from "lucide-react";

import { api, type Topic } from "@/lib/api";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { formatRelative } from "@/lib/utils";

type TopicsList = { topics: Topic[] | null };

export function DashboardPage() {
  const { data } = useQuery({
    queryKey: ["topics"],
    queryFn: () => api.get<TopicsList>("/topics"),
  });
  const topics = data?.topics ?? [];

  const active = topics.filter((t) => t.Status === "active").length;
  const errored = topics.filter((t) => t.Status === "error").length;
  const updatedLast24h = topics.filter(
    (t) =>
      t.LastUpdatedAt &&
      Date.now() - new Date(t.LastUpdatedAt).getTime() < 86400_000,
  ).length;

  const tiles = [
    {
      icon: Radio,
      label: "Active topics",
      value: active,
      accent: "from-primary/40 to-primary/10",
    },
    {
      icon: Sparkles,
      label: "Updates in 24h",
      value: updatedLast24h,
      accent: "from-accent/40 to-accent/10",
    },
    {
      icon: AlertTriangle,
      label: "Errored",
      value: errored,
      accent: "from-destructive/40 to-destructive/10",
    },
    {
      icon: Clock,
      label: "Total tracked",
      value: topics.length,
      accent: "from-success/40 to-success/10",
    },
  ];

  return (
    <div className="space-y-10">
      <header>
        <motion.div
          initial={{ opacity: 0, y: 8 }}
          animate={{ opacity: 1, y: 0 }}
          transition={{ duration: 0.4 }}
        >
          <div className="mb-1 text-xs font-mono uppercase tracking-wider text-primary">
            overview
          </div>
          <h1 className="text-3xl font-semibold tracking-tight">
            Good to see you again.
          </h1>
          <p className="mt-2 text-sm text-muted-foreground">
            Here&apos;s what Marauder has been watching while you were away.
          </p>
        </motion.div>
      </header>

      <section className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {tiles.map((t, i) => (
          <motion.div
            key={t.label}
            initial={{ opacity: 0, y: 12 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ duration: 0.4, delay: 0.05 * i }}
          >
            <Card className="relative overflow-hidden">
              <div
                className={`pointer-events-none absolute -right-10 -top-10 size-40 rounded-full bg-gradient-to-br ${t.accent} blur-2xl`}
              />
              <div className="relative flex items-start justify-between p-6">
                <div>
                  <div className="text-xs uppercase tracking-wider text-muted-foreground">
                    {t.label}
                  </div>
                  <div className="mt-2 font-mono text-4xl font-semibold">
                    {t.value}
                  </div>
                </div>
                <div className="flex size-10 items-center justify-center rounded-lg border border-border/60 bg-background/40">
                  <t.icon className="size-5 text-foreground/80" />
                </div>
              </div>
            </Card>
          </motion.div>
        ))}
      </section>

      <section>
        <div className="mb-4 flex items-baseline justify-between">
          <h2 className="text-lg font-semibold tracking-tight">
            Recent activity
          </h2>
          <span className="text-xs text-muted-foreground">
            Last 10 topics updated
          </span>
        </div>
        <Card>
          {topics.length === 0 ? (
            <div className="flex flex-col items-center gap-3 p-12 text-center">
              <div className="flex size-12 items-center justify-center rounded-full bg-primary/10 text-primary">
                <Radio className="size-5" />
              </div>
              <div className="text-base font-medium">
                No topics yet
              </div>
              <div className="max-w-sm text-sm text-muted-foreground">
                Head over to the <span className="font-medium text-foreground">Topics</span>{" "}
                page and paste a tracker URL or magnet link to start watching.
              </div>
            </div>
          ) : (
            <div className="divide-y divide-border/60">
              {topics.slice(0, 10).map((t) => (
                <div
                  key={t.ID}
                  className="flex items-center gap-4 p-4 hover:bg-accent/5"
                >
                  <StatusDot status={t.Status} />
                  <div className="min-w-0 flex-1">
                    <div className="truncate font-medium">{t.DisplayName}</div>
                    <div className="font-mono text-xs text-muted-foreground truncate">
                      {t.URL}
                    </div>
                  </div>
                  <Badge variant="outline" className="hidden sm:inline-flex">
                    {t.TrackerName}
                  </Badge>
                  <div className="hidden md:block text-right">
                    <div className="text-xs text-muted-foreground">checked</div>
                    <div className="text-sm">
                      {formatRelative(t.LastCheckedAt)}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </Card>
      </section>
    </div>
  );
}

function StatusDot({ status }: { status: Topic["Status"] }) {
  const cls =
    status === "active"
      ? "bg-success"
      : status === "error"
      ? "bg-destructive"
      : "bg-muted-foreground";
  return (
    <span className="relative flex size-2.5">
      <span
        className={`absolute inline-flex h-full w-full animate-ping rounded-full ${cls} opacity-40`}
      />
      <span
        className={`relative inline-flex size-2.5 rounded-full ${cls}`}
      />
    </span>
  );
}
