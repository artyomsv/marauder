import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { useT } from "@/i18n";
import { SELECT_CLASS } from "./SeasonEpisodePicker";

export type FolderNameMode = "default" | "keep" | "custom";

// The folder name settings (issue #225), grouped so TopicForm can hand them
// over as one value.
export interface FolderNameValue {
  folderNameMode: FolderNameMode;
  folderName: string;
}

interface FolderNameFieldsProps {
  value: FolderNameValue;
  onChange: (next: FolderNameValue) => void;
  // Display name of the receiving client when it cannot rename folders;
  // null when it can or is not known.
  unsupportedClient: string | null;
}

const MODES: FolderNameMode[] = ["default", "keep", "custom"];

// How the torrent's top folder is named in the client. TopicForm hides it for
// per-episode trackers and notify-only topics.
export function FolderNameFields({ value, onChange, unsupportedClient }: FolderNameFieldsProps) {
  const t = useT();
  return (
    <div className="space-y-2 rounded-md border border-border/60 bg-muted/20 p-3">
      <Label htmlFor="folder-name-mode">{t("topics.folderName.label")}</Label>
      <select
        id="folder-name-mode"
        value={value.folderNameMode}
        onChange={(e) => onChange({ ...value, folderNameMode: e.target.value as FolderNameMode })}
        className={SELECT_CLASS}
      >
        {MODES.map((mode) => (
          <option key={mode} value={mode}>
            {t(`topics.folderName.mode.${mode}`)}
          </option>
        ))}
      </select>
      <p className="text-xs text-muted-foreground">{t(`topics.folderName.help.${value.folderNameMode}`)}</p>

      {value.folderNameMode === "custom" && (
        <div className="space-y-1.5 pt-1">
          <Label htmlFor="folder-name">{t("topics.folderName.customLabel")}</Label>
          <Input
            id="folder-name"
            value={value.folderName}
            onChange={(e) => onChange({ ...value, folderName: e.target.value })}
            maxLength={255}
          />
        </div>
      )}

      {value.folderNameMode !== "default" && (
        <p className="text-xs text-muted-foreground">{t("topics.folderName.limits")}</p>
      )}

      {unsupportedClient && value.folderNameMode !== "default" && (
        <p className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-700 dark:text-amber-400">
          {t("topics.folderName.unsupported", { client: unsupportedClient })}
        </p>
      )}
    </div>
  );
}
