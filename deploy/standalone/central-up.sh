#!/usr/bin/env bash
#
# central-up.sh — deploy the federation-autoscaler BROKER onto the central
# cluster, and mint per-participant join bundles. Standalone (no Ansible, no
# cert-manager); run by the central-cluster admin against their own cluster.
#
# This script is the root of trust: it generates the federation CA (kept ONLY
# on this machine, under --ca-dir) and signs (a) the broker's server cert and
# (b) one client cert per participating provider/consumer cluster. The CA
# private key never leaves this host.
#
# Usage:
#   central-up.sh --public-endpoint <ip|host> [--tag <tag>] [--registry <reg>]
#                 [--kubeconfig <path>] [--namespace <ns>] [--ca-dir <dir>]
#
#   central-up.sh join --cluster-id <id> [--out <file.tgz>]
#                 [--ca-dir <dir>] [--broker-url <url>]
#
#   --public-endpoint  Routable IP or hostname agents will dial the broker at
#                      (becomes the broker cert SAN and the published URL
#                      https://<endpoint>:30443). Required for the deploy.
#   --tag / --registry Image coordinate: <registry>/federation-autoscaler-broker:<tag>
#                      (default registry docker.io/kazem26, tag latest).
#   --ca-dir           Where the federation CA (ca.crt/ca.key) + broker-url are
#                      stored/reused (default ./fa-pki). KEEP THIS PRIVATE.
#
#   join               Mint a bundle for one participant: broker URL + ca.crt +
#                      a client cert/key with CN=<cluster-id>. Hand the bundle to
#                      that cluster's admin OUT OF BAND (it contains a private key).
#
# Assumes: an already-running Kubernetes cluster reachable via KUBECONFIG. Any
# missing CLI tools (kubectl, openssl, tar) are installed automatically — you
# only need Kubernetes + a kubeconfig, sudo, and outbound internet.

set -euo pipefail
# shellcheck source=common.sh
source "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/common.sh"

# ----------------------------------------------------------------------------
# Defaults + arg parsing
# ----------------------------------------------------------------------------
PUBLIC_ENDPOINT=""
CA_DIR="./fa-pki"
JOIN_CLUSTER_ID=""
JOIN_OUT=""
JOIN_BROKER_URL=""
KUBECONFIG_FLAG=""

SUBCOMMAND="up"
if [[ "${1:-}" == "join" ]]; then SUBCOMMAND="join"; shift; fi
if [[ "${1:-}" == "up" ]]; then shift; fi

usage() { awk 'NR>2{ if (/^#/) { sub(/^# ?/,""); print } else { exit } }' "$0"; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --public-endpoint) PUBLIC_ENDPOINT="$2"; shift 2 ;;
    --tag)             TAG="$2"; shift 2 ;;
    --registry)        REGISTRY="$2"; shift 2 ;;
    --kubeconfig)      KUBECONFIG_FLAG="$2"; shift 2 ;;
    --namespace)       NAMESPACE="$2"; shift 2 ;;
    --ca-dir)          CA_DIR="$2"; shift 2 ;;
    --cluster-id)      JOIN_CLUSTER_ID="$2"; shift 2 ;;
    --out)             JOIN_OUT="$2"; shift 2 ;;
    --broker-url)      JOIN_BROKER_URL="$2"; shift 2 ;;
    -h|--help)         usage 0 ;;
    *)                 die "unknown argument: $1 (see --help)" ;;
  esac
done

# ----------------------------------------------------------------------------
# join — mint one participant bundle (no cluster access needed)
# ----------------------------------------------------------------------------
if [[ "$SUBCOMMAND" == "join" ]]; then
  ensure_tools openssl tar
  [[ -n "$JOIN_CLUSTER_ID" ]] || die "join: --cluster-id <id> is required"
  [[ -s "$CA_DIR/ca.crt" && -s "$CA_DIR/ca.key" ]] \
    || die "join: no CA in $CA_DIR — run 'central-up.sh --public-endpoint …' first (or pass --ca-dir)"

  broker_url="$JOIN_BROKER_URL"
  [[ -z "$broker_url" && -s "$CA_DIR/broker-url" ]] && broker_url="$(cat "$CA_DIR/broker-url")"
  [[ -n "$broker_url" ]] || die "join: broker URL unknown — pass --broker-url https://<endpoint>:30443"

  out="${JOIN_OUT:-${JOIN_CLUSTER_ID}-bundle.tgz}"
  work="$(mktemp -d)"; trap 'rm -rf "$work"' EXIT
  log "Minting join bundle for cluster-id '${JOIN_CLUSTER_ID}'"
  sign_leaf "$CA_DIR" "$work/client" "$JOIN_CLUSTER_ID" "clientAuth"
  cp "$CA_DIR/ca.crt" "$work/ca.crt"
  printf '%s\n' "$broker_url" > "$work/broker-url"
  tar -C "$work" -czf "$out" broker-url ca.crt client.crt client.key
  ok "wrote $out"
  cat <<EOF

Send '$out' to the '${JOIN_CLUSTER_ID}' cluster admin OVER A SECURE CHANNEL
(it contains a private key); they pass it to their up-script with --bundle.
EOF
  exit 0
fi

# ----------------------------------------------------------------------------
# up — deploy the broker
# ----------------------------------------------------------------------------
[[ -n "$PUBLIC_ENDPOINT" ]] || die "--public-endpoint <ip|host> is required (see --help)"
ensure_tools kubectl openssl
use_kubeconfig "$KUBECONFIG_FLAG"

BROKER_URL="https://${PUBLIC_ENDPOINT}:30443"
DASHBOARD_URL="http://${PUBLIC_ENDPOINT}:30444"

log "Central/broker deploy — endpoint ${PUBLIC_ENDPOINT}, image ${REGISTRY}/federation-autoscaler-broker:${TAG}"

# 1. Federation CA (kept on this host only).
gen_ca "$CA_DIR"
printf '%s\n' "$BROKER_URL" > "$CA_DIR/broker-url"

# 2. Broker server cert — SANs cover the public endpoint + the in-cluster Service
#    FQDNs (mirrors config/broker/certmanager.yaml). ca.crt in the Secret is the
#    federation CA, which is ALSO what the broker uses to verify agent client certs.
log "Signing broker server certificate"
sign_leaf "$CA_DIR" "$CA_DIR/broker-server" "broker.${NAMESPACE}.svc" "serverAuth" \
  "$PUBLIC_ENDPOINT" \
  "broker" "broker.${NAMESPACE}" "broker.${NAMESPACE}.svc" "broker.${NAMESPACE}.svc.cluster.local"

# 3. Namespace + broker-server-cert Secret (before the overlay, which mounts it).
ensure_namespace
apply_tls_secret "broker-server-cert" \
  "$CA_DIR/broker-server.crt" "$CA_DIR/broker-server.key" "$CA_DIR/ca.crt"

# 4. CRDs (cluster-scoped) then the broker overlay (NodePort 30443/30444, no cert-manager).
log "Applying federation CRDs"
kubectl apply -k "${FA_REPO_ROOT}/config/crd"
log "Applying broker overlay"
apply_overlay "${FA_REPO_ROOT}/config/standalone/broker" "broker"

# 5. Wait for the broker to come up.
log "Waiting for the broker Deployment to become Available"
kubectl -n "$NAMESPACE" rollout status deploy/broker --timeout=180s

# ----------------------------------------------------------------------------
# Done
# ----------------------------------------------------------------------------
echo
ok "Broker is up."
cat <<EOF

Broker API (agents dial this):  ${BROKER_URL}
Broker dashboard (browser):     ${DASHBOARD_URL}/

Next: mint one join bundle per participant and send it to that cluster's admin:

  ./central-up.sh join --cluster-id provider-1 --ca-dir ${CA_DIR}
  ./central-up.sh join --cluster-id provider-2 --ca-dir ${CA_DIR}
  ./central-up.sh join --cluster-id consumer-1 --ca-dir ${CA_DIR}

Keep ${CA_DIR}/ (the federation CA private key) private and backed up.
EOF
