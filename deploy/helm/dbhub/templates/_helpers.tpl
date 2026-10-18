{{- define "dbhub.name" -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 50 | trimSuffix "-" -}}
{{- end -}}
{{- define "dbhub.labels" -}}
app.kubernetes.io/name: dbhub
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}
{{- define "dbhub.image" -}}
{{- $repo := required "Set image.repository to a built/published DBHub image" .repository -}}
{{- if .digest -}}{{ $repo }}@{{ .digest }}
{{- else -}}{{ $repo }}:{{ required "Set image.tag or image.digest" .tag }}{{- end -}}
{{- end -}}
{{- define "dbhub.validate" -}}
{{- if ne (int .Values.backend.replicas) 1 -}}{{ fail "backend.replicas must be 1: workbench transactions require a single backend" }}{{- end -}}
{{- $secret := required "existingSecret is required; credentials must not be stored in Helm values" .Values.existingSecret -}}
{{- $allow := required "config.DBHUB_TARGET_ALLOWLIST is required" .Values.config.DBHUB_TARGET_ALLOWLIST -}}
{{- end -}}
