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

package ollama

import (
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"

	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// SystemPrompt is the fixed system-level instruction sent to Ollama on every
// ConsumerChoice selection call. It constrains the LLM to return exactly one
// provider ID from the input list and nothing else.
const SystemPrompt = `You are a Kubernetes provider selection assistant.

You will receive a user request and a JSON list of available providers.
Select exactly one provider that best matches the user's request.

Rules:
- Choose only from the providers in the list.
- Never invent provider IDs or field values.
- If the request is vague, prefer in order: lowest cost, lowest carbon, most resources.
- If two providers are equivalent, prefer the one with lower cost.
- Return only valid JSON matching this schema: {"providerId": "<id>"}
- No explanations, markdown, or extra text.`

// ProviderInfo is the structured JSON the LLM sees per provider. It contains
// only the fields relevant to a placement decision, flattened from the
// NodeGroupView wire type.
type ProviderInfo struct {
	ProviderID      string   `json:"providerId"`
	ClusterType     string   `json:"clusterType"`
	AvailableChunks int32    `json:"availableChunks"`
	CPUPerChunk     string   `json:"cpuPerChunk,omitempty"`
	MemoryPerChunk  string   `json:"memoryPerChunk,omitempty"`
	GPUPerChunk     string   `json:"gpuPerChunk,omitempty"`
	CostPerChunk    *float64 `json:"costPerChunk,omitempty"`
	CarbonIntensity *float64 `json:"carbonIntensity,omitempty"`
	Region          string   `json:"region,omitempty"`
	Latitude        float64  `json:"latitude,omitempty"`
	Longitude       float64  `json:"longitude,omitempty"`
}

// SelectionResponse is the JSON schema Ollama must return.
type SelectionResponse struct {
	ProviderID string `json:"providerId"`
}

// NodeGroupViewToProviderInfo converts a broker NodeGroupView into the
// simplified shape the LLM receives.
func NodeGroupViewToProviderInfo(ng brokerapi.NodeGroupView) ProviderInfo {
	info := ProviderInfo{
		ProviderID:      ng.ProviderClusterID,
		ClusterType:     string(ng.Type),
		AvailableChunks: ng.MaxSize - ng.CurrentReserved,
	}

	// Extract per-chunk resources.
	if q, ok := ng.ChunkResources[corev1.ResourceCPU]; ok {
		info.CPUPerChunk = q.String()
	}
	if q, ok := ng.ChunkResources[corev1.ResourceMemory]; ok {
		info.MemoryPerChunk = q.String()
	}
	if q, ok := ng.ChunkResources["nvidia.com/gpu"]; ok {
		info.GPUPerChunk = q.String()
	}

	// Cost: convert from *resource.Quantity to *float64 for JSON readability.
	if ng.Cost != nil {
		v := ng.Cost.AsApproximateFloat64()
		info.CostPerChunk = &v
	}

	// Carbon and topology.
	info.CarbonIntensity = ng.CarbonIntensity
	if ng.Topology != nil {
		info.Region = ng.Topology.Region
		info.Latitude = ng.Topology.Latitude
		info.Longitude = ng.Topology.Longitude
	}

	return info
}

// BuildUserPrompt assembles the user-facing portion of the Ollama prompt from
// the user's natural-language request and the structured provider list.
func BuildUserPrompt(userRequest string, providers []ProviderInfo) string {
	providersJSON, err := json.MarshalIndent(providers, "", "  ")
	if err != nil {
		// Fallback to compact JSON if indented fails (should never happen).
		providersJSON, _ = json.Marshal(providers)
	}

	var sb strings.Builder
	sb.WriteString("USER REQUEST:\n")
	sb.WriteString(fmt.Sprintf("%q\n\n", userRequest))
	sb.WriteString("AVAILABLE PROVIDERS:\n")
	sb.Write(providersJSON)
	return sb.String()
}
