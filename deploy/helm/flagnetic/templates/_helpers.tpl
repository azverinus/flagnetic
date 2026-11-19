{{- define "flagnetic.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "flagnetic.fullname" -}}
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

{{/*
The full name cut to 55 characters, so that -migrate and -metrics still fit the
63 a Job's and a Service's name may have.
*/}}
{{- define "flagnetic.shortname" -}}
{{- include "flagnetic.fullname" . | trunc 55 | trimSuffix "-" -}}
{{- end -}}

{{- define "flagnetic.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "flagnetic.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "flagnetic.selectorLabels" -}}
app.kubernetes.io/name: {{ include "flagnetic.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
The server's pods alone. The migration Job's pod and the helm test Pod carry
the release's labels too; without the component, the Services would route
requests to them, and the budget and the spread would count them.
*/}}
{{- define "flagnetic.serverSelectorLabels" -}}
{{ include "flagnetic.selectorLabels" . }}
app.kubernetes.io/component: server
{{- end -}}

{{- define "flagnetic.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "flagnetic.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{- define "flagnetic.image" -}}
{{- printf "%s:%s" .Values.image.repository (default .Chart.AppVersion .Values.image.tag) -}}
{{- end -}}

{{/* The Secret the server reads DATABASE_URL and FLAGNETIC_SECRET_KEY from. */}}
{{- define "flagnetic.secretName" -}}
{{- default (include "flagnetic.fullname" .) .Values.secrets.existingSecret -}}
{{- end -}}

{{/*
Refuses to render without the secrets, and with settings the chart owns.
Every template that renders a workload includes it.
*/}}
{{- define "flagnetic.validate" -}}
{{- if not .Values.secrets.existingSecret -}}
{{- if or (not .Values.secrets.databaseUrl) (not .Values.secrets.secretKey) -}}
{{- fail "flagnetic: set secrets.existingSecret to a Secret holding DATABASE_URL and FLAGNETIC_SECRET_KEY, or set both secrets.databaseUrl and secrets.secretKey; the chart never generates them, because the server refuses to start under a secret key other than the one it first stored" -}}
{{- end -}}
{{- end -}}
{{- range $key := list "listen_addr" "metrics_listen_addr" "auto_migrate" "database_url" "secret_key" -}}
{{- if hasKey $.Values.config $key -}}
{{- fail (printf "flagnetic: config.%s is managed by the chart: use migrations.mode, service.port, service.metrics.port or secrets instead" $key) -}}
{{- end -}}
{{- end -}}
{{- /* A pod killed before the server has stopped loses the mail, the
evaluation counts and the SDK inventory it was still writing. The server
stops within the drain, the grace period and the cancel step after them
(docs/operations.md, "Stopping"), so the pod gets at least as long. */ -}}
{{- $drain := "5s" -}}
{{- if and (hasKey .Values.config "shutdown_drain") (ne (toString .Values.config.shutdown_drain) "") -}}{{- $drain = .Values.config.shutdown_drain -}}{{- end -}}
{{- $grace := "15s" -}}
{{- if and (hasKey .Values.config "shutdown_grace") (ne (toString .Values.config.shutdown_grace) "") -}}{{- $grace = .Values.config.shutdown_grace -}}{{- end -}}
{{- $stop := add (ceil (addf (include "flagnetic.durationSeconds" $drain) (include "flagnetic.durationSeconds" $grace))) 10 -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) (int $stop) -}}
{{- fail (printf "flagnetic: terminationGracePeriodSeconds is %d and must be at least %d: config.shutdown_drain plus config.shutdown_grace plus the 10 seconds the server takes to cancel what is left" (int .Values.terminationGracePeriodSeconds) (int $stop)) -}}
{{- end -}}
{{- end -}}

{{/*
The seconds a Go duration such as 20s, 1m30s or 500ms lasts. A value that is
no duration, which the server refuses, counts as nothing here.
*/}}
{{- define "flagnetic.durationSeconds" -}}
{{- $units := dict "ns" 0.000000001 "us" 0.000001 "µs" 0.000001 "ms" 0.001 "s" 1.0 "m" 60.0 "h" 3600.0 -}}
{{- $total := 0.0 -}}
{{- range $part := regexFindAll "[0-9.]+(ns|us|µs|ms|s|m|h)" (toString .) -1 -}}
{{- $number := regexFind "^[0-9.]+" $part -}}
{{- $total = addf $total (mulf (float64 $number) (get $units (trimPrefix $number $part))) -}}
{{- end -}}
{{- $total -}}
{{- end -}}

{{/* The environment of the server and the migration: DATABASE_URL and extras. */}}
{{- define "flagnetic.databaseEnv" -}}
- name: DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ include "flagnetic.secretName" . }}
      key: {{ .Values.secrets.databaseUrlKey }}
{{- end -}}

{{/*
The settings of config as variables. The migration reads them too: the log
format and level and the pool size apply to it as to the server.
*/}}
{{- define "flagnetic.configEnv" -}}
{{- range $key, $value := .Values.config }}
- name: {{ printf "FLAGNETIC_%s" ($key | upper) }}
  {{- if kindIs "float64" $value }}
  value: {{ int64 $value | toString | quote }}
  {{- else if and (hasPrefix "rate_limit_" $key) (kindIs "bool" $value) }}
  {{- /* An unquoted off is a boolean in YAML 1.1, which Helm reads; the
  setting takes off, never false or true. */}}
  {{- if $value }}
  {{- fail (printf "flagnetic: config.%s is true, which is no limit: give a whole number, or \"off\" in quotes" $key) }}
  {{- end }}
  value: "off"
  {{- else }}
  value: {{ $value | toString | quote }}
  {{- end }}
{{- end }}
{{- end -}}

{{/* What the migration runs with: the database, config and extraEnv. */}}
{{- define "flagnetic.migrationEnv" -}}
{{ include "flagnetic.databaseEnv" . }}
{{- include "flagnetic.configEnv" . }}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{- define "flagnetic.serverEnv" -}}
{{ include "flagnetic.databaseEnv" . }}
- name: FLAGNETIC_SECRET_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "flagnetic.secretName" . }}
      key: {{ .Values.secrets.secretKeyKey }}
- name: FLAGNETIC_LISTEN_ADDR
  value: {{ printf ":%d" (int .Values.service.port) | quote }}
- name: FLAGNETIC_METRICS_LISTEN_ADDR
  value: {{ printf ":%d" (int .Values.service.metrics.port) | quote }}
- name: FLAGNETIC_AUTO_MIGRATE
  value: {{ ternary "true" "false" (eq .Values.migrations.mode "auto") | quote }}
{{- include "flagnetic.configEnv" . }}
{{- with .Values.extraEnv }}
{{ toYaml . }}
{{- end }}
{{- end -}}

{{/* The security context every container of the chart runs with. */}}
{{- define "flagnetic.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop:
    - ALL
{{- end -}}

{{- define "flagnetic.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: 65532
runAsGroup: 65532
seccompProfile:
  type: RuntimeDefault
{{- end -}}
