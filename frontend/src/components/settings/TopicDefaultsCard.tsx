import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Settings2 } from "lucide-react";

import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { api, type UpdateSettingsBody } from "@/lib/api";
import { QK } from "@/lib/queryKeys";
import { cn } from "@/lib/utils";
import { useT } from "@/i18n";

/**
 * Admin-only "Topic defaults" settings section: the value new topics are
 * created with when their creator sends no explicit choice. Materialised
 * onto the topic at create time, so changing it here never touches topics
 * that already exist. The add-topic form seeds its per-topic toggle from
 * this, and the toggle can still override it.
 */
export function TopicDefaultsCard() {
  const t = useT();
  const queryClient = useQueryClient();
  const settings = useQuery({
    queryKey: QK.settings,
    queryFn: () => api.getSettings(),
  });

  const save = useMutation({
    mutationFn: (patch: UpdateSettingsBody) => api.updateSettings(patch),
    onSuccess: (next) => {
      queryClient.setQueryData(QK.settings, next);
    },
  });

  // The control must never invent a value: the stored default is unknown
  // until the read settles, and an unreadable one is not "off". The checkbox
  // stays disabled (and unwritable) until a real value is known, and a failed
  // read says so with a retry instead of showing an enabled switch that would
  // happily save the wrong thing over the truth.
  const known = settings.isSuccess;

  return (
    <Card>
      <CardHeader>
        <CardTitle className="flex items-center gap-2">
          <Settings2 className="size-4 text-muted-foreground" />
          {t("settings.topicDefaults.title")}
        </CardTitle>
        <CardDescription>{t("settings.topicDefaults.blurb")}</CardDescription>
      </CardHeader>
      <CardContent className="pt-0">
        {settings.isError ? (
          <div className="space-y-2">
            <p className="rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
              {t("settings.topicDefaults.loadFailed")}
            </p>
            <Button
              variant="outline"
              size="sm"
              onClick={() => settings.refetch()}
            >
              {t("settings.topicDefaults.retry")}
            </Button>
          </div>
        ) : (
          <div className="space-y-4">
            <DefaultToggleRow
              checked={
                known ? settings.data.default_follow_tracker_title : false
              }
              disabled={!known || save.isPending}
              label={t("topics.followTitle.label")}
              help={t("settings.topicDefaults.help")}
              onChange={(v) => save.mutate({ default_follow_tracker_title: v })}
            />
            <DefaultToggleRow
              checked={known ? settings.data.default_replace_on_update : false}
              disabled={!known || save.isPending}
              label={t("topics.replaceOnUpdate.label")}
              help={t("settings.topicDefaults.replaceHelp")}
              onChange={(v) => save.mutate({ default_replace_on_update: v })}
            />
            <DefaultToggleRow
              checked={
                known ? settings.data.default_replace_delete_data : false
              }
              disabled={
                !known ||
                save.isPending ||
                !settings.data.default_replace_on_update
              }
              label={t("topics.replaceOnUpdate.deleteData")}
              help={t("settings.topicDefaults.deleteDataHelp")}
              onChange={(v) => save.mutate({ default_replace_delete_data: v })}
              indent
            />
          </div>
        )}
        {save.isError && (
          <p className="mt-2 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
            {t("settings.topicDefaults.saveFailed")}
          </p>
        )}
      </CardContent>
    </Card>
  );
}

interface DefaultToggleRowProps {
  checked: boolean;
  disabled: boolean;
  label: string;
  help: string;
  onChange: (checked: boolean) => void;
  indent?: boolean;
}

function DefaultToggleRow({
  checked,
  disabled,
  label,
  help,
  onChange,
  indent,
}: DefaultToggleRowProps) {
  return (
    <label className={cn("flex items-start gap-2 text-sm", indent && "ml-6")}>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(e) => onChange(e.target.checked)}
        className="mt-0.5"
      />
      <span>
        <span className="font-medium text-foreground">{label}</span>
        <span className="block text-xs text-muted-foreground">{help}</span>
      </span>
    </label>
  );
}
