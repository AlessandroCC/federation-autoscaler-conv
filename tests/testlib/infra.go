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

package testlib

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RunID generates a unique run identifier for cluster naming.
func RunID() string {
	return fmt.Sprintf("cmp-%s", time.Now().Format("20060102-150405"))
}

// ClusterSpec describes one Kind cluster to create.
type ClusterSpec struct {
	Name       string // full Kind cluster name (includes run-ID prefix)
	Role       string // "central", "consumer-1", "provider-1", etc.
	PodCIDR    string
	SvcCIDR    string
	HasWorker  bool   // whether to add a worker node
	Kubeconfig string // populated after creation
}

// GenerateClusterSpecs builds the list of Kind clusters for a given topology.
func GenerateClusterSpecs(runID string, numConsumers, numProviders int) []ClusterSpec {
	specs := make([]ClusterSpec, 0, 1+numConsumers+numProviders)

	idx := 0
	specs = append(specs, ClusterSpec{
		Name:      fmt.Sprintf("%s-central", runID),
		Role:      "central",
		PodCIDR:   cidr(idx, "pod"),
		SvcCIDR:   cidr(idx, "svc"),
		HasWorker: false,
	})
	idx++

	for i := 1; i <= numConsumers; i++ {
		specs = append(specs, ClusterSpec{
			Name:      fmt.Sprintf("%s-consumer-%d", runID, i),
			Role:      fmt.Sprintf("consumer-%d", i),
			PodCIDR:   cidr(idx, "pod"),
			SvcCIDR:   cidr(idx, "svc"),
			HasWorker: true,
		})
		idx++
	}

	for i := 1; i <= numProviders; i++ {
		specs = append(specs, ClusterSpec{
			Name:      fmt.Sprintf("%s-provider-%d", runID, i),
			Role:      fmt.Sprintf("provider-%d", i),
			PodCIDR:   cidr(idx, "pod"),
			SvcCIDR:   cidr(idx, "svc"),
			HasWorker: true,
		})
		idx++
	}

	return specs
}

func cidr(idx int, kind string) string {
	switch kind {
	case "pod":
		return fmt.Sprintf("10.%d.0.0/16", 241+idx)
	case "svc":
		return fmt.Sprintf("10.%d.0.0/16", 96+idx)
	default:
		panic("cidr: unknown kind " + kind)
	}
}

// KindCreateCluster creates a single Kind cluster from a spec.
func KindCreateCluster(ctx context.Context, spec *ClusterSpec, kubeconfigDir string) error {
	configFile, err := writeKindConfig(spec)
	if err != nil {
		return fmt.Errorf("write kind config for %s: %w", spec.Name, err)
	}
	defer os.Remove(configFile)

	kcPath := filepath.Join(kubeconfigDir, spec.Role+".kubeconfig")

	args := []string{
		"create", "cluster",
		"--name", spec.Name,
		"--config", configFile,
		"--kubeconfig", kcPath,
	}

	log.Printf("[infra] creating Kind cluster %s (pod=%s svc=%s)", spec.Name, spec.PodCIDR, spec.SvcCIDR)
	cmd := exec.CommandContext(ctx, "kind", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("kind create cluster %s: %w", spec.Name, err)
	}

	spec.Kubeconfig = kcPath
	log.Printf("[infra] cluster %s ready (kubeconfig=%s)", spec.Name, kcPath)
	return nil
}

func writeKindConfig(spec *ClusterSpec) (string, error) {
	var sb strings.Builder
	sb.WriteString("kind: Cluster\napiVersion: kind.x-k8s.io/v1alpha4\nnetworking:\n")
	fmt.Fprintf(&sb, "  podSubnet: %q\n", spec.PodCIDR)
	fmt.Fprintf(&sb, "  serviceSubnet: %q\n", spec.SvcCIDR)
	sb.WriteString("nodes:\n- role: control-plane\n")
	if spec.HasWorker {
		sb.WriteString("- role: worker\n")
	}

	f, err := os.CreateTemp("", "kind-config-*.yaml")
	if err != nil {
		return "", err
	}
	if _, err := f.WriteString(sb.String()); err != nil {
		f.Close()
		return "", err
	}
	f.Close()
	return f.Name(), nil
}

// KindDeleteCluster deletes a Kind cluster by name.
func KindDeleteCluster(ctx context.Context, name string) error {
	log.Printf("[infra] deleting Kind cluster %s", name)
	cmd := exec.CommandContext(ctx, "kind", "delete", "cluster", "--name", name)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// KindLoadImages loads Docker images into a Kind cluster.
func KindLoadImages(ctx context.Context, clusterName string, images []string) error {
	if len(images) == 0 {
		return nil
	}
	args := []string{"load", "docker-image", "--name", clusterName}
	args = append(args, images...)
	log.Printf("[infra] loading %d images into %s", len(images), clusterName)
	cmd := exec.CommandContext(ctx, "kind", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// DockerBuild runs `make docker-build` in the repo root.
func DockerBuild(ctx context.Context, repoRoot string) error {
	log.Println("[infra] building Docker images (make docker-build)")
	cmd := exec.CommandContext(ctx, "make", "docker-build")
	cmd.Dir = repoRoot
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), "IMG_PREFIX=federation-autoscaler", "TAG=latest")
	return cmd.Run()
}

// ContainerIP returns the Docker container IP of a Kind cluster node.
func ContainerIP(ctx context.Context, containerName string) (string, error) {
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}", containerName)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("docker inspect %s: %w", containerName, err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("no IP found for container %s", containerName)
	}
	return ip, nil
}

// CheckPrerequisites verifies the required tools are installed.
func CheckPrerequisites() error {
	for _, tool := range []string{"docker", "kind", "go"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("required tool %q not found on $PATH: %w", tool, err)
		}
	}
	cmd := exec.Command("docker", "info")
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("Docker is not running: %w", err)
	}
	return nil
}

// standaloneComponents lists the components built by make docker-build.
var standaloneComponents = []string{"broker", "agent", "grpc-server", "mock-eco", "mock-geo"}

// DefaultStandaloneRegistry is the default REGISTRY in deploy/standalone/common.sh.
const DefaultStandaloneRegistry = "docker.io/kazem26"

// standaloneImageName returns the image name the standalone scripts expect:
// REGISTRY/federation-autoscaler-COMPONENT:TAG.
func standaloneImageName(registry, component, tag string) string {
	return fmt.Sprintf("%s/federation-autoscaler-%s:%s", registry, component, tag)
}

// RetagForStandalone re-tags images from the make docker-build naming convention
// (IMG_PREFIX/component:TAG) to the standalone scripts' convention
// (REGISTRY/federation-autoscaler-component:TAG).
func RetagForStandalone(ctx context.Context, imgPrefix, tag, registry string) error {
	for _, comp := range standaloneComponents {
		src := fmt.Sprintf("%s/%s:%s", imgPrefix, comp, tag)
		dst := standaloneImageName(registry, comp, tag)
		log.Printf("[infra] retagging %s → %s", src, dst)
		cmd := exec.CommandContext(ctx, "docker", "tag", src, dst)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("tag %s -> %s: %w", src, dst, err)
		}
	}
	return nil
}

// CentralImages returns images needed on the central cluster (standalone naming).
func CentralImages(registry, tag string) []string {
	return []string{
		standaloneImageName(registry, "broker", tag),
		standaloneImageName(registry, "mock-eco", tag),
		standaloneImageName(registry, "mock-geo", tag),
	}
}

// AgentImages returns images needed on consumer/provider clusters (standalone naming).
func AgentImages(registry, tag string) []string {
	return []string{
		standaloneImageName(registry, "agent", tag),
	}
}

// ConsumerExtraImages returns additional images for consumer clusters (standalone naming).
func ConsumerExtraImages(registry, tag string) []string {
	return []string{
		standaloneImageName(registry, "grpc-server", tag),
	}
}

// RepoRoot finds the repository root by looking for go.mod.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not find go.mod in any parent directory")
		}
		dir = parent
	}
}
