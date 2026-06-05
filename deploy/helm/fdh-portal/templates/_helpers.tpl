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

{{- define "fdh-portal.web.image" -}}
{{- $tag := .Values.web.image.tag | default .Chart.AppVersion -}}
{{ .Values.web.image.repository }}:{{ $tag }}
{{- end -}}
