{{/*
Expand the name of the chart.
*/}}
{{- define "marauder.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. Truncated at 63 chars (DNS label limit).
*/}}
{{- define "marauder.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "marauder.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels applied to every object.
*/}}
{{- define "marauder.labels" -}}
helm.sh/chart: {{ include "marauder.chart" . }}
{{ include "marauder.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: marauder
{{- end -}}

{{/*
Selector labels (stable across upgrades — never include version here).
*/}}
{{- define "marauder.selectorLabels" -}}
app.kubernetes.io/name: {{ include "marauder.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Per-component selector labels. Input: dict "root" $ "component" "backend".
*/}}
{{- define "marauder.componentSelectorLabels" -}}
{{ include "marauder.selectorLabels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/*
Per-component full label set. Input: dict "root" $ "component" "backend".
*/}}
{{- define "marauder.componentLabels" -}}
{{ include "marauder.labels" .root }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/*
ServiceAccount name.
*/}}
{{- define "marauder.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "marauder.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Render an image reference. Input: dict "image" <imageBlock> "root" $.
Falls back to the chart-wide image.tag / appVersion when the block omits tag.
*/}}
{{- define "marauder.image" -}}
{{- $img := .image -}}
{{- $root := .root -}}
{{- $repo := $img.repository -}}
{{- $tag := $img.tag | default $root.Values.image.tag | default $root.Chart.AppVersion -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end -}}

{{/*
============================================================================
VOLUME MODEL — the reusable volumeSpec helper.
Every persistent volume in the chart (db data, app config, downloads, and
each optional client/arr config) routes through these three helpers so the
six source types are supported uniformly. Adding a new backend = one edit.
============================================================================
*/}}

{{/*
Render a single pod `volumes:` list entry from a volumeSpec.
Input: dict "name" <volName> "spec" <persistenceBlock> "root" $.
Supported spec.type: existingClaim | pvc | nfs | hostPath | emptyDir | raw
*/}}
{{- define "marauder.volume" -}}
{{- $name := .name -}}
{{- $spec := .spec -}}
{{- $fullname := include "marauder.fullname" .root -}}
- name: {{ $name }}
{{- if eq $spec.type "existingClaim" }}
  persistentVolumeClaim:
    claimName: {{ required (printf "persistence %q: existingClaim is required when type=existingClaim" $name) $spec.existingClaim }}
{{- else if eq $spec.type "pvc" }}
  persistentVolumeClaim:
    claimName: {{ printf "%s-%s" $fullname $name }}
{{- else if eq $spec.type "nfs" }}
  nfs:
    server: {{ required (printf "persistence %q: nfs.server is required when type=nfs" $name) $spec.nfs.server }}
    path: {{ required (printf "persistence %q: nfs.path is required when type=nfs" $name) $spec.nfs.path }}
{{- else if eq $spec.type "hostPath" }}
  hostPath:
    path: {{ required (printf "persistence %q: hostPath.path is required when type=hostPath" $name) $spec.hostPath.path }}
    type: {{ $spec.hostPath.type | default "DirectoryOrCreate" }}
{{- else if eq $spec.type "emptyDir" }}
  emptyDir: {{- if $spec.emptyDir }}{{ toYaml $spec.emptyDir | nindent 4 }}{{- else }} {}{{- end }}
{{- else if eq $spec.type "raw" }}
{{ toYaml (required (printf "persistence %q: raw is required when type=raw" $name) $spec.raw) | indent 2 }}
{{- else }}
{{- fail (printf "persistence %q: unknown volume type %q (use existingClaim|pvc|nfs|hostPath|emptyDir|raw)" $name $spec.type) }}
{{- end }}
{{- end -}}

{{/*
Render a single `volumeMounts:` list entry.
Input: dict "name" <volName> "mountPath" <path> ["subPath" <sub>] ["readOnly" <bool>].
*/}}
{{- define "marauder.volumeMount" -}}
- name: {{ .name }}
  mountPath: {{ .mountPath }}
{{- if .subPath }}
  subPath: {{ .subPath }}
{{- end }}
{{- if .readOnly }}
  readOnly: true
{{- end }}
{{- end -}}

{{/*
Render a standalone PersistentVolumeClaim object for a type=pvc volumeSpec.
Input: dict "name" <volName> "spec" <persistenceBlock> "root" $.
Emits nothing for non-pvc types. Leading `---` lets callers concatenate.
*/}}
{{- define "marauder.pvc" -}}
{{- $name := .name -}}
{{- $spec := .spec -}}
{{- $root := .root -}}
{{- if eq $spec.type "pvc" -}}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: {{ printf "%s-%s" (include "marauder.fullname" $root) $name }}
  labels:
    {{- include "marauder.labels" $root | nindent 4 }}
spec:
  accessModes:
    {{- toYaml ($spec.pvc.accessModes | default (list "ReadWriteOnce")) | nindent 4 }}
{{- if $spec.pvc.storageClass }}
  storageClassName: {{ $spec.pvc.storageClass | quote }}
{{- end }}
  resources:
    requests:
      storage: {{ required (printf "persistence %q: pvc.size is required when type=pvc" $name) $spec.pvc.size }}
{{- end -}}
{{- end -}}

{{/*
Resolve the Postgres host the backend should connect to, based on db mode.
simple -> <fullname>-db ; cnpg -> <fullname>-db-rw (CNPG-managed primary svc).
*/}}
{{- define "marauder.dbHost" -}}
{{- if eq .Values.database.mode "cnpg" -}}
{{- printf "%s-db-rw" (include "marauder.fullname" .) -}}
{{- else -}}
{{- printf "%s-db" (include "marauder.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Name of the Secret holding MARAUDER_MASTER_KEY + MARAUDER_DB_PASSWORD.
Either the chart-created one or a user-supplied existingSecret.
*/}}
{{- define "marauder.secretName" -}}
{{- if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "marauder.fullname" .) -}}
{{- end -}}
{{- end -}}
