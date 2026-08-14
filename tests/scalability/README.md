# Broker Scalability Test Harness

Automated scalability experiment for the federation-autoscaler **Broker** API.
Generates synthetic Consumer and Provider HTTP traffic without deploying real
agents, clusters, controllers, Liqo tunnels, or Cluster Autoscaler instances.

## Scope

### What it tests

| Metric | Description |
|---|---|
| Evaluation latency | `GET /api/v1/nodegroups` round-trip time |
| Advertisement throughput | `POST /api/v1/advertisements` processing rate |
| Heartbeat throughput | `POST /api/v1/heartbeat` processing rate |
| Instruction poll rate | `GET /api/v1/instructions` processing rate |
| Broker CPU/RAM | Resource usage during the test |

### What it does NOT test

Real Consumer/Provider Agent instances, Kubernetes clusters, controllers,
Cluster Autoscaler, Liqo peering, virtual nodes, tunnels, certificate exchange,
reservations, or resource allocation.

### Kubernetes persistence note

The Broker **is** a controller-runtime process. Provider advertisements
create/update `ClusterAdvertisement` CRs in etcd via the Broker's existing
Kubernetes-backed persistence path. The test does not deploy additional
clusters, agents, or controllers, but it **does** exercise the Broker's real
CRD write path. See [Cleanup](#cleanup) for how to remove test-created CRs.

## Traffic Pattern (matches real system)

| Agent type | Endpoint | Method | Interval | Purpose |
|---|---|---|---|---|
| **Provider** | `/api/v1/advertisements` | POST | 30 s | Liveness + resource data (doubles as heartbeat) |
| **Provider** | `/api/v1/instructions` | GET | 5 s | Instruction poll |
| **Consumer** | `/api/v1/heartbeat` | POST | 15 s | Liveness + policy + location |
| **Consumer** | `/api/v1/nodegroups` | GET | configurable | Evaluation (the metric under test) |
| **Consumer** | `/api/v1/instructions` | GET | 5 s | Instruction poll |

> **Note:** `POST /api/v1/heartbeat` is Consumer-only. Providers do NOT call
> this endpoint — their 30 s advertisement POST serves as their liveness signal.
> `POST /api/v1/reservations` is explicitly excluded.

## Prerequisites

1. **Go 1.22+** (the test harness is written in Go, matching the project).
2. **openssl** (for test certificate generation).
3. A **running Broker** instance reachable over HTTPS with mTLS.

## Authentication: mTLS certificates

The Broker enforces `body.clusterId == TLS cert CN` on every request via its
`ClusterIDMiddleware`. Each logical agent **must** present a unique client
certificate whose Common Name (CN) matches the `clusterId` in the request body.

### Generating test certificates

The included `generate-test-certs.sh` script creates a disposable test-only CA
and per-agent client certificates:

```bash
cd tests/scalability
./generate-test-certs.sh \
  --consumers 50 \
  --providers 100 \
  --out-dir certs/run-001

# Creates:
#   certs/run-001/ca.crt, ca.key          — test CA (NOT the production CA)
#   certs/run-001/server.crt, server.key  — server cert (for local test broker)
#   certs/run-001/scaltest-provider-001.{crt,key}  — per-provider certs
#   certs/run-001/scaltest-consumer-001.{crt,key}  — per-consumer certs
```

For a **production Broker**, use `deploy/standalone/central-up.sh join` to mint
per-agent bundles from the real federation CA instead.

### TLS operating modes

| Mode | Flags | When to use |
|---|---|---|
| **Per-agent certs** | `--certs-dir <dir>` | Points to `generate-test-certs.sh` output. Each agent uses its own cert. CA is read from `<dir>/ca.crt`. |
| **Single shared cert** | `--tls-cert ... --tls-key ... --tls-ca ...` | Only valid with ≤1 provider + ≤1 consumer (CN must match body.clusterId). |
| **ServerName override** | above + `--broker-server-name <host>` | When dialing via `kubectl port-forward` to localhost but the server cert's SAN only covers in-cluster DNS names. |

> **Plain HTTP will NOT work** with the production Broker — its API listener is
> HTTPS-only with `RequireAndVerifyClientCert`. The Broker's TLS configuration
> has no insecure-skip-verify option; every connection must present a valid
> client certificate and verify the server certificate against the CA bundle.

## Quick Start

### Build

```bash
cd tests/scalability
go build -o bin/broker-scalability-test .
```

### Generate test certs + smoke test

```bash
# Generate certs for 1 consumer + 1 provider
./generate-test-certs.sh --consumers 1 --providers 1 --out-dir certs/smoke

# Run 60-second smoke test against a locally port-forwarded Broker
# (--broker-server-name overrides TLS verification when the server cert
#  doesn't cover "localhost")
./bin/broker-scalability-test \
  --consumers 1 --providers 1 --duration 60s \
  --certs-dir certs/smoke \
  --broker-url https://localhost:9443 \
  --broker-server-name broker.federation-autoscaler-system.svc \
  --output-dir results/smoke-001
```

### Full 10-minute experiment (50 consumers, 100 providers)

```bash
./generate-test-certs.sh --consumers 50 --providers 100 --out-dir certs/full

./bin/broker-scalability-test \
  --consumers 50 --providers 100 --duration 10m \
  --certs-dir certs/full \
  --consumer-eval-interval 5s \
  --advertisement-interval 30s \
  --heartbeat-interval 15s \
  --instruction-poll-interval 5s \
  --broker-url https://broker.example.com:9443 \
  --output-dir results/full-150-10m
```

## Test Lifecycle

1. Validate parameters and output directory
2. Check Broker reachability (`GET /healthz`)
3. Save full configuration to `configuration.json`
4. Start Broker CPU/RAM monitoring
5. **Warm-up phase:** Start all provider advertisement loops. Wait until every
   provider has successfully advertised at least once (HTTP 200). This ensures
   `GET /api/v1/nodegroups` returns a non-empty provider list.
6. Start all consumer heartbeat loops. Wait until every consumer has heartbeated
   once.
7. **Measurement phase:** Start consumer evaluation (`GET /api/v1/nodegroups`)
   traffic.
8. Run for the configured duration.
9. Gracefully stop all generators and monitoring.
10. Save raw metrics and generate summary.

## Warm-up Phase

Provider advertisements must be registered before consumer evaluations begin.
Otherwise `GET /api/v1/nodegroups` returns an empty list and the benchmark
does not reflect normal behavior.

The warm-up blocks until every provider has received at least one HTTP 200
from `POST /api/v1/advertisements`. A configurable `--warmup-timeout`
(default: 60 s) caps how long the warm-up waits.

## Scale Configurations

| Scale | Consumers | Providers | Total | Duration |
|---|---|---|---|---|
| Smoke | 1 | 1 | 2 | 60 s |
| Small | 5 | 5 | 10 | 2 min |
| Medium | 12 | 13 | 25 | 5 min |
| Large | 25 | 25 | 50 | 10 min |
| XL | 50 | 50 | 100 | 10 min |
| XXL | 75 | 75 | 150 | 10 min |
| XXXL | 100 | 100 | 200 | 10 min |

## CLI Flags

| Flag | Default | Description |
|---|---|---|
| `--consumers` | 1 | Number of logical consumers |
| `--providers` | 1 | Number of logical providers |
| `--duration` | 60s | Total test duration (measurement phase) |
| `--broker-url` | — (required) | Broker REST API base URL (https only), e.g. `https://localhost:9443` |
| `--broker-server-name` | — | TLS ServerName override (for port-forward / SAN mismatch) |
| `--certs-dir` | — | Directory with per-agent certs from generate-test-certs.sh |
| `--tls-cert` | — | Single shared client cert (alternative to --certs-dir; ≤1 provider + ≤1 consumer) |
| `--tls-key` | — | Single shared client key |
| `--tls-ca` | — | CA certificate for server verification (required with --tls-cert) |
| `--output-dir` | `results/<timestamp>` | Output directory |
| `--consumer-eval-interval` | 5s | Interval between consumer evaluations |
| `--advertisement-interval` | 30s | Provider advertisement interval |
| `--heartbeat-interval` | 15s | Consumer heartbeat interval |
| `--instruction-poll-interval` | 5s | Instruction poll interval |
| `--monitor-interval` | 5s | Broker resource monitoring interval |
| `--instruction-poll` | true | Exercise `GET /api/v1/instructions` from every agent |
| `--monitor-mode` | none | `none`, `k8s`, `docker`, `process` |
| `--broker-container` | — | Docker container name (docker mode) |
| `--broker-pod` | — | Exact pod name, overrides --broker-pod-label (k8s mode) |
| `--broker-pod-label` | `app.kubernetes.io/component=broker` | Label selector for `kubectl top pod` (k8s mode) |
| `--broker-namespace` | `federation-autoscaler-system` | K8s namespace (k8s mode + cleanup) |
| `--broker-pid` | 0 | PID of a local `broker` process (process mode) |
| `--kubeconfig` | — | Kubeconfig path for kubectl (k8s mode + cleanup) |
| `--request-timeout` | 10s | Per-HTTP-attempt timeout |
| `--client-max-retries` | 0 | Additional retry attempts on transient failures (0 = single attempt for clean latency; 3 mirrors production) |
| `--warmup-timeout` | 60s | Max time to wait for warm-up |
| `--provider-cpu` | `16` | Synthetic CPU quantity each provider advertises |
| `--provider-memory` | `32Gi` | Synthetic memory quantity each provider advertises |
| `--seed` | current time | Deterministic seed for synthetic data generation |

## Output Files

| File | Format | Contents |
|---|---|---|
| `configuration.json` | JSON | Full experiment configuration |
| `raw_evaluations.csv` | CSV | Per-request evaluation (GET /nodegroups) metrics |
| `raw_provider_requests.csv` | CSV | Per-request provider (POST /advertisements + GET /instructions) metrics |
| `raw_consumer_requests.csv` | CSV | Per-request consumer (POST /heartbeat + GET /instructions) metrics |
| `broker_resource_usage.csv` | CSV | Periodic CPU/RAM samples |
| `summary.json` | JSON | Machine-readable summary |
| `summary.md` | Markdown | Human-readable summary |

## Cleanup

All test-created `ClusterAdvertisement` CRs are named after their advertising
cluster ID, which always starts with the prefix `scaltest-` (e.g.
`scaltest-provider-001`). The cleanup subcommand matches CRs by this **name
prefix** — no labels are involved because the Broker's `upsertClusterAdvertisement()`
sets only `.Spec`, never `.ObjectMeta.Labels`.

```bash
# Dry-run: list CRs that would be deleted
./bin/broker-scalability-test cleanup \
  --broker-namespace federation-autoscaler-system

# Actually delete them
./bin/broker-scalability-test cleanup \
  --broker-namespace federation-autoscaler-system --yes

# Or manually:
kubectl get clusteradvertisements.broker.federation-autoscaler.io \
  -n federation-autoscaler-system -o name \
  | grep '/scaltest-' \
  | xargs kubectl delete -n federation-autoscaler-system
```

The cleanup **never** deletes CRs whose name does not start with `scaltest-`.

## Ctrl+C / Signal Handling

Press Ctrl+C during the test. The harness:
1. Stops all load generators gracefully (context cancellation)
2. Stops the resource monitor
3. Saves all data collected up to the interruption point
4. Generates the summary with actual elapsed duration

## Limitations

1. **mTLS required.** The production Broker requires `RequireAndVerifyClientCert`.
   Use `generate-test-certs.sh` for test certs or production bundles for real certs.
2. **CRD writes.** Provider advertisements create real `ClusterAdvertisement` CRs.
   Use `cleanup` after testing or use a disposable cluster.
3. **Instruction poll returns empty.** Without controllers creating instructions,
   `GET /api/v1/instructions` returns `[]`. The endpoint is still exercised to
   measure request processing overhead.
4. **No reservations.** `POST /api/v1/reservations` is not called by design.
5. **Resource monitoring** requires access to the Broker process. Use
   `--monitor-mode none` if unavailable.
