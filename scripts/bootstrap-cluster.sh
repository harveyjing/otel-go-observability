#!/usr/bin/env bash
# bootstrap-cluster.sh — creates kind cluster and installs the observability stack.
# The dice apps (frontend :8080, backend :8081) run on localhost via run-apps.sh.
set -euo pipefail

CLUSTER=otelpoc
HERE="$(cd "$(dirname "$0")/.." && pwd)"

echo "==> Creating kind cluster"
if ! kind get clusters | grep -qx "$CLUSTER"; then
  kind create cluster --config "$HERE/deploy/kind/cluster.yaml" --name "$CLUSTER"
fi
kubectl config use-context "kind-$CLUSTER"

echo "==> Adding Helm repos"
helm repo add prometheus-community https://prometheus-community.github.io/helm-charts >/dev/null
helm repo add grafana               https://grafana.github.io/helm-charts >/dev/null
helm repo add open-telemetry        https://open-telemetry.github.io/opentelemetry-helm-charts >/dev/null
helm repo update

echo "==> Installing kube-prometheus-stack"
helm upgrade --install kube-prometheus-stack prometheus-community/kube-prometheus-stack \
  --namespace observability --create-namespace \
  --version 65.5.0 \
  --values "$HERE/deploy/helm-values/kube-prometheus-stack-values.yaml" \
  --wait --timeout 5m

echo "==> Installing tempo"
helm upgrade --install tempo grafana/tempo \
  --namespace observability --create-namespace \
  --version 1.10.0 \
  --values "$HERE/deploy/helm-values/tempo-values.yaml" \
  --wait --timeout 5m

echo "==> Installing loki"
helm upgrade --install loki grafana/loki \
  --namespace observability --create-namespace \
  --version 6.16.0 \
  --values "$HERE/deploy/helm-values/loki-values.yaml" \
  --wait --timeout 5m

echo "==> Installing opentelemetry-collector"
helm upgrade --install otel-collector open-telemetry/opentelemetry-collector \
  --namespace observability --create-namespace \
  --version 0.108.0 \
  --values "$HERE/deploy/helm-values/otel-collector-values.yaml" \
  --wait --timeout 5m

echo "==> Installing Grafana dashboards"
kubectl create configmap otel-poc-dashboard \
  --namespace observability \
  --from-file=otelpoc.json="$HERE/deploy/grafana/dashboard.json" \
  --dry-run=client -o yaml | \
  kubectl label --local --dry-run=client -o yaml -f - grafana_dashboard=1 | \
  kubectl apply -f -

kubectl create configmap otel-goruntime-dashboard \
  --namespace observability \
  --from-file=goruntime.json="$HERE/deploy/grafana/dashboard-go-runtime.json" \
  --dry-run=client -o yaml | \
  kubectl label --local --dry-run=client -o yaml -f - grafana_dashboard=1 | \
  kubectl apply -f -

cat <<EOF

Bootstrap complete. Observability stack is running in kind.

Host endpoints (published directly via kind extraPortMappings):
  OTel Collector OTLP gRPC: localhost:4317
  Grafana:                  http://localhost:3000  (admin / prom-operator)

Start apps locally:
  ./scripts/run-apps.sh

Generate load:
  ./scripts/load.sh

Teardown:
  ./scripts/teardown.sh

NOTE: If you previously had a kind cluster without these port mappings,
run ./scripts/teardown.sh first — extraPortMappings are immutable.
EOF
