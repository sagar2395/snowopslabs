{{/*
Expand the name of the chart.
*/}}
{{- define "echo-server.name" -}}
echo-server
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "echo-server.fullname" -}}
echo-server
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "echo-server.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "echo-server.labels" -}}
helm.sh/chart: {{ include "echo-server.chart" . }}
{{ include "echo-server.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "echo-server.selectorLabels" -}}
app.kubernetes.io/name: {{ include "echo-server.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app: echo-server
{{- end }}
