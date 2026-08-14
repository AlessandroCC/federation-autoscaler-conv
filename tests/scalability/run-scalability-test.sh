#!/usr/bin/env bash
# ============================================================
#  run-scalability-test.sh — Fully automated Broker scalability test
# ============================================================
#  Usage:
#    bash tests/scalability/run-scalability-test.sh
#
#  Prerequisites:
#    - Docker Desktop running
#    - Go 1.22+, kind, openssl, kubectl on PATH
#
#  Configuration:
#    Edit tests/scalability/test.env before running.
#
#  What this script does (in order):
#    1. Sources test.env for configuration
#    2. Checks prerequisites
#    3. Builds the Broker and the scalability-test binaries
#    4. Generates per-agent mTLS certificates
#    5. Creates a Kind cluster (or reuses an existing one)
#    6. Installs CRDs + creates the Broker namespace
#    7. Starts the Broker as a local process (out-of-cluster)
#    8. Waits for the Broker to become ready
#    9. Runs the scalability test
#   10. Stops the Broker and cleans up test CRs
#   11. Optionally tears down the Kind cluster
# ============================================================
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

# -----------------------------------------------------------
#  Source configuration
# -----------------------------------------------------------
if [[ ! -f "$SCRIPT_DIR/test.env" ]]; then
  echo "ERROR: $SCRIPT_DIR/test.env not found. Copy test.env.example and edit it." >&2
  exit 1
fi
# shellcheck disable=SC1091
source "$SCRIPT_DIR/test.env"

# -----------------------------------------------------------
#  Derived paths
# -----------------------------------------------------------
CERTS_DIR="$SCRIPT_DIR/certs/${KIND_CLUSTER_NAME}"
BROKER_BIN="$REPO_ROOT/bin/broker"
TEST_BIN="$REPO_ROOT/bin/broker-scalability-test"
BROKER_PID=""
BROKER_LOG=""
KIND_KUBECONFIG=""

if [[ "$(uname -s)" == MINGW* || "$(uname -s)" == MSYS* ]]; then
  BROKER_BIN="${BROKER_BIN}.exe"
  TEST_BIN="${TEST_BIN}.exe"
fi

# -----------------------------------------------------------
#  Output helpers
# -----------------------------------------------------------
_green()  { printf '\033[0;32m%s\033[0m\n' "$*"; }
_yellow() { printf '\033[0;33m%s\033[0m\n' "$*"; }
_red()    { printf '\033[0;31m%s\033[0m\n' "$*"; }
step()    { _green "==> $*"; }
warn()    { _yellow "    WARN: $*"; }
die()     { _red "ERROR: $*" >&2; exit 1; }

# -----------------------------------------------------------
#  Cleanup trap — always runs on exit
# -----------------------------------------------------------
cleanup() {
  local exit_code=$?
  echo ""
  if [[ -n "$BROKER_PID" ]] && kill -0 "$BROKER_PID" 2>/dev/null; then
    step "Stopping Broker (PID $BROKER_PID)..."
    kill "$BROKER_PID" 2>/dev/null || true
    wait "$BROKER_PID" 2>/dev/null || true
  fi

  if [[ "${KEEP_CLUSTER:-true}" != "true" ]] && kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
    step "Deleting Kind cluster '${KIND_CLUSTER_NAME}'..."
    kind delete cluster --name "$KIND_CLUSTER_NAME" 2>/dev/null || true
  fi

  if [[ $exit_code -ne 0 ]]; then
    echo ""
    _red "Test failed (exit $exit_code)."
    if [[ -n "$BROKER_LOG" && -f "$BROKER_LOG" ]]; then
      _red "Broker log: $BROKER_LOG"
    fi
  fi
}
trap cleanup EXIT

# -----------------------------------------------------------
#  1. Check prerequisites
# -----------------------------------------------------------
step "Checking prerequisites..."

for cmd in go kind kubectl openssl docker; do
  command -v "$cmd" >/dev/null 2>&1 || die "'$cmd' not found on PATH"
done

docker info >/dev/null 2>&1 || die "Docker is not running. Start Docker Desktop first."

go_version=$(go version | sed 's/.*go1\.\([0-9]*\).*/\1/')
if [[ "$go_version" -lt 22 ]]; then
  die "Go 1.22+ required (found go1.${go_version})"
fi

# -----------------------------------------------------------
#  2. Build binaries
# -----------------------------------------------------------
step "Building Broker binary..."
(cd "$REPO_ROOT" && go build -o "$BROKER_BIN" ./cmd/broker/) || die "Broker build failed"

step "Building scalability test binary..."
(cd "$REPO_ROOT" && go build -o "$TEST_BIN" ./tests/scalability/) || die "Test binary build failed"

# -----------------------------------------------------------
#  3. Generate certificates
# -----------------------------------------------------------
TOTAL_AGENTS=$(( CONSUMERS + PROVIDERS ))
if [[ -d "$CERTS_DIR" && -f "$CERTS_DIR/ca.crt" ]]; then
  existing_certs=$(find "$CERTS_DIR" -name 'scaltest-*.crt' 2>/dev/null | wc -l)
  if [[ "$existing_certs" -ge "$TOTAL_AGENTS" ]]; then
    step "Reusing existing certificates in $CERTS_DIR ($existing_certs certs found)"
  else
    step "Regenerating certificates (need $TOTAL_AGENTS, found $existing_certs)..."
    rm -rf "$CERTS_DIR"
    bash "$SCRIPT_DIR/generate-test-certs.sh" \
      --consumers "$CONSUMERS" --providers "$PROVIDERS" --out-dir "$CERTS_DIR"
  fi
else
  step "Generating $TOTAL_AGENTS certificates..."
  bash "$SCRIPT_DIR/generate-test-certs.sh" \
    --consumers "$CONSUMERS" --providers "$PROVIDERS" --out-dir "$CERTS_DIR"
fi

# -----------------------------------------------------------
#  4. Create/reuse Kind cluster
# -----------------------------------------------------------
if kind get clusters 2>/dev/null | grep -q "^${KIND_CLUSTER_NAME}$"; then
  step "Reusing existing Kind cluster '${KIND_CLUSTER_NAME}'"
else
  step "Creating Kind cluster '${KIND_CLUSTER_NAME}'..."
  kind create cluster --name "$KIND_CLUSTER_NAME" --wait 60s
fi

KIND_KUBECONFIG="$SCRIPT_DIR/.kubeconfig-${KIND_CLUSTER_NAME}"
kind get kubeconfig --name "$KIND_CLUSTER_NAME" > "$KIND_KUBECONFIG"
export KUBECONFIG="$KIND_KUBECONFIG"

# -----------------------------------------------------------
#  5. Install CRDs + create namespace
# -----------------------------------------------------------
step "Installing CRDs..."
kubectl apply -k "$REPO_ROOT/config/crd" --server-side 2>&1 | grep -v "^$" || true

if ! kubectl get namespace "$BROKER_NAMESPACE" >/dev/null 2>&1; then
  step "Creating namespace '$BROKER_NAMESPACE'..."
  kubectl create namespace "$BROKER_NAMESPACE"
else
  step "Namespace '$BROKER_NAMESPACE' already exists"
fi

# -----------------------------------------------------------
#  6. Start Broker
# -----------------------------------------------------------
if [[ -z "${OUTPUT_DIR:-}" ]]; then
  OUTPUT_DIR="$SCRIPT_DIR/results/$(date -u +%Y%m%dT%H%M%SZ)"
fi
mkdir -p "$OUTPUT_DIR"

BROKER_LOG="$OUTPUT_DIR/broker.log"
HEALTH_PORT=8081

# Kill any leftover Broker from a previous run
for port in "$BROKER_PORT" "$HEALTH_PORT"; do
  if pid=$(netstat -ano 2>/dev/null | grep "LISTENING" | grep ":${port} " | awk '{print $NF}' | head -1) && [[ -n "$pid" ]]; then
    warn "Port $port already in use by PID $pid — killing it"
    taskkill //F //PID "$pid" >/dev/null 2>&1 || kill "$pid" 2>/dev/null || true
    sleep 1
  fi
done

step "Starting Broker on :${BROKER_PORT}..."
"$BROKER_BIN" \
  --api-bind-address=":${BROKER_PORT}" \
  --api-tls-cert-file="$CERTS_DIR/server.crt" \
  --api-tls-key-file="$CERTS_DIR/server.key" \
  --api-client-ca-file="$CERTS_DIR/ca.crt" \
  --namespace="$BROKER_NAMESPACE" \
  --health-probe-bind-address=":8081" \
  --metrics-bind-address="0" \
  --dashboard-bind-address="" \
  --reservation-timeout="2m" \
  --zap-time-encoding=rfc3339nano \
  > "$BROKER_LOG" 2>&1 &
BROKER_PID=$!
echo "$BROKER_PID" > "$OUTPUT_DIR/.broker.pid"

# -----------------------------------------------------------
#  7. Wait for Broker to become ready
# -----------------------------------------------------------
step "Waiting for Broker to become ready..."
MAX_WAIT=30
for i in $(seq 1 "$MAX_WAIT"); do
  if ! kill -0 "$BROKER_PID" 2>/dev/null; then
    echo ""
    _red "Broker process exited prematurely. Log:"
    tail -20 "$BROKER_LOG"
    die "Broker failed to start"
  fi
  if curl -sf http://localhost:8081/readyz >/dev/null 2>&1; then
    _green "    Broker ready after ${i}s (PID $BROKER_PID)"
    break
  fi
  if [[ $i -eq $MAX_WAIT ]]; then
    echo ""
    _red "Broker did not become ready within ${MAX_WAIT}s. Last log lines:"
    tail -20 "$BROKER_LOG"
    die "Broker readiness timeout"
  fi
  printf "."
  sleep 1
done

# -----------------------------------------------------------
#  8. Build test flags
# -----------------------------------------------------------
TEST_FLAGS=(
  --consumers "$CONSUMERS"
  --providers "$PROVIDERS"
  --duration "$DURATION"
  --broker-url "https://localhost:${BROKER_PORT}"
  --broker-server-name "localhost"
  --certs-dir "$CERTS_DIR"
  --output-dir "$OUTPUT_DIR"
  --advertisement-interval "$ADVERTISEMENT_INTERVAL"
  --heartbeat-interval "$HEARTBEAT_INTERVAL"
  --consumer-eval-interval "$EVAL_INTERVAL"
  --instruction-poll-interval "$INSTRUCTION_POLL_INTERVAL"
  --instruction-poll="$INSTRUCTION_POLL"
  --request-timeout "$REQUEST_TIMEOUT"
  --warmup-timeout "$WARMUP_TIMEOUT"
  --client-max-retries "$CLIENT_MAX_RETRIES"
  --provider-cpu "$PROVIDER_CPU"
  --provider-memory "$PROVIDER_MEMORY"
  --monitor-interval "$MONITOR_INTERVAL"
)

if [[ "$MONITOR_MODE" == "process" ]]; then
  TEST_FLAGS+=(--monitor-mode process --broker-pid "$BROKER_PID")
elif [[ "$MONITOR_MODE" == "k8s" ]]; then
  TEST_FLAGS+=(--monitor-mode k8s --kubeconfig "$KIND_KUBECONFIG"
               --broker-namespace "$BROKER_NAMESPACE")
elif [[ "$MONITOR_MODE" == "docker" ]]; then
  TEST_FLAGS+=(--monitor-mode docker)
else
  TEST_FLAGS+=(--monitor-mode none)
fi

# -----------------------------------------------------------
#  9. Run the scalability test
# -----------------------------------------------------------
echo ""
step "Running scalability test: ${CONSUMERS}C + ${PROVIDERS}P × ${DURATION}"
echo "    Output: $OUTPUT_DIR"
echo ""

"$TEST_BIN" "${TEST_FLAGS[@]}" 2>&1 | tee "$OUTPUT_DIR/test.log"
TEST_EXIT=${PIPESTATUS[0]}

# -----------------------------------------------------------
#  10. Stop Broker + clean up test CRs
# -----------------------------------------------------------
step "Stopping Broker..."
if kill -0 "$BROKER_PID" 2>/dev/null; then
  kill "$BROKER_PID" 2>/dev/null || true
  wait "$BROKER_PID" 2>/dev/null || true
fi
BROKER_PID=""

step "Cleaning up test ClusterAdvertisements..."
"$TEST_BIN" cleanup --namespace "$BROKER_NAMESPACE" --kubeconfig "$KIND_KUBECONFIG" --yes 2>&1 || true

# -----------------------------------------------------------
#  11. Summary
# -----------------------------------------------------------
echo ""
echo "============================================================"
if [[ $TEST_EXIT -eq 0 ]]; then
  _green "  TEST COMPLETED SUCCESSFULLY"
else
  _red   "  TEST FAILED (exit $TEST_EXIT)"
fi
echo "============================================================"
echo ""
echo "  Results:  $OUTPUT_DIR"
echo "  Files:"
for f in "$OUTPUT_DIR"/*.{json,csv,md,log}; do
  [[ -f "$f" ]] && echo "    - $(basename "$f")"
done
echo ""
if [[ "${KEEP_CLUSTER:-true}" == "true" ]]; then
  echo "  Kind cluster '${KIND_CLUSTER_NAME}' kept for re-runs."
  echo "  To delete: kind delete cluster --name ${KIND_CLUSTER_NAME}"
fi
echo ""

exit "$TEST_EXIT"
