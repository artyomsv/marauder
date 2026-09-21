import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { motion } from "framer-motion";
import { AlertTriangle, Bug, Check, Copy, Download } from "lucide-react";

import { api, type Topic, type TopicDiagnosticsPage } from "@/lib/api";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { useT } from "@/i18n";

// Download is the primary action and Copy is secondary, which is the opposite
// of what "copy the HTML for the bug report" sounds like. A tracker topic page
// is around 110 KB; pasting that into an issue buries the report, while a
// 110 KB attachment is one line in it.
//
// The card is inline rather than a modal because this project has no Dialog
// primitive (see ResetTopicCard, same reason).
interface TopicDiagnosticsCardProps {
  topic: Topic;
  onClose: () => void;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${Math.round(n / 1024)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

// slug keeps the downloaded filename recognisable in a Downloads folder and
// safe on every OS: a tracker topic name is full of characters Windows
// refuses, and a topic could legitimately be named "../../etc/passwd".
function slug(s: string): string {
  return s.replace(/[^a-zA-Z0-9-_]+/g, "-").replace(/^-+|-+$/g, "").slice(0, 60) || "topic";
}

export function TopicDiagnosticsCard({ topic, onClose }: TopicDiagnosticsCardProps) {
  const t = useT();
  const [copied, setCopied] = useState(false);

  const fetchPage = useMutation<TopicDiagnosticsPage, Error>({
    mutationFn: () => api.topicDiagnosticsPage(topic.ID),
  });
  const page = fetchPage.data;

  const download = () => {
    if (!page) return;
    const blob = new Blob([page.html], { type: "text/html;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const a = document.createElement("a");
    a.href = url;
    a.download = `marauder-${page.tracker}-${slug(topic.DisplayName)}.html`;
    a.click();
    // Revoking immediately can race the download in some browsers; a tick is
    // enough and the object is small-lived either way.
    setTimeout(() => URL.revokeObjectURL(url), 1000);
  };

  const copy = async () => {
    if (!page) return;
    try {
      await navigator.clipboard.writeText(page.html);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard access is denied outside a secure context, which a
      // self-hosted install over plain http very much is. Downloading still
      // works, so this fails quietly rather than claiming success.
      setCopied(false);
    }
  };

  return (
    <motion.div initial={{ opacity: 0, y: -8 }} animate={{ opacity: 1, y: 0 }}>
      <Card className="space-y-3 p-4">
        <div className="flex items-center gap-2 text-sm font-medium">
          <Bug className="size-4" />
          {t("topics.diagnostics.title")}
        </div>

        <p className="text-xs text-muted-foreground">
          {t("topics.diagnostics.explain")}
        </p>

        {!page && (
          <Button size="sm" onClick={() => fetchPage.mutate()} disabled={fetchPage.isPending}>
            {fetchPage.isPending
              ? t("topics.diagnostics.fetching")
              : t("topics.diagnostics.fetch")}
          </Button>
        )}

        {fetchPage.isError && (
          <p className="flex items-start gap-1.5 text-xs text-destructive">
            <AlertTriangle className="mt-0.5 size-3 shrink-0" />
            {fetchPage.error.message}
          </p>
        )}

        {page && (
          <>
            <div className="text-xs text-muted-foreground">
              {page.tracker} · {formatBytes(page.bytes)}
              {!page.authenticated && ` · ${t("topics.diagnostics.anonymous")}`}
            </div>

            {/* The file is not the whole page, and a reporter should know
                that before attaching it — so say which parts are in it, and
                which the page did not have. A missing region is evidence. */}
            <div className="text-xs">
              <div className="text-muted-foreground">{t("topics.diagnostics.includes")}</div>
              <ul className="mt-1 space-y-0.5 font-mono">
                {page.regions.map((r) => (
                  <li key={r.name} className={r.found ? "" : "text-muted-foreground"}>
                    {r.found
                      ? `${r.name} · ${formatBytes(r.bytes)}`
                      : `${r.name} · ${t("topics.diagnostics.notFound")}`}
                  </li>
                ))}
              </ul>
            </div>

            {/* Not a disclaimer to click past. Redaction removes the secrets
                we know about on a page we do not control, so the honest thing
                is to say what was removed and ask the reader to look. */}
            <p className="flex items-start gap-1.5 rounded-md bg-muted p-2 text-xs">
              <AlertTriangle className="mt-0.5 size-3 shrink-0" />
              <span>
                {t("topics.diagnostics.warning")}{" "}
                <code className="font-mono">{page.redaction_mark}</code>
              </span>
            </p>

            <div className="flex gap-2">
              <Button size="sm" onClick={download}>
                <Download className="size-4" />
                {t("topics.diagnostics.download")}
              </Button>
              <Button size="sm" variant="outline" onClick={copy}>
                {copied ? <Check className="size-4" /> : <Copy className="size-4" />}
                {copied ? t("topics.diagnostics.copied") : t("topics.diagnostics.copy")}
              </Button>
            </div>
          </>
        )}

        <Button size="sm" variant="ghost" onClick={onClose}>
          {t("topics.diagnostics.close")}
        </Button>
      </Card>
    </motion.div>
  );
}
