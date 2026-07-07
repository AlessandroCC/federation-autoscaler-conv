#!/usr/bin/env bash
#
# mock-down.sh — remove the mock-eco / mock-geo services. Does NOT touch the node.
#
# Usage:
#   mock-down.sh [--kubeconfig <path>] [--namespace <ns>]

set -euo pipefail
# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

KUBECONFIG_FLAG=""
usage() { awk 'NR>2{ if (/^#/) { sub(/^# ?/,""); print } else { exit } }' "$0"; exit "${1:-0}"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --kubeconfig) KUBECONFIG_FLAG="$2"; shift 2 ;;
    --namespace)  NAMESPACE="$2"; shift 2 ;;
    -h|--help)    usage 0 ;;
    *)            die "unknown argument: $1 (see --help)" ;;
  esac
done

ensure_tools kubectl
use_kubeconfig "$KUBECONFIG_FLAG"

log "Deleting the mock overlays"
kubectl delete -k "${FA_REPO_ROOT}/config/standalone/mock-eco" --ignore-not-found
kubectl delete -k "${FA_REPO_ROOT}/config/standalone/mock-geo" --ignore-not-found
kubectl delete namespace "$NAMESPACE" --ignore-not-found
ok "Mock services torn down."
