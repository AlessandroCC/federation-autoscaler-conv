#!/usr/bin/env bash
#
# run-benchmark.sh — drive N scale-up / scale-down cycles and collect one
# merged log per cycle.
#
# The measurement is deliberately end-to-end and hands-off: apply the burst
# workload, wait until every replica is Running (which can only happen once
# borrowed capacity has materialised), delete it, wait until the borrowed
# node is gone and the reservation released. Wall-clock for each half is
# recorded, and collect-run.sh then pulls the four VMs' logs for that exact
# window so the halves can be split into phases afterwards.
#
# Two regimes, because they are genuinely different systems:
#
#   --mode cold   Every iteration starts with no peering to the provider, so
#                 each scale-up pays the full `liqoctl peer` cost (40-90 s).
#                 This is what an idle federation does.
#
#   --mode warm   A holder Deployment keeps one borrowed chunk alive for the
#                 whole session, so the (consumer, provider) peering is never
#                 torn down and the broker's Pending fast-path skips the
#                 credential round-trip entirely. Measures what a federation
#                 under sustained use does.
#
# Usage:
#   run-benchmark.sh [--runs N] [--mode cold|warm] [--consumer <cluster-id>]
#                    [--workload <path>] [--out <dir>]
#                    [--up-timeout S] [--down-timeout S] [--settle S]
#
# Defaults: 10 runs, cold, auto-detected consumer, the burst workload from
# deploy/ansible/samples, results under deploy/bench/results/<stamp>.
#
# Requires: kubectl, the per-cluster kubeconfigs in ~/.kube (written by the
# Ansible deploy), and a federation that 03-verify.yaml passes on.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# -----------------------------------------------------------------------------
# Defaults
# -----------------------------------------------------------------------------
RUNS=10
MODE="cold"
CONSUMER=""
WORKLOAD="$REPO_ROOT/deploy/ansible/samples/burst-workload.yaml"
WORKLOAD_NS="default"
OUT_DIR=""
KUBE_DIR="${KUBE_DIR:-$HOME/.kube}"
NAMESPACE="${NAMESPACE:-federation-autoscaler-system}"
UP_TIMEOUT=300
DOWN_TIMEOUT=300
SETTLE=20
HOLD_REPLICAS=2

usage() { sed -n '2,36p' "$0" | sed 's|^# \{0,1\}||'; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --runs)         RUNS="$2"; shift 2 ;;
    --mode)         MODE="$2"; shift 2 ;;
    --consumer)     CONSUMER="$2"; shift 2 ;;
    --workload)     WORKLOAD="$2"; shift 2 ;;
    --out)          OUT_DIR="$2"; shift 2 ;;
    --up-timeout)   UP_TIMEOUT="$2"; shift 2 ;;
    --down-timeout) DOWN_TIMEOUT="$2"; shift 2 ;;
    --settle)       SETTLE="$2"; shift 2 ;;
    --hold-replicas) HOLD_REPLICAS="$2"; shift 2 ;;
    -h|--help)      usage 0 ;;
    *) echo "run-benchmark.sh: unknown flag $1" >&2; usage 1 ;;
  esac
done

case "$MODE" in cold|warm) ;; *) echo "--mode must be cold or warm" >&2; exit 1 ;; esac
command -v kubectl >/dev/null || { echo "run-benchmark.sh: kubectl not on PATH" >&2; exit 1; }
[[ -f "$WORKLOAD" ]] || { echo "run-benchmark.sh: no workload at $WORKLOAD" >&2; exit 1; }

# -----------------------------------------------------------------------------
# Locate the consumer cluster
# -----------------------------------------------------------------------------
# The consumer is the cluster running grpc-server — that is the only role that
# talks to Cluster Autoscaler, so it is where a scale-up is driven from.
if [[ -z "$CONSUMER" ]]; then
  shopt -s nullglob
  for kubeconfig in "$KUBE_DIR"/*.yaml; do
    if kubectl --kubeconfig "$kubeconfig" -n "$NAMESPACE" \
         get deploy grpc-server >/dev/null 2>&1; then
      CONSUMER="$(basename "$kubeconfig" .yaml)"
      break
    fi
  done
  shopt -u nullglob
fi
[[ -n "$CONSUMER" ]] || { echo "run-benchmark.sh: no consumer cluster found in $KUBE_DIR" >&2; exit 1; }

KC="$KUBE_DIR/$CONSUMER.yaml"
[[ -f "$KC" ]] || { echo "run-benchmark.sh: no kubeconfig at $KC" >&2; exit 1; }

if [[ -z "$OUT_DIR" ]]; then
  OUT_DIR="$SCRIPT_DIR/results/$(date -u +%Y%m%dT%H%M%SZ)-$MODE"
fi
mkdir -p "$OUT_DIR"

kc() { kubectl --kubeconfig "$KC" "$@"; }

now_ms()   { date +%s%3N; }
now_rfc()  { date -u +%Y-%m-%dT%H:%M:%SZ; }

# Borrowed capacity is identified by Liqo's own label rather than by node
# name: naming has changed once already (one node per ResourceSlice), the
# label has not. Same key the gRPC server's node template uses.
virtual_nodes() {
  kc get nodes -l liqo.io/type=virtual-node -o name 2>/dev/null | wc -l
}

workload_ready() {
  local want have
  want="$(kc -n "$WORKLOAD_NS" get deploy federation-demo -o jsonpath='{.spec.replicas}' 2>/dev/null || echo 0)"
  have="$(kc -n "$WORKLOAD_NS" get deploy federation-demo -o jsonpath='{.status.readyReplicas}' 2>/dev/null || echo 0)"
  [[ -n "$want" && "$want" != "0" && "${have:-0}" == "$want" ]]
}

# -----------------------------------------------------------------------------
# Warm mode: a holder that keeps the peering alive between iterations
# -----------------------------------------------------------------------------
# One chunk held by a workload CA is not allowed to reclaim. Without it every
# iteration's scale-down would unpeer (it would be the last chunk), and the
# next scale-up would be cold again — silently turning a "warm" run into a
# slower cold one.
HOLDER_NAME="bench-holder"
apply_holder() {
  cat <<EOF | kc -n "$WORKLOAD_NS" apply -f - >/dev/null
apiVersion: apps/v1
kind: Deployment
metadata:
  name: $HOLDER_NAME
  labels: { app: $HOLDER_NAME }
spec:
  replicas: $HOLD_REPLICAS
  selector:
    matchLabels: { app: $HOLDER_NAME }
  template:
    metadata:
      labels: { app: $HOLDER_NAME }
    spec:
      tolerations:
        - key: virtual-node.liqo.io/not-allowed
          operator: Exists
          effect: NoExecute
      containers:
        - name: pause
          image: registry.k8s.io/pause:3.9
          resources:
            requests: { cpu: "1", memory: "1Gi" }
            limits:   { cpu: "1", memory: "1Gi" }
EOF
}

cleanup() {
  if [[ "$MODE" == "warm" ]]; then
    echo "removing holder"
    kc -n "$WORKLOAD_NS" delete deploy "$HOLDER_NAME" --ignore-not-found >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT

# -----------------------------------------------------------------------------
# Wait helpers
# -----------------------------------------------------------------------------
wait_scale_up() {
  local deadline=$(( SECONDS + UP_TIMEOUT ))
  while (( SECONDS < deadline )); do
    if workload_ready && (( $(virtual_nodes) > 0 )); then return 0; fi
    sleep 1
  done
  return 1
}

wait_scale_down() {
  # Down means BOTH: the borrowed node is gone from the consumer, and the
  # broker has no live reservation left. The node can disappear a beat before
  # the reservation reaches Released, and a benchmark that stopped at the
  # first would under-report scale-down.
  local deadline=$(( SECONDS + DOWN_TIMEOUT ))
  local target=0
  [[ "$MODE" == "warm" ]] && target=1   # the holder keeps one node up
  while (( SECONDS < deadline )); do
    if (( $(virtual_nodes) <= target )); then return 0; fi
    sleep 1
  done
  return 1
}

# -----------------------------------------------------------------------------
# Run
# -----------------------------------------------------------------------------
SUMMARY="$OUT_DIR/summary.tsv"
printf 'run\tmode\tstatus\tscale_up_ms\tscale_down_ms\tstarted\n' >"$SUMMARY"

echo "consumer=$CONSUMER mode=$MODE runs=$RUNS out=$OUT_DIR"

# Start from a clean slate so run 1 is not measuring the tail of whatever was
# already on the cluster.
kc -n "$WORKLOAD_NS" delete -f "$WORKLOAD" --ignore-not-found >/dev/null 2>&1 || true
wait_scale_down || echo "warning: cluster still holds borrowed nodes at start"

if [[ "$MODE" == "warm" ]]; then
  echo "applying holder ($HOLD_REPLICAS replicas) and waiting for the peering to come up"
  apply_holder
  deadline=$(( SECONDS + UP_TIMEOUT ))
  while (( SECONDS < deadline )) && (( $(virtual_nodes) < 1 )); do sleep 2; done
  (( $(virtual_nodes) >= 1 )) || { echo "holder never borrowed a node — check capacity" >&2; exit 1; }
  echo "peering warm; borrowed nodes = $(virtual_nodes)"
fi

for (( run = 1; run <= RUNS; run++ )); do
  run_dir="$(printf '%s/run-%03d' "$OUT_DIR" "$run")"
  started_rfc="$(now_rfc)"
  status="ok"

  echo
  echo "=== run $run/$RUNS ($MODE) ==="

  # --- scale up -------------------------------------------------------------
  t0="$(now_ms)"
  kc -n "$WORKLOAD_NS" apply -f "$WORKLOAD" >/dev/null
  if wait_scale_up; then
    t1="$(now_ms)"
    up_ms=$(( t1 - t0 ))
    echo "  scale-up   ${up_ms} ms"
  else
    t1="$(now_ms)"; up_ms=$(( t1 - t0 )); status="up-timeout"
    echo "  scale-up   TIMEOUT after ${up_ms} ms"
  fi

  # --- scale down -----------------------------------------------------------
  t2="$(now_ms)"
  kc -n "$WORKLOAD_NS" delete -f "$WORKLOAD" --ignore-not-found >/dev/null
  if wait_scale_down; then
    t3="$(now_ms)"
    down_ms=$(( t3 - t2 ))
    echo "  scale-down ${down_ms} ms"
  else
    t3="$(now_ms)"; down_ms=$(( t3 - t2 ))
    status="${status}/down-timeout"
    echo "  scale-down TIMEOUT after ${down_ms} ms"
  fi

  # --- collect --------------------------------------------------------------
  # Windowed to this iteration only, so merged.log reads as one scale-up
  # followed by one scale-down and nothing else.
  "$SCRIPT_DIR/collect-run.sh" \
    --out "$run_dir" \
    --since-time "$started_rfc" \
    --kube-dir "$KUBE_DIR" \
    --namespace "$NAMESPACE" || echo "  ! collection failed"

  printf '%d\t%s\t%s\t%d\t%d\t%s\n' \
    "$run" "$MODE" "$status" "$up_ms" "$down_ms" "$started_rfc" >>"$SUMMARY"

  # Let controllers finish their tail work (cleanup instructions, chunk
  # accounting) before the next iteration perturbs the same objects.
  sleep "$SETTLE"
done

echo
echo "done — $SUMMARY"
column -t "$SUMMARY"
echo
echo "Discard run 1 (image pulls) and take the median of the rest."
