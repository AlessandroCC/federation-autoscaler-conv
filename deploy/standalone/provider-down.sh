#!/usr/bin/env bash
#
# provider-down.sh — remove the provider agent. Does NOT touch the node.
#
# Usage:
#   provider-down.sh [--kubeconfig <path>] [--namespace <ns>] [--uninstall-liqo]
#
#   --uninstall-liqo  Also unpeer + `liqoctl uninstall` (best-effort; needs liqoctl).

set -euo pipefail
# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

KUBECONFIG_FLAG=""
UNINSTALL_LIQO=""
usage() { awk 'NR>2{ if (/^#/) { sub(/^# ?/,""); print } else { exit } }' "$0"; exit "${1:-0}"; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --kubeconfig)     KUBECONFIG_FLAG="$2"; shift 2 ;;
    --namespace)      NAMESPACE="$2"; shift 2 ;;
    --uninstall-liqo) UNINSTALL_LIQO=1; shift ;;
    -h|--help)        usage 0 ;;
    *)                die "unknown argument: $1 (see --help)" ;;
  esac
done

ensure_tools kubectl
use_kubeconfig "$KUBECONFIG_FLAG"

log "Deleting the provider agent overlay"
kubectl delete -k "${FA_REPO_ROOT}/config/standalone/agent-provider" --ignore-not-found
log "Deleting namespace ${NAMESPACE} (cascades agent-client-cert)"
kubectl delete namespace "$NAMESPACE" --ignore-not-found
if [[ -n "$UNINSTALL_LIQO" ]]; then
  ensure_tools liqoctl
  warn "Uninstalling Liqo (best-effort)"
  liqoctl unpeer --skip-confirm 2>/dev/null || true
  liqoctl uninstall --skip-confirm 2>/dev/null || true
fi
ok "Provider torn down."
