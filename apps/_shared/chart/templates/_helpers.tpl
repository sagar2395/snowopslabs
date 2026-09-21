{{/* The app's name, required — every resource and the metric selector key off it. */}}
{{- define "workload.name" -}}
{{- required "appName is required" .Values.appName -}}
{{- end -}}

{{- define "workload.namespace" -}}
{{- .Values.namespace | default (include "workload.name" .) -}}
{{- end -}}

{{/*
Selector labels. "app" is not decoration: Prometheus relabels it onto every
series, and both the scenarios' PromQL and `labctl app verify` select on
app=<name>. Dropping it makes an app's metrics unfindable.
*/}}
{{- define "workload.selectorLabels" -}}
app.kubernetes.io/name: {{ include "workload.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app: {{ include "workload.name" . }}
{{- end -}}

{{- define "workload.labels" -}}
{{ include "workload.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
