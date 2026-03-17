{{/*
Expand the name of the chart.
*/}}
{{- define "jumpcloud-teleport-sync.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Fullname: release-chart
*/}}
{{- define "jumpcloud-teleport-sync.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "jumpcloud-teleport-sync.labels" -}}
helm.sh/chart: {{ include "jumpcloud-teleport-sync.name" . }}-{{ .Chart.Version | replace "+" "_" }}
{{ include "jumpcloud-teleport-sync.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "jumpcloud-teleport-sync.selectorLabels" -}}
app.kubernetes.io/name: {{ include "jumpcloud-teleport-sync.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name
*/}}
{{- define "jumpcloud-teleport-sync.serviceAccountName" -}}
{{- if .Values.serviceAccount.name }}
{{- .Values.serviceAccount.name }}
{{- else }}
{{- include "jumpcloud-teleport-sync.fullname" . }}
{{- end }}
{{- end }}

{{/*
Secret name
*/}}
{{- define "jumpcloud-teleport-sync.secretName" -}}
{{- if .Values.existingSecret }}
{{- .Values.existingSecret }}
{{- else }}
{{- include "jumpcloud-teleport-sync.fullname" . }}
{{- end }}
{{- end }}
