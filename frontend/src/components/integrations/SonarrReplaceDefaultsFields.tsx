import { useQuery } from "@tanstack/react-query";

import { Field } from "@/components/integrations/SonarrFields";
import { SELECT_CLASS } from "@/components/topics/SeasonEpisodePicker";
import { useT } from "@/i18n";
import { api } from "@/lib/api";
import { QK } from "@/lib/queryKeys";

interface Props {
  replace: boolean | null;
  deleteData: boolean | null;
  onReplaceChange: (value: boolean | null) => void;
  onDeleteDataChange: (value: boolean | null) => void;
}

export function SonarrReplaceDefaultsFields({
  replace,
  deleteData,
  onReplaceChange,
  onDeleteDataChange,
}: Props) {
  const t = useT();
  const settings = useQuery({
    queryKey: QK.settings,
    queryFn: () => api.getSettings(),
  });
  const effectiveReplace =
    replace ?? settings.data?.default_replace_on_update ?? false;
  return (
    <>
      <DefaultSelect
        label={t("settings.sonarr.defaultReplaceOnUpdate")}
        value={replace}
        globalValue={settings.data?.default_replace_on_update}
        onChange={onReplaceChange}
      />
      <DefaultSelect
        label={t("settings.sonarr.defaultReplaceDeleteData")}
        value={deleteData}
        globalValue={settings.data?.default_replace_delete_data}
        disabled={!effectiveReplace}
        onChange={onDeleteDataChange}
      />
    </>
  );
}

interface DefaultSelectProps {
  label: string;
  value: boolean | null;
  globalValue: boolean | undefined;
  disabled?: boolean;
  onChange: (value: boolean | null) => void;
}

function DefaultSelect({
  label,
  value,
  globalValue,
  disabled,
  onChange,
}: DefaultSelectProps) {
  const t = useT();
  const inheritLabel =
    globalValue === undefined
      ? t("settings.sonarr.useGlobalDefault")
      : t("settings.sonarr.useGlobalDefaultCurrent", {
          value: t(globalValue ? "common.on" : "common.off"),
        });
  return (
    <Field label={label}>
      <select
        aria-label={label}
        value={value === null ? "inherit" : String(value)}
        disabled={disabled}
        onChange={(e) =>
          onChange(
            e.target.value === "inherit" ? null : e.target.value === "true",
          )
        }
        className={SELECT_CLASS}
      >
        <option value="inherit">{inheritLabel}</option>
        <option value="true">{t("common.on")}</option>
        <option value="false">{t("common.off")}</option>
      </select>
    </Field>
  );
}
