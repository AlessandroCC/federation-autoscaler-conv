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
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// BrokerCASecretName is the cert-manager-produced Secret that holds the
// federation-autoscaler CA cert+key on the central cluster. Created by
// the config/broker/certmanager.yaml Certificate of the same name.
const BrokerCASecretName = "federation-autoscaler-ca-cert"

// BrokerCAIssuerName is the cert-manager Issuer the config/agent and
// config/grpc-server overlays reference. On the central cluster it's
// created by the broker overlay; on agent / grpc-server clusters this
// package's MirrorBrokerCA creates it.
const BrokerCAIssuerName = "federation-autoscaler-ca-issuer"

// MirrorBrokerCA copies the broker's CA Secret from the central cluster
// to a target cluster and then creates a cert-manager Issuer of kind
// `ca` that references it. This is what makes the federation-autoscaler
// Certificates on the target cluster (agent-client / grpc-server /
// cluster-autoscaler-client) actually sign with the same root the
// broker trusts — without this step they stay Ready=False forever.
//
// The function waits for the Secret to exist on the central cluster
// before attempting to copy it; cert-manager needs a few seconds after
// the broker overlay is applied to issue the CA certificate.
func MirrorBrokerCA(ctx context.Context, centralKubeconfig, targetKubeconfig string) error {
	switch {
	case centralKubeconfig == "":
		return fmt.Errorf("MirrorBrokerCA: centralKubeconfig %w", errEmpty)
	case targetKubeconfig == "":
		return fmt.Errorf("MirrorBrokerCA: targetKubeconfig %w", errEmpty)
	}
	kubectl, err := resolveBinary("", envKubectl, defaultKubectlBin)
	if err != nil {
		return err
	}

	// Wait for the CA Secret on central — cert-manager fills it in a
	// few seconds after the broker overlay is applied, but we may have
	// just applied the overlay so it might not be ready yet.
	caSecretJSON, err := waitForSecret(ctx, kubectl, centralKubeconfig,
		BrokerNamespace, BrokerCASecretName, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("wait for %s Secret on central: %w", BrokerCASecretName, err)
	}

	// Strip per-cluster metadata (resourceVersion, uid, managedFields,
	// creationTimestamp) so the same Secret applies cleanly to the
	// target cluster.
	stripped, err := stripSecretMetadata(caSecretJSON, BrokerCASecretName, BrokerNamespace)
	if err != nil {
		return fmt.Errorf("strip Secret metadata: %w", err)
	}

	// Apply the Secret on the target cluster.
	if err := applyStdin(ctx, kubectl, targetKubeconfig, stripped); err != nil {
		return fmt.Errorf("apply CA Secret on target: %w", err)
	}

	// Create the Issuer of kind ca pointing at the mirrored Secret.
	issuer := fmt.Sprintf(`apiVersion: cert-manager.io/v1
kind: Issuer
metadata:
  name: %s
  namespace: %s
spec:
  ca:
    secretName: %s
`, BrokerCAIssuerName, BrokerNamespace, BrokerCASecretName)
	if err := applyStdin(ctx, kubectl, targetKubeconfig, issuer); err != nil {
		return fmt.Errorf("apply CA Issuer on target: %w", err)
	}
	return nil
}

// waitForSecret polls `kubectl get secret` until it returns a Secret
// with non-empty `data` (cert-manager populates the Secret in one shot
// once the Certificate is Ready). Returns the Secret's JSON.
func waitForSecret(ctx context.Context, kubectl, kubeconfig, namespace, name string,
	timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		out, err := exec.CommandContext(ctx, kubectl,
			"--kubeconfig="+kubeconfig,
			"get", "secret", name,
			"--namespace", namespace,
			"-o", "json").CombinedOutput()
		if err == nil {
			var sec struct {
				Data map[string]string `json:"data"`
			}
			if json.Unmarshal(out, &sec) == nil && len(sec.Data) > 0 {
				return string(out), nil
			}
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("timed out after %s; last kubectl output: %s",
				timeout, strings.TrimSpace(string(out)))
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// stripSecretMetadata removes per-cluster metadata so the Secret
// applies cleanly on a different cluster.
func stripSecretMetadata(rawJSON, name, namespace string) (string, error) {
	var sec map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &sec); err != nil {
		return "", err
	}
	// Rewrite metadata to keep only name + namespace + the cert-manager
	// annotation kubectl apply will need.
	sec["metadata"] = map[string]any{
		"name":      name,
		"namespace": namespace,
	}
	// Clear status so the apiserver doesn't reject the apply with an
	// immutable-field error.
	delete(sec, "status")
	out, err := json.Marshal(sec)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// applyStdin pipes manifest YAML/JSON into `kubectl apply -f -`.
func applyStdin(ctx context.Context, kubectl, kubeconfig, manifest string) error {
	cmd := exec.CommandContext(ctx, kubectl,
		"--kubeconfig="+kubeconfig, "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
