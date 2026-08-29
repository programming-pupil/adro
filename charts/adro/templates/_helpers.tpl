{{- define "adro.name" -}}{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}{{- end -}}
{{- define "adro.fullname" -}}{{- printf "%s-%s" .Release.Name (include "adro.name" .) | trunc 63 | trimSuffix "-" -}}{{- end -}}
{{- define "adro.labels" -}}
app.kubernetes.io/name: {{ include "adro.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end -}}
