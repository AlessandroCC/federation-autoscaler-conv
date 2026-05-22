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

package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/netgroup-polito/federation-autoscaler/test/e2e/kind"
)

// envKustomize is the env-var name used to override the kustomize binary
// (matches the Makefile-style KIND ?= kind convention).
const envKustomize = "KUSTOMIZE"
const defaultKustomizeBin = "kustomize"

// OverlayPathForRole returns the path (relative to repo root) of the
// kustomize overlay that should be applied to a given Kind role.
//
// The central cluster gets the broker; consumer-1 gets the consumer
// agent + the gRPC server; provider clusters get the provider agent.
func OverlayPathForRole(role kind.Role) []string {
	switch role {
	case kind.RoleCentral:
		return []string{"config/broker"}
	case kind.RoleConsumer1:
		return []string{"config/agent/consumer", "config/grpc-server"}
	case kind.RoleProvider1, kind.RoleProvider2:
		return []string{"config/agent/provider"}
	default:
		return nil
	}
}

// ApplyOverlayOptions configures ApplyOverlay.
type ApplyOverlayOptions struct {
	// Kubeconfig is the path to the kubeconfig of the target cluster.
	// Required.
	Kubeconfig string

	// OverlayPath is the kustomize overlay directory (e.g.
	// "config/broker"). Resolved relative to the repo root when not
	// absolute. Required.
	OverlayPath string

	// CRDs (default true) decides whether the federation-autoscaler
	// CRDs are bundled with the overlay apply. Most overlays do NOT
	// include CRDs themselves so the suite layers them in via
	// InstallCRDs; set this false when the caller has already invoked
	// InstallCRDs.
	CRDs bool

	// KustomizeBinary / KubectlBinary override the executables. Empty
	// resolves to $KUSTOMIZE / $KUBECTL or the default name on $PATH.
	KustomizeBinary string
	KubectlBinary   string
}

// ApplyOverlay renders the kustomize overlay and pipes it through
// `kubectl apply -f -` against the target kubeconfig. Idempotent.
//
// The kustomize binary is invoked from the repo root so relative
// `resources:` references resolve correctly.
func ApplyOverlay(ctx context.Context, opts ApplyOverlayOptions) error {
	switch {
	case opts.Kubeconfig == "":
		return fmt.Errorf("ApplyOverlay: Kubeconfig %w", errEmpty)
	case opts.OverlayPath == "":
		return fmt.Errorf("ApplyOverlay: OverlayPath %w", errEmpty)
	}
	kustomizeBin, err := resolveBinary(opts.KustomizeBinary, envKustomize, defaultKustomizeBin)
	if err != nil {
		return err
	}
	kubectl, err := resolveBinary(opts.KubectlBinary, envKubectl, defaultKubectlBin)
	if err != nil {
		return err
	}
	overlayPath := opts.OverlayPath
	if !filepath.IsAbs(overlayPath) {
		root, err := repoRoot()
		if err != nil {
			return fmt.Errorf("resolve repo root: %w", err)
		}
		overlayPath = filepath.Join(root, overlayPath)
	}

	build := exec.CommandContext(ctx, kustomizeBin, "build", overlayPath)
	apply := exec.CommandContext(ctx, kubectl, "--kubeconfig="+opts.Kubeconfig, "apply", "-f", "-")

	// Pipe kustomize's stdout into kubectl's stdin; capture each side's
	// stderr separately so we can surface a useful diagnostic on either
	// half failing.
	stdoutPipe, err := build.StdoutPipe()
	if err != nil {
		return fmt.Errorf("kustomize stdout pipe: %w", err)
	}
	apply.Stdin = stdoutPipe

	var buildStderr, applyStderr strings.Builder
	build.Stderr = &buildStderr
	apply.Stderr = &applyStderr

	if err := build.Start(); err != nil {
		return fmt.Errorf("kustomize start: %w", err)
	}
	if err := apply.Start(); err != nil {
		_ = build.Process.Kill()
		return fmt.Errorf("kubectl apply start: %w", err)
	}

	// kustomize must finish first (so it closes the pipe and kubectl can
	// see EOF on stdin). Then kubectl drains and exits.
	buildErr := build.Wait()
	applyErr := apply.Wait()

	if buildErr != nil {
		return fmt.Errorf("kustomize build %q: %w: %s",
			overlayPath, buildErr, strings.TrimSpace(buildStderr.String()))
	}
	if applyErr != nil {
		return fmt.Errorf("kubectl apply: %w: %s",
			applyErr, strings.TrimSpace(applyStderr.String()))
	}
	return nil
}

// EnsureNamespace creates the named Namespace on the target cluster
// (idempotent — `apply` of an existing Namespace is a no-op). Used by
// the e2e suite to pre-create federation-autoscaler-system on the
// agent / grpc-server clusters: only the broker overlay ships the
// Namespace resource, and it lands on the central cluster, so the
// other clusters need it explicitly before their overlays can be
// applied.
func EnsureNamespace(ctx context.Context, kubeconfig, name string) error {
	switch {
	case kubeconfig == "":
		return fmt.Errorf("EnsureNamespace: kubeconfig %w", errEmpty)
	case name == "":
		return fmt.Errorf("EnsureNamespace: name %w", errEmpty)
	}
	kubectl, err := resolveBinary("", envKubectl, defaultKubectlBin)
	if err != nil {
		return err
	}
	// Pipe a one-line Namespace manifest into `kubectl apply -f -`
	// rather than `kubectl create namespace` (which errors when the
	// namespace already exists).
	manifest := fmt.Sprintf("apiVersion: v1\nkind: Namespace\nmetadata:\n  name: %s\n", name)
	cmd := exec.CommandContext(ctx, kubectl,
		"--kubeconfig="+kubeconfig, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ensure namespace %q: %w: %s",
			name, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RolloutRestart triggers a `kubectl rollout restart deployment/<name>`
// on the target cluster and waits for the rollout to finish. Used by
// the e2e suite to force agent / grpc-server pods to re-read their
// freshly-rotated TLS Secrets after PatchAgentConfig flips the
// Certificate's commonName from the REPLACE_ME placeholder to the
// real cluster identity.
//
// Without this restart, pods that loaded the cert at startup keep the
// old REPLACE_ME_CLUSTER_ID CN in memory even after kubelet has
// refreshed the projected volume, and every mTLS call returns 403.
func RolloutRestart(ctx context.Context, kubeconfig, namespace, deployment string) error {
	switch {
	case kubeconfig == "":
		return fmt.Errorf("RolloutRestart: kubeconfig %w", errEmpty)
	case deployment == "":
		return fmt.Errorf("RolloutRestart: deployment %w", errEmpty)
	}
	kubectl, err := resolveBinary("", envKubectl, defaultKubectlBin)
	if err != nil {
		return err
	}
	if namespace == "" {
		namespace = BrokerNamespace
	}
	out, err := exec.CommandContext(ctx, kubectl,
		"--kubeconfig="+kubeconfig,
		"rollout", "restart", "deployment/"+deployment,
		"--namespace", namespace,
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rollout restart %s/%s: %w: %s",
			namespace, deployment, err, strings.TrimSpace(string(out)))
	}
	out, err = exec.CommandContext(ctx, kubectl,
		"--kubeconfig="+kubeconfig,
		"rollout", "status", "deployment/"+deployment,
		"--namespace", namespace,
		"--timeout", "3m",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("rollout status %s/%s: %w: %s",
			namespace, deployment, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// PinBrokerServiceNodePort kubectl-patches the broker Service to a
// NodePort on CentralBrokerNodePort. Idempotent. Agents on consumer /
// provider clusters reach the broker via this NodePort across the
// shared docker network the Kind clusters all sit on.
func PinBrokerServiceNodePort(ctx context.Context, kubeconfig string) error {
	if kubeconfig == "" {
		return fmt.Errorf("PinBrokerServiceNodePort: kubeconfig %w", errEmpty)
	}
	kubectl, err := resolveBinary("", envKubectl, defaultKubectlBin)
	if err != nil {
		return err
	}
	patch, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"type": "NodePort",
			"ports": []map[string]any{
				{
					"name":     "api",
					"port":     9443,
					"nodePort": CentralBrokerNodePort,
				},
			},
		},
	})
	if err != nil {
		return fmt.Errorf("encode service patch: %w", err)
	}
	return runCommand(ctx, kubectl, withKubeconfig(kubeconfig,
		"patch", "service", "broker",
		"--namespace", BrokerNamespace,
		"--type", "strategic",
		"--patch", string(patch),
	)...)
}

// repoRoot resolves to the repo root by walking up from this source
// file. Cheap, deterministic, no env vars.
func repoRoot() (string, error) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("runtime.Caller failed")
	}
	// thisFile = .../test/e2e/bootstrap/overlays.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..")), nil
}
