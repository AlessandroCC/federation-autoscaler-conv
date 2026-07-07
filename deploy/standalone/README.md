# Standalone per-cluster deployment

Deploy federation-autoscaler onto **separate, already-running Kubernetes
clusters** — one script per role, each run by that cluster's own admin. No shared
control host, no Ansible, no cert-manager. (For the all-in-one demo on fresh VMs,
use `deploy/ansible/scripts/demo-up.sh` instead.)

Roles: **central** (broker) · **provider** (donates capacity) · **consumer**
(borrows capacity) · **mock** (optional carbon/geo services for eco & latency).

**The scripts** (run each on its own cluster's admin machine):

| Script | What it does |
| --- | --- |
| `central-up.sh` | Deploys the broker + creates the federation CA. `central-up.sh join --cluster-id <id>` mints a join bundle for one participant. |
| `mock-up.sh` | Deploys the mock carbon/geo services (only needed for the eco & latency policies). |
| `provider-up.sh` | Joins a provider cluster — installs Liqo + the provider agent from its bundle. |
| `consumer-up.sh` | Joins the consumer cluster — installs Liqo + the agent, gRPC server, Cluster Autoscaler, and Liqo dashboard from its bundle. |
| `<role>-down.sh` | Tears the matching role back down (removes what it deployed; never touches the node). |
| `common.sh` | Shared helpers, sourced by the others — not run directly. |

---

## 1. Before you start

**All you need per cluster:** a running Kubernetes cluster + a kubeconfig that
reaches it, on an Ubuntu machine with `sudo` and outbound internet.

**The scripts install any missing CLI tools for you** (like the Ansible demo
does) — you do **not** need to pre-install them. Each installs what it needs:

| Script | Installs if missing |
|---|---|
| `central-up.sh` | `kubectl`, `openssl`, `tar` |
| `provider-up.sh` | `kubectl`, `tar`, `liqoctl` |
| `consumer-up.sh` | `kubectl`, `openssl`, `tar`, `liqoctl`, `helm`, `git` |
| `mock-up.sh` | `kubectl` |

(`kubectl` v1.32.5 and `liqoctl` v1.1.2 are fetched to `/usr/local/bin`; `helm`
via the official installer; `openssl`/`tar`/`git` via `apt`. Override versions
with `KUBECTL_VERSION` / `LIQOCTL_VERSION` env vars.)

**Coordinate up front (can't be auto-checked):**
- Each cluster needs a **unique, DNS-safe `--cluster-id`** (e.g. `provider-1`).
- **Liqo networking:** just omit `--pod-cidr` / `--service-cidr` — `liqoctl` reads
  each cluster's real pod/service CIDRs itself. Only pass them if auto-detection
  fails, and then they must match the cluster's **actual** CIDRs (not arbitrary
  values). Overlapping CIDRs across clusters are fine — Liqo remaps them at peering.

**Open these ports** between the machines/clusters:

| Port | Proto | Between | For |
|---|---|---|---|
| 30443 | TCP | providers+consumer → central | broker API (mTLS) |
| 30081 / 30080 | TCP | providers+consumer → mock | carbon / coordinates |
| 30000–32767 | UDP | consumer ↔ providers | Liqo WireGuard (peering) |
| 30444 / 30445 / 80 | TCP | your browser → clusters | broker dashboard / consoles / Liqo dashboard |

Set the image once and reuse it: `--registry docker.io/kazem26 --tag v0.1.5`
(defaults: registry `docker.io/kazem26`, tag `latest`).

---

## 2. Deploy — order matters: central → mock → providers → consumer

### Step 1 — Central admin: bring up the broker, then mint a bundle per cluster
```bash
./central-up.sh --public-endpoint <central node IP> --tag <image tag> \
                --kubeconfig <kubeconfig file path>
# prints the broker URL + dashboard. Keep ./fa-pki (the CA) private.

# one bundle per participant (send each OVER A SECURE CHANNEL — it holds a key):
./central-up.sh join --cluster-id <provider-id> --ca-dir ./fa-pki
./central-up.sh join --cluster-id <consumer-id> --ca-dir ./fa-pki
```

### Step 2 — Mock admin (optional, needed for eco/latency)
```bash
./mock-up.sh --public-endpoint <mock node IP> --tag <image tag> \
             --kubeconfig <kubeconfig file path>
# prints the two --mock-*-url values to give to providers/consumer.
```

### Step 3 — Each provider admin (using their bundle)
```bash
./provider-up.sh --bundle <provider bundle .tgz> --cluster-id <provider-id> \
                 --tag <image tag> --kubeconfig <kubeconfig file path> \
                 --mock-eco-url <mock-eco URL> \
                 --mock-geo-url <mock-geo URL> \
                 --public-endpoint <provider node IP>
# liqoctl auto-detects the cluster's CIDRs; add --pod-cidr/--service-cidr only if it can't.
```

### Step 4 — Consumer admin (using their bundle)
```bash
./consumer-up.sh --bundle <consumer bundle .tgz> --cluster-id <consumer-id> \
                 --tag <image tag> --kubeconfig <kubeconfig file path> \
                 --mock-geo-url <mock-geo URL> \
                 --public-endpoint <consumer node IP>
# add --scale-down-unneeded-time 1m for a snappy demo.
```

---

## 3. Drive it (same as the demo)

1. **Broker dashboard** — `http://<central-ip>:30444/` — watch advertisements
   (cost/carbon/region) and reservations.
2. **Provider consoles** — `http://<provider-ip>:30445/` — set each provider's
   price, region, and capacity %.
3. **Consumer console** — `http://<consumer-ip>:30445/` — pick a policy
   (Price/Eco/Latency) + region, then flip the **workload** switch ON.
4. Watch the broker dashboard: **Price→cheapest, Eco→greenest, Latency→closest**.
   Flip the workload OFF to scale down. Optionally view peering on the **Liqo
   dashboard** (`http://liqo-dashboard.local`, after adding the hosts entry the
   script prints).

---

## 4. Tear down

Per-role scripts remove only what was deployed — **no k3s / node changes**:
```bash
./consumer-down.sh --kubeconfig <kubeconfig file path>   # [--uninstall-liqo] [--delete-crds]
./provider-down.sh --kubeconfig <kubeconfig file path>   # [--uninstall-liqo]
./mock-down.sh     --kubeconfig <kubeconfig file path>
./central-down.sh  --kubeconfig <kubeconfig file path>      # [--delete-crds]
```
`--uninstall-liqo` additionally runs `liqoctl unpeer` + `uninstall` (best-effort). `central-down.sh`
leaves the federation CA (`./fa-pki`) on your machine untouched.

---

## Notes
- Certs: the **federation CA** private key stays only on the central admin's
  machine (`--ca-dir ./fa-pki`); each cluster gets a pre-signed client cert in its
  bundle. The consumer's Cluster-Autoscaler↔gRPC mTLS uses a **separate** local CA
  the consumer script generates itself.
- Re-running a script is safe (idempotent); re-running `central-up.sh join`
  re-issues that cluster's cert.
- If a cluster already has Liqo, pass `--skip-liqo`.
