import { useT } from "@/i18n";

// The four "when the topic updates" settings. Grouped so TopicForm can hand
// them over as one value.
export interface UpdatePolicyValue {
  replaceOnUpdate: boolean;
  replaceDeleteData: boolean;
  addPausedOnUpdate: boolean;
  onlyNewFiles: boolean;
}

interface UpdatePolicyFieldsProps {
  value: UpdatePolicyValue;
  onChange: (next: UpdatePolicyValue) => void;
  // Per-episode trackers deliver one torrent per episode, so the paused and
  // only-new-files settings have nothing to act on (issue #205).
  episodic: boolean;
  // Display name of the receiving client when it cannot pause or select
  // files; null when it can or is not known.
  unsupportedClient: string | null;
}

// Replace-on-update (issue #101) and the update policy (issue #205). The two
// interact: with only-new-files on, deleting the replaced version's files
// would lose them for good, so that box is forced off and locked.
export function UpdatePolicyFields({
  value,
  onChange,
  episodic,
  unsupportedClient,
}: UpdatePolicyFieldsProps) {
  const t = useT();
  const set = (patch: Partial<UpdatePolicyValue>) => onChange({ ...value, ...patch });

  return (
    <div className="space-y-2 rounded-md border border-border/60 bg-muted/20 p-3">
      <label className="flex items-center gap-2 text-sm font-medium text-foreground">
        <input
          type="checkbox"
          checked={value.replaceOnUpdate}
          onChange={(e) => set({ replaceOnUpdate: e.target.checked })}
        />
        <span>{t("topics.replaceOnUpdate.label")}</span>
      </label>
      <p className="text-xs text-muted-foreground">{t("topics.replaceOnUpdate.help")}</p>
      {value.replaceOnUpdate && (
        <label className="flex items-center gap-2 pt-1 text-sm">
          <input
            type="checkbox"
            checked={value.replaceDeleteData && !value.onlyNewFiles}
            disabled={value.onlyNewFiles}
            onChange={(e) => set({ replaceDeleteData: e.target.checked })}
          />
          <span>{t("topics.replaceOnUpdate.deleteData")}</span>
        </label>
      )}
      {value.replaceOnUpdate && value.onlyNewFiles && (
        <p className="text-xs text-muted-foreground">{t("topics.updatePolicy.deleteDataLocked")}</p>
      )}

      {!episodic && (
        <>
          <label className="flex items-center gap-2 pt-2 text-sm font-medium text-foreground">
            <input
              type="checkbox"
              checked={value.addPausedOnUpdate}
              onChange={(e) => set({ addPausedOnUpdate: e.target.checked })}
            />
            <span>{t("topics.updatePolicy.addPaused")}</span>
          </label>
          <p className="text-xs text-muted-foreground">{t("topics.updatePolicy.addPausedHelp")}</p>

          <label className="flex items-center gap-2 pt-2 text-sm font-medium text-foreground">
            <input
              type="checkbox"
              checked={value.onlyNewFiles}
              onChange={(e) =>
                set(
                  e.target.checked
                    ? { onlyNewFiles: true, replaceDeleteData: false }
                    : { onlyNewFiles: false },
                )
              }
            />
            <span>{t("topics.updatePolicy.onlyNewFiles")}</span>
          </label>
          <p className="text-xs text-muted-foreground">{t("topics.updatePolicy.onlyNewFilesHelp")}</p>

          {unsupportedClient && (value.addPausedOnUpdate || value.onlyNewFiles) && (
            <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
              {t("topics.updatePolicy.unsupported", { client: unsupportedClient })}
            </p>
          )}
        </>
      )}
    </div>
  );
}
