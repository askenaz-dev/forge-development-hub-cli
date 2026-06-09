{{/*
Common helpers for the fdh-portal chart.
*/}}

{{- define "fdh-portal.fullname" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fdh-portal.api.name" -}}
{{ include "fdh-portal.fullname" . }}-api
{{- end -}}

{{- define "fdh-portal.web.name" -}}
{{ include "fdh-portal.fullname" . }}-web
{{- end -}}

{{- define "fdh-portal.labels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/*
Selector labels: the STABLE subset used in Deployment/StatefulSet matchLabels
and Service selectors. These MUST NOT include version- or chart-dependent
labels (app.kubernetes.io/version, helm.sh/chart) — selectors are immutable,
so a chart-version bump would otherwise break the upgrade and orphan Services
from their pods. Component is added at each call site.
*/}}
{{- define "fdh-portal.selectorLabels" -}}
app.kubernetes.io/name: {{ .Chart.Name }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "fdh-portal.api.image" -}}
{{- $tag := .Values.api.image.tag | default .Chart.AppVersion -}}
{{ .Values.api.image.repository }}:{{ $tag }}
{{- end -}}

{{/*
Telemetry Postgres store names (hub-usage-telemetry Phase 2). All gated by
.Values.telemetry.store.enabled at the call sites — these helpers only build
the names so the DSN host and the Secret/Service references stay consistent.
*/}}
{{- define "fdh-portal.telemetry.name" -}}
{{ include "fdh-portal.fullname" . }}-telemetry-postgres
{{- end -}}

{{- define "fdh-portal.telemetry.headlessName" -}}
{{ include "fdh-portal.telemetry.name" . }}-headless
{{- end -}}

{{/*
Secret holding POSTGRES_PASSWORD + the assembled FDH_TELEMETRY_DSN. When the
operator supplies telemetry.store.existingSecret, that name is used verbatim
and the chart renders no Secret of its own.
*/}}
{{- define "fdh-portal.telemetry.secretName" -}}
{{- if .Values.telemetry.store.existingSecret -}}
{{ .Values.telemetry.store.existingSecret }}
{{- else -}}
{{ include "fdh-portal.telemetry.name" . }}
{{- end -}}
{{- end -}}

{{/*
In-cluster password for the chart-managed Secret. Order of precedence:
  1. explicit telemetry.store.password
  2. the password already stored in a prior release's Secret (lookup -> stable
     across `helm upgrade`, so the DSN never drifts out from under the data)
  3. a freshly generated random password (first install)
Only consulted when existingSecret is empty.
*/}}
{{- define "fdh-portal.telemetry.password" -}}
{{- if .Values.telemetry.store.password -}}
{{ .Values.telemetry.store.password }}
{{- else -}}
{{- $existing := (lookup "v1" "Secret" .Release.Namespace (include "fdh-portal.telemetry.name" .)) -}}
{{- if and $existing $existing.data (index $existing.data "POSTGRES_PASSWORD") -}}
{{ index $existing.data "POSTGRES_PASSWORD" | b64dec }}
{{- else -}}
{{ randAlphaNum 32 }}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The assembled libpq DSN the API reads from FDH_TELEMETRY_DSN. Host is the
headless Service's stable cluster DNS; sslmode=disable for the in-cluster hop.
*/}}
{{- define "fdh-portal.telemetry.dsn" -}}
{{- $pw := include "fdh-portal.telemetry.password" . -}}
{{- printf "postgres://%s:%s@%s.%s.svc.cluster.local:5432/%s?sslmode=disable" .Values.telemetry.store.user $pw (include "fdh-portal.telemetry.headlessName" .) .Release.Namespace .Values.telemetry.store.database -}}
{{- end -}}

{{- define "fdh-portal.web.image" -}}
{{- $tag := .Values.web.image.tag | default .Chart.AppVersion -}}
{{ .Values.web.image.repository }}:{{ $tag }}
{{- end -}}
