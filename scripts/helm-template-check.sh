#!/usr/bin/env sh
set -eu

HELM=${HELM:-helm}
CHART_DIR=${CHART_DIR:-deploy/prometheus-config-sync}

render() {
  "$HELM" template prometheus-config-sync "$CHART_DIR" "$@" >/dev/null
}

reject() {
  if render "$@" 2>/dev/null; then
    echo "expected Helm rendering to fail: $*" >&2
    exit 1
  fi
}

render
render -f "$CHART_DIR/values-dev.yaml"
render \
  --set persistence.create=false \
  --set persistence.existingClaim=prometheus-generated
render \
  --set sourceAuth.create=true \
  --set-string sourceAuth.token=local-test-token
render \
  --set serviceMonitor.enabled=true \
  --set prometheusRule.enabled=true
render \
  --set networkPolicy.enabled=true \
  --set-json 'networkPolicy.ingress=[{}]' \
  --set-json 'networkPolicy.egress=[{}]'
render --set-string image.digest=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
render --set config.interval=1m30s

reject --set replicaCount=2
reject --set persistence.enabled=false
reject --set persistence.create=false
reject --set persistence.existingClaim=claim --set persistence.create=true
reject --set config.metricsPath=/healthz
reject --set config.metricsPath=/livez
reject --set config.metricsPath=/readyz
reject --set config.metricsPath=/
reject --set-string config.sourceURL=ftp://source
reject --set-string 'config.sourceURL=http://invalid host'
reject --set-string config.prometheusReloadURL=https://user:secret@prometheus/-/reload
reject --set config.metricsPath=metrics
reject --set-string config.metricsPath=/metrics%
reject --set-string config.metricsPath=/metrics//nested
reject --set config.interval=0s
reject --set config.httpTimeout=invalid
reject --set config.validationTimeout=0s
reject --set config.maxConfigBytes=0
reject --set config.maxRulesBytes=0
reject --set serviceMonitor.enabled=true --set serviceMonitor.interval=invalid
reject --set serviceMonitor.enabled=true --set serviceMonitor.scrapeTimeout=0s
reject --set-json 'persistence.accessModes=[]'
reject --set-string config.extraArgs[0]=--output.dir=/tmp/generated
reject --set-string config.extraArgs[0]=--log.level=debug
reject --set-string podAnnotations.checksum/config=override
reject --set-string podAnnotations.checksum/source-secret=override
reject --set networkPolicy.enabled=true
reject --set-string image.digest=latest
reject --set sourceAuth.create=true
reject --set sourceAuth.create=true --set sourceAuth.existingSecret=source
reject --set prometheusRule.criticalSyncAgeSeconds=300

echo "Helm template matrix passed"
