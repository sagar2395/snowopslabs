{{/*
Expand the name of the chart.
*/}}
{{- define "go-api.name" -}}
go-api
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "go-api.fullname" -}}
go-api
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "go-api.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "go-api.labels" -}}
helm.sh/chart: {{ include "go-api.chart" . }}
{{ include "go-api.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "go-api.selectorLabels" -}}
app.kubernetes.io/name: {{ include "go-api.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app: go-api
{{- end }}
