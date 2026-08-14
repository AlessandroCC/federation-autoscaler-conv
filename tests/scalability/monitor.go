/*
Copyright 2026 Politecnico di Torino - NetGroup.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Resource-usage sampling. The Broker is a controller-runtime process with
// no built-in per-process CPU/RAM self-report over its own REST API — the
// only real, already-implemented signals this repository exposes are:
//
//   - the Kubernetes Deployment it always runs as in every deploy path this
//     repo ships (config/broker/deployment.yaml; deploy/standalone/central-up.sh
//     runs `kubectl rollout status deploy/broker` — there is no bare-process
//     or Docker Compose deployment path in this repository at all), sampled
//     via `kubectl top pod` (needs metrics-server, the standard k8s add-on);
//   - `docker stats`, useful only when the target cluster is a local
//     kind/minikube-on-Docker node and the caller supplies the container
//     name directly (this repository does not run the Broker as a bare
//     Docker container in any script);
//   - direct process sampling via `ps`, useful only for a manual
//     `go run ./cmd/broker` smoke test outside Kubernetes.
//
// A fourth, unimplemented option is worth recording for future work: the
// manager's controller-runtime metrics server (--metrics-bind-address,
// wired in internal/manager/manager.go and set to :8443 in
// config/broker/deployment.yaml) exposes the standard Prometheus process
// collector (process_cpu_seconds_total, process_resident_memory_bytes) —
// but it defaults to --metrics-secure=true, requiring a bearer token with
// RBAC access (see config/rbac and config/network-policy/allow-metrics-traffic.yaml),
// and this repository ships no Prometheus server to scrape it from
// (config/prometheus/monitor.yaml only defines a ServiceMonitor, which is
// inert without a Prometheus Operator watching it). Wiring a token-authenticated
// scrape here was left out rather than guessed at; --monitor-mode=k8s is the
// documented default for a real cluster.
package main

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ResourceSample is one CPU/RAM observation of the Broker process/pod.
type ResourceSample struct {
	Timestamp time.Time
	CPUValue  float64
	CPUUnit   string // "cores" (k8s/process) or "percent" (docker)
	MemMiB    float64
	Source    string // human-readable description of where the sample came from
}

// MonitorRunner samples Broker resource usage on a fixed interval until ctx
// is cancelled. sampleFn is swapped per --monitor-mode by newMonitor.
type MonitorRunner struct {
	interval time.Duration
	sampleFn func(ctx context.Context) (ResourceSample, error)
	samples  []ResourceSample
	errs     []string
}

func newMonitor(cfg *Config) (*MonitorRunner, error) {
	var fn func(ctx context.Context) (ResourceSample, error)
	switch cfg.MonitorMode {
	case "none":
		return nil, nil
	case "k8s":
		fn = k8sSampler(cfg)
	case "docker":
		fn = dockerSampler(cfg)
	case "process":
		fn = processSampler(cfg)
	default:
		return nil, fmt.Errorf("monitor: unknown mode %q", cfg.MonitorMode)
	}
	return &MonitorRunner{interval: cfg.MonitorInterval, sampleFn: fn}, nil
}

// Run blocks, sampling every interval, until ctx is cancelled. Sampling
// errors are recorded (not fatal) so a transient `kubectl top` hiccup
// doesn't abort the whole experiment.
func (m *MonitorRunner) Run(ctx context.Context) {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s, err := m.sampleFn(ctx)
			if err != nil {
				m.errs = append(m.errs, err.Error())
				continue
			}
			m.samples = append(m.samples, s)
		}
	}
}

// --- k8s: kubectl top pod ---------------------------------------------------

func k8sSampler(cfg *Config) func(ctx context.Context) (ResourceSample, error) {
	return func(ctx context.Context) (ResourceSample, error) {
		args := []string{"top", "pod", "-n", cfg.BrokerNamespace, "--no-headers", "--containers=false"}
		if cfg.Kubeconfig != "" {
			args = append([]string{"--kubeconfig", cfg.Kubeconfig}, args...)
		}
		if cfg.BrokerPod != "" {
			args = append(args, cfg.BrokerPod)
		} else {
			args = append(args, "-l", cfg.BrokerPodLabel)
		}
		out, err := exec.CommandContext(ctx, "kubectl", args...).Output()
		if err != nil {
			return ResourceSample{}, fmt.Errorf("kubectl top pod: %w", err)
		}

		var cpuCores, memMiB float64
		lines := 0
		sc := bufio.NewScanner(strings.NewReader(string(out)))
		for sc.Scan() {
			fields := strings.Fields(sc.Text())
			if len(fields) < 3 {
				continue
			}
			cpuCores += parseK8sCPU(fields[1])
			memMiB += parseK8sMemMiB(fields[2])
			lines++
		}
		if lines == 0 {
			return ResourceSample{}, fmt.Errorf("kubectl top pod: no matching pod (namespace=%s label=%s pod=%s)",
				cfg.BrokerNamespace, cfg.BrokerPodLabel, cfg.BrokerPod)
		}
		return ResourceSample{
			Timestamp: time.Now(),
			CPUValue:  cpuCores,
			CPUUnit:   "cores",
			MemMiB:    memMiB,
			Source:    "kubectl top pod",
		}, nil
	}
}

// parseK8sCPU parses kubectl top's CPU column ("123m" millicores, or a bare
// core count like "1") into fractional cores.
func parseK8sCPU(s string) float64 {
	if strings.HasSuffix(s, "m") {
		v, _ := strconv.ParseFloat(strings.TrimSuffix(s, "m"), 64)
		return v / 1000
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// parseK8sMemMiB parses kubectl top's MEMORY column (Ki/Mi/Gi suffix, or
// bare bytes) into MiB.
func parseK8sMemMiB(s string) float64 {
	units := map[string]float64{
		"Ki": 1.0 / 1024, "Mi": 1, "Gi": 1024, "Ti": 1024 * 1024,
		"K": 1000.0 / (1024 * 1024), "M": 1e6 / (1024 * 1024), "G": 1e9 / (1024 * 1024),
	}
	for suffix, mult := range units {
		if strings.HasSuffix(s, suffix) {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(s, suffix), 64)
			return v * mult
		}
	}
	v, _ := strconv.ParseFloat(s, 64)
	return v / (1024 * 1024)
}

// --- docker: docker stats ---------------------------------------------------

func dockerSampler(cfg *Config) func(ctx context.Context) (ResourceSample, error) {
	return func(ctx context.Context) (ResourceSample, error) {
		out, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream",
			"--format", "{{.CPUPerc}}\t{{.MemUsage}}", cfg.BrokerContainer).Output()
		if err != nil {
			return ResourceSample{}, fmt.Errorf("docker stats: %w", err)
		}
		fields := strings.SplitN(strings.TrimSpace(string(out)), "\t", 2)
		if len(fields) != 2 {
			return ResourceSample{}, fmt.Errorf("docker stats: unexpected output %q", string(out))
		}
		cpuPct, _ := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(fields[0]), "%"), 64)
		memMiB := parseDockerMem(fields[1])
		return ResourceSample{
			Timestamp: time.Now(),
			CPUValue:  cpuPct,
			CPUUnit:   "percent",
			MemMiB:    memMiB,
			Source:    "docker stats container=" + cfg.BrokerContainer,
		}, nil
	}
}

// parseDockerMem parses docker stats' "12.34MiB / 256MiB" MemUsage column,
// keeping only the usage side, converted to MiB.
func parseDockerMem(s string) float64 {
	usage := strings.TrimSpace(strings.SplitN(s, "/", 2)[0])
	switch {
	case strings.HasSuffix(usage, "GiB"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(usage, "GiB"), 64)
		return v * 1024
	case strings.HasSuffix(usage, "MiB"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(usage, "MiB"), 64)
		return v
	case strings.HasSuffix(usage, "KiB"):
		v, _ := strconv.ParseFloat(strings.TrimSuffix(usage, "KiB"), 64)
		return v / 1024
	default:
		v, _ := strconv.ParseFloat(usage, 64)
		return v
	}
}

// --- process: ps -p <pid> ----------------------------------------------------

func processSampler(cfg *Config) func(ctx context.Context) (ResourceSample, error) {
	pid := strconv.Itoa(cfg.BrokerPID)
	return func(ctx context.Context) (ResourceSample, error) {
		// %cpu is a percentage of one core (standard BSD/GNU ps semantics);
		// rss is resident set size in KiB. Requires a POSIX `ps` (Linux/macOS,
		// or Git Bash/WSL on Windows) — see README Limitations.
		out, err := exec.CommandContext(ctx, "ps", "-o", "%cpu=,rss=", "-p", pid).Output()
		if err != nil {
			return ResourceSample{}, fmt.Errorf("ps -p %s: %w", pid, err)
		}
		fields := strings.Fields(string(out))
		if len(fields) != 2 {
			return ResourceSample{}, fmt.Errorf("ps -p %s: unexpected output %q", pid, string(out))
		}
		cpuPct, _ := strconv.ParseFloat(fields[0], 64)
		rssKiB, _ := strconv.ParseFloat(fields[1], 64)
		return ResourceSample{
			Timestamp: time.Now(),
			CPUValue:  cpuPct,
			CPUUnit:   "percent",
			MemMiB:    rssKiB / 1024,
			Source:    "ps pid=" + pid,
		}, nil
	}
}
