#!/usr/bin/env bash
#
# collect-run.sh — pull every component log across the federation and merge
# them into one chronologically ordered file.
#
# Why this exists: a single scale-up is written down by four different VMs.
# Cluster Autoscaler's decision is logged on the consumer, the phase machine
# on the central broker, `liqoctl generate peering-user` on a provider, and
# `liqoctl peer` back on the consumer. No single log tells the story, and
# matching four files by eye is where measurement projects die.
#
# The merged file is what deploy/bench/README (and
# federation-autoscaler-scaling-measurement.md) treat as the run artefact:
# one line per event, sorted by time, tagged with which cluster and component
# emitted it.
#
# Usage:
#   collect-run.sh --out <dir> [--since <dur> | --since-time <RFC3339>]
#                  [--kube-dir <dir>] [--namespace <ns>]
#
#   --out         Directory to write into (created; must not already hold a run).
#   --since       Relative window passed to `kubectl logs --since` (default 20m).
#   --since-time  Absolute RFC3339 start; overrides --since. run-benchmark.sh
#                 passes the exact instant the iteration began, so the merged
#                 file contains that iteration and nothing else.
#   --kube-dir    Where the per-cluster kubeconfigs live (default ~/.kube).
#                 The Ansible deploy writes them as <cluster_id>.yaml, and the
#                 file name is what tags each line.
#   --namespace   Namespace holding the components (default
#                 federation-autoscaler-system).
#
# Clusters and components are DISCOVERED, not configured: every kubeconfig in
# --kube-dir is probed for each known Deployment. That keeps the script
# correct for 1+1+2 and for any other topology, and it silently skips the
# mock cluster, which runs none of them.

set -euo pipefail

# -----------------------------------------------------------------------------
# Defaults
# -----------------------------------------------------------------------------
OUT_DIR=""
SINCE="20m"
SINCE_TIME=""
KUBE_DIR="${KUBE_DIR:-$HOME/.kube}"
NAMESPACE="${NAMESPACE:-federation-autoscaler-system}"

# The Deployments worth reading. `agent` exists on consumers AND providers;
# `broker` marks the central cluster; `grpc-server` and `cluster-autoscaler`
# mark a consumer. Anything absent is skipped without complaint.
COMPONENTS=(broker agent grpc-server cluster-autoscaler)

usage() { sed -n '2,40p' "$0" | sed 's|^# \{0,1\}||'; exit "${1:-0}"; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --out)        OUT_DIR="$2"; shift 2 ;;
    --since)      SINCE="$2"; shift 2 ;;
    --since-time) SINCE_TIME="$2"; shift 2 ;;
    --kube-dir)   KUBE_DIR="$2"; shift 2 ;;
    --namespace)  NAMESPACE="$2"; shift 2 ;;
    -h|--help)    usage 0 ;;
    *) echo "collect-run.sh: unknown flag $1" >&2; usage 1 ;;
  esac
done

[[ -n "$OUT_DIR" ]] || { echo "collect-run.sh: --out is required" >&2; exit 1; }
command -v kubectl >/dev/null || { echo "collect-run.sh: kubectl not on PATH" >&2; exit 1; }

mkdir -p "$OUT_DIR/raw" "$OUT_DIR/crs"

if [[ -n "$SINCE_TIME" ]]; then
  SINCE_ARGS=(--since-time="$SINCE_TIME")
else
  SINCE_ARGS=(--since="$SINCE")
fi

# -----------------------------------------------------------------------------
# Timestamp normalisation
# -----------------------------------------------------------------------------
# `kubectl logs --timestamps` emits Go's RFC3339Nano, which DROPS trailing
# zeros: ".12Z" and ".123456789Z" both occur. Sorting those lexicographically
# is wrong — 'Z' (0x5A) sorts above any digit, so ".12Z" lands after ".123Z"
# even though it is earlier. Pad the fraction to a fixed 9 digits first, and
# the merge is a plain `sort` again. The padded form is also what makes two
# lines from different VMs directly comparable by eye.
pad_and_tag() {
  local tag="$1"
  awk -v tag="$tag" '
    {
      ts = $1
      rest = substr($0, length(ts) + 2)
      if (match(ts, /\.[0-9]+Z$/)) {
        base = substr(ts, 1, RSTART - 1)
        frac = substr(ts, RSTART + 1, RLENGTH - 2)
      } else {
        base = substr(ts, 1, length(ts) - 1)
        frac = ""
      }
      while (length(frac) < 9) frac = frac "0"
      printf "%s.%sZ %-28s %s\n", base, frac, tag, rest
    }'
}

# -----------------------------------------------------------------------------
# Collect
# -----------------------------------------------------------------------------
central_kubeconfig=""
consumer_kubeconfigs=()
found_any=0

shopt -s nullglob
for kubeconfig in "$KUBE_DIR"/*.yaml; do
  cluster="$(basename "$kubeconfig" .yaml)"

  for component in "${COMPONENTS[@]}"; do
    # Probe before reading: a missing Deployment is the normal case (a
    # provider has no grpc-server), not an error worth printing.
    kubectl --kubeconfig "$kubeconfig" -n "$NAMESPACE" \
      get deploy "$component" >/dev/null 2>&1 || continue

    raw="$OUT_DIR/raw/${cluster}-${component}.log"
    if ! kubectl --kubeconfig "$kubeconfig" -n "$NAMESPACE" \
        logs "deploy/$component" --timestamps --tail=-1 "${SINCE_ARGS[@]}" \
        >"$raw" 2>/dev/null; then
      echo "  ! ${cluster}/${component}: log read failed (pod restarted?)" >&2
      continue
    fi

    found_any=1
    echo "  + ${cluster}/${component} ($(wc -l <"$raw") lines)"

    # Remember roles for the CR snapshots below.
    [[ "$component" == "broker" ]]      && central_kubeconfig="$kubeconfig"
    [[ "$component" == "grpc-server" ]] && consumer_kubeconfigs+=("$kubeconfig")
  done
done
shopt -u nullglob

if [[ "$found_any" -eq 0 ]]; then
  echo "collect-run.sh: no components found under $KUBE_DIR — wrong --kube-dir?" >&2
  exit 1
fi

# -----------------------------------------------------------------------------
# Merge
# -----------------------------------------------------------------------------
: >"$OUT_DIR/merged.log"
shopt -s nullglob
for raw in "$OUT_DIR"/raw/*.log; do
  tag="$(basename "$raw" .log)"
  pad_and_tag "$tag" <"$raw" >>"$OUT_DIR/merged.log"
done
shopt -u nullglob

sort -o "$OUT_DIR/merged.log" "$OUT_DIR/merged.log"

# The timeline: only the lines a phase breakdown is built from. "timing" is
# the message the instrumented call sites emit (broker phase changes, agent
# instruction receive/handle, peer/unpeer internal steps); the rest are
# Cluster Autoscaler's own scale decisions, which bound the phases at both
# ends and come from an upstream binary we do not instrument.
grep -E 'timing|Scale-up:|Scale-down:|scale down|[Rr]emoving empty node|[Dd]eleted node' \
  "$OUT_DIR/merged.log" >"$OUT_DIR/timeline.log" || true

# -----------------------------------------------------------------------------
# CR snapshots
# -----------------------------------------------------------------------------
# Taken AFTER the logs so a run that ends mid-flight still shows its final
# object state. These are the second-resolution cross-check on the
# millisecond log timeline: instruction issuedAt / lastDeliveredAt /
# lastUpdateTime, and the attempts counter that reveals a redelivery.
if [[ -n "$central_kubeconfig" ]]; then
  kubectl --kubeconfig "$central_kubeconfig" -n "$NAMESPACE" \
    get reservations.broker.federation-autoscaler.io -o json \
    >"$OUT_DIR/crs/reservations.json" 2>/dev/null || true
  kubectl --kubeconfig "$central_kubeconfig" -n "$NAMESPACE" \
    get providerinstructions.autoscaling.federation-autoscaler.io -o json \
    >"$OUT_DIR/crs/providerinstructions.json" 2>/dev/null || true
  kubectl --kubeconfig "$central_kubeconfig" -n "$NAMESPACE" \
    get reservationinstructions.autoscaling.federation-autoscaler.io -o json \
    >"$OUT_DIR/crs/reservationinstructions.json" 2>/dev/null || true
  kubectl --kubeconfig "$central_kubeconfig" -n "$NAMESPACE" \
    get clusteradvertisements.broker.federation-autoscaler.io -o json \
    >"$OUT_DIR/crs/clusteradvertisements.json" 2>/dev/null || true
fi

for kubeconfig in "${consumer_kubeconfigs[@]}"; do
  cluster="$(basename "$kubeconfig" .yaml)"
  kubectl --kubeconfig "$kubeconfig" get nodes -o json \
    >"$OUT_DIR/crs/nodes-${cluster}.json" 2>/dev/null || true
  kubectl --kubeconfig "$kubeconfig" -n "$NAMESPACE" \
    get virtualnodestates.autoscaling.federation-autoscaler.io -o json \
    >"$OUT_DIR/crs/virtualnodestates-${cluster}.json" 2>/dev/null || true
done

echo "collected -> $OUT_DIR"
echo "  merged.log   $(wc -l <"$OUT_DIR/merged.log") lines"
echo "  timeline.log $(wc -l <"$OUT_DIR/timeline.log") lines"
