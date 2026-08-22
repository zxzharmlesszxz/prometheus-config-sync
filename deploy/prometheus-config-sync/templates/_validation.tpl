{{- define "prometheus-config-sync.validate" -}}
{{- if ne (int .Values.replicaCount) 1 }}
{{- fail "replicaCount must be 1: multiple writers cannot safely publish the same generated files" }}
{{- end }}
{{- if not .Values.persistence.enabled }}
{{- fail "persistence.enabled must be true: generated files must live on the volume shared with Prometheus" }}
{{- end }}
{{- if and .Values.persistence.create (not (empty .Values.persistence.existingClaim)) }}
{{- fail "set only one persistence mode: create=true or existingClaim" }}
{{- end }}
{{- if and (not .Values.persistence.create) (empty .Values.persistence.existingClaim) }}
{{- fail "persistence requires create=true or a non-empty existingClaim" }}
{{- end }}
{{- if or (empty .Values.config.sourceURL) (empty .Values.config.prometheusReloadURL) (empty .Values.config.outputDir) }}
{{- fail "config.sourceURL, config.prometheusReloadURL, and config.outputDir are required" }}
{{- end }}
{{- $endpointPattern := "^https?://[^[:space:]/@?#]+(/[^?#[:space:]]*)?$" }}
{{- if not (regexMatch $endpointPattern .Values.config.sourceURL) }}
{{- fail "config.sourceURL must be an HTTP(S) URL without credentials, query, or fragment" }}
{{- end }}
{{- if not (regexMatch $endpointPattern .Values.config.prometheusReloadURL) }}
{{- fail "config.prometheusReloadURL must be an HTTP(S) URL without credentials, query, or fragment" }}
{{- end }}
{{- if eq .Values.config.metricsPath "/" }}
{{- fail "config.metricsPath must not be the root path" }}
{{- end }}
{{- if has .Values.config.metricsPath (list "/healthz" "/livez" "/readyz") }}
{{- fail "config.metricsPath must not conflict with a health endpoint" }}
{{- end }}
{{- if hasSuffix .Values.config.metricsPath "/" }}
{{- fail "config.metricsPath must not end with '/'" }}
{{- end }}
{{- if contains "*" .Values.config.metricsPath }}
{{- fail "config.metricsPath must be a literal path and must not contain '*'" }}
{{- end }}
{{- if contains "//" .Values.config.metricsPath }}
{{- fail "config.metricsPath must not contain consecutive slashes" }}
{{- end }}
{{- if not (regexMatch "^/[A-Za-z0-9._~!$&()*+,;=:@/-]*$" .Values.config.metricsPath) }}
{{- fail "config.metricsPath must be a literal absolute HTTP path" }}
{{- end }}
{{- if ne (int .Values.service.port) 9534 }}
{{- fail "service.port must be 9534 because charts and container port/probes are fixed to 9534" }}
{{- end }}
{{- $durationPattern := "^[1-9][0-9]*(\\.[0-9]+)?(ns|us|µs|ms|s|m|h)([0-9]+(\\.[0-9]+)?(ns|us|µs|ms|s|m|h))*$" }}
{{- if not (regexMatch $durationPattern .Values.config.interval) }}
{{- fail "config.interval must be a positive Go duration, for example 30s or 1m30s" }}
{{- end }}
{{- if not (regexMatch $durationPattern .Values.config.httpTimeout) }}
{{- fail "config.httpTimeout must be a positive Go duration" }}
{{- end }}
{{- if not (regexMatch $durationPattern .Values.config.validationTimeout) }}
{{- fail "config.validationTimeout must be a positive Go duration" }}
{{- end }}
{{- if and .Values.serviceMonitor.enabled (not (regexMatch $durationPattern .Values.serviceMonitor.interval)) }}
{{- fail "serviceMonitor.interval must be a positive duration" }}
{{- end }}
{{- if and .Values.serviceMonitor.enabled (not (regexMatch $durationPattern .Values.serviceMonitor.scrapeTimeout)) }}
{{- fail "serviceMonitor.scrapeTimeout must be a positive duration" }}
{{- end }}
{{- if le (int64 .Values.config.maxConfigBytes) 0 }}
{{- fail "config.maxConfigBytes must be greater than zero" }}
{{- end }}
{{- if le (int64 .Values.config.maxRulesBytes) 0 }}
{{- fail "config.maxRulesBytes must be greater than zero" }}
{{- end }}
{{- if and .Values.image.digest (not (regexMatch "^sha256:[a-f0-9]{64}$" .Values.image.digest)) }}
{{- fail "image.digest must be empty or a sha256 digest" }}
{{- end }}
{{- $reservedArgs := list "--source." "--prometheus.reload-url" "--output.dir" "--promtool.path" "--interval" "--http.timeout" "--validation.timeout" "--web.metrics-path" "--web.listen-address" "--log.level" -}}
{{- range $arg := .Values.config.extraArgs }}
{{- range $prefix := $reservedArgs }}
{{- if hasPrefix $prefix $arg }}
{{- fail (printf "config.extraArgs must not override chart-managed flag %s" $prefix) }}
{{- end }}
{{- end }}
{{- end }}
{{- if and .Values.sourceAuth.create (not (empty .Values.sourceAuth.existingSecret)) }}
{{- fail "set only one source auth mode: create=true or existingSecret" }}
{{- end }}
{{- if and .Values.sourceAuth.create (empty .Values.sourceAuth.token) }}
{{- fail "sourceAuth.token is required when sourceAuth.create=true" }}
{{- end }}
{{- if and (or .Values.sourceAuth.create (not (empty .Values.sourceAuth.existingSecret))) (empty .Values.sourceAuth.existingSecretKey) }}
{{- fail "sourceAuth.existingSecretKey is required when HTTP source authentication is configured" }}
{{- end }}
{{- if ne .Values.config.listenAddress ":9534" }}
{{- fail "config.listenAddress must remain :9534 because the container port and probes use 9534" }}
{{- end }}
{{- if and .Values.networkPolicy.enabled (or (empty .Values.networkPolicy.ingress) (empty .Values.networkPolicy.egress)) }}
{{- fail "networkPolicy ingress and egress rules are both required when networkPolicy.enabled=true" }}
{{- end }}
{{- if le (int .Values.prometheusRule.maxSyncAgeSeconds) 0 }}
{{- fail "prometheusRule.maxSyncAgeSeconds must be greater than zero" }}
{{- end }}
{{- if le (int .Values.prometheusRule.criticalSyncAgeSeconds) (int .Values.prometheusRule.maxSyncAgeSeconds) }}
{{- fail "prometheusRule.criticalSyncAgeSeconds must be greater than prometheusRule.maxSyncAgeSeconds" }}
{{- end }}
{{- if empty .Values.persistence.accessModes }}
{{- fail "persistence.accessModes must contain at least one Kubernetes access mode" }}
{{- end }}
{{- if and .Values.podDisruptionBudget.enabled (ne (int .Values.podDisruptionBudget.maxUnavailable) 1) }}
{{- fail "podDisruptionBudget.maxUnavailable must be 1 for this singleton deployment" }}
{{- end }}
{{- if hasKey .Values.podAnnotations "checksum/config" }}
{{- fail "podAnnotations must not override checksum/config" }}
{{- end }}
{{- if hasKey .Values.podAnnotations "checksum/source-secret" }}
{{- fail "podAnnotations must not override checksum/source-secret" }}
{{- end }}
{{- end }}
