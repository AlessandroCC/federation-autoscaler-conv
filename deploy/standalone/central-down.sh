#!/usr/bin/env bash
#
# central-down.sh — remove the broker from the central cluster. Does NOT touch
# the node (no k3s uninstall) or the federation CA on disk (--ca-dir).
#
# Usage:
#   central-down.sh [--kubeconfig <path>] [--namespace <ns>] [--delete-crds]
#
#   --delete-crds  Also remove the federation CRDs (cluster-scoped, shared).

set -euo pipefail
# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

KUBECONFIG_FLAG=""
DELETE_CRDS=""
usage() { awk 'NR>2{ if (/^#/) { sub(/^# ?/,""); print } else { exit } }' "$0"; exit "${1:-0}"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --kubeconfig) KUBECONFIG_FLAG="$2"; shift 2 ;;
    --namespace)  NAMESPACE="$2"; shift 2 ;;
    --delete-crds) DELETE_CRDS=1; shift ;;
    -h|--help)    usage 0 ;;
    *)            die "unknown argument: $1 (see --help)" ;;
  esac
done

ensure_tools kubectl
use_kubeconfig "$KUBECONFIG_FLAG"

# The broker stamps a finalizer (broker.federation-autoscaler.io/release-chunks)
# on every Reservation and is the ONLY thing that removes it. Deleting the broker
# and the namespace together leaves those finalizers orphaned, so the namespace
# hangs forever in Terminating. Order matters: drop the controller first, strip
# the finalizers ourselves, then delete the rest.
log "Deleting the broker Deployment (first, so it can't re-add Reservation finalizers)"
kubectl -n "$NAMESPACE" delete deploy/broker --ignore-not-found

if kubectl get crd reservations.broker.federation-autoscaler.io >/dev/null 2>&1; then
  log "Releasing Reservation finalizers so the namespace can terminate cleanly"
  kubectl get reservations.broker.federation-autoscaler.io -n "$NAMESPACE" -o name 2>/dev/null \
    | xargs -r -I{} kubectl patch {} -n "$NAMESPACE" --type=merge \
        -p '{"metadata":{"finalizers":null}}' >/dev/null 2>&1 || true
fi

log "Deleting the broker overlay"
kubectl delete -k "${FA_REPO_ROOT}/config/standalone/broker" --ignore-not-found
log "Deleting namespace ${NAMESPACE} (cascades broker-server-cert)"
kubectl delete namespace "$NAMESPACE" --ignore-not-found
if [[ -n "$DELETE_CRDS" ]]; then
  log "Deleting federation CRDs"
  kubectl delete -k "${FA_REPO_ROOT}/config/crd" --ignore-not-found
fi
ok "Central torn down. The federation CA (--ca-dir) on this machine was NOT touched."
