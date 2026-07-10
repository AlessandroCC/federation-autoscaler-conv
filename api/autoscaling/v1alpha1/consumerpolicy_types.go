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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PlacementStrategy selects how the Broker places a consumer's chunk requests
// across the available provider clusters. An empty value (or a missing
// ConsumerPolicy) means the Broker keeps its default behaviour: it exposes all
// available providers and lets the Cluster Autoscaler pick — i.e. no
// broker-driven preference. The Broker is the decision-maker only when a
// strategy is set; the Cluster Autoscaler never sees the ranking metric
// (price, carbon, or distance).
// +kubebuilder:validation:Enum=Price;Eco;Latency;ConsumerChoice
type PlacementStrategy string

const (
	// PlacementStrategyPrice makes the Broker prefer, for this consumer, the
	// cheapest *priced* provider(s) that still have capacity (cheapest-first
	// greedy). Providers without a price are reached only as a last resort.
	PlacementStrategyPrice PlacementStrategy = "Price"

	// PlacementStrategyEco makes the Broker prefer, for this consumer, the
	// provider(s) with the lowest advertised carbon intensity that still have
	// capacity (greenest-first greedy). Providers that advertise no carbon
	// intensity are reached only as a last resort. Mirrors the Price greedy.
	PlacementStrategyEco PlacementStrategy = "Eco"

	// PlacementStrategyLatency makes the Broker prefer, for this consumer, the
	// geographically closest provider(s) with capacity — ranked by the
	// great-circle (Haversine) distance between the consumer's advertised
	// location and each provider's advertised location (closest-first greedy).
	// If the consumer has not advertised a location, the Broker applies no
	// preference (all providers stay exposed). v1 is estimation-only; no
	// measured RTT is used.
	PlacementStrategyLatency PlacementStrategy = "Latency"

	// PlacementStrategyConsumerChoice delegates provider selection to a local
	// LLM (e.g. Ollama) running on the consumer cluster. The Broker returns
	// all available providers (no masking), and the Consumer Agent passes the
	// list plus the user's natural-language request (ConsumerPolicySpec.UserPrompt)
	// to the AI, which picks one. Falls back deterministically if the AI is
	// unreachable or returns an invalid choice.
	PlacementStrategyConsumerChoice PlacementStrategy = "ConsumerChoice"
)

// PlacementPolicy is the placement policy a consumer declares for itself. It is
// carried on the Consumer Agent's heartbeat to the Broker.
type PlacementPolicy struct {
	// Type selects the placement strategy. Empty means the Broker default
	// (no preference; the Cluster Autoscaler picks). Supported values are
	// "Price" (cheapest), "Eco" (lowest carbon), and "Latency" (closest).
	// +optional
	Type PlacementStrategy `json:"type,omitempty"`
}

// ConsumerPolicySpec is the desired placement policy for the consumer cluster
// it lives on. It is operator-stamped (manually, or via the fa_consumer Ansible
// role) and may be edited at any time; the Consumer Agent re-reads it on every
// heartbeat (~15 s) so changes take effect without a restart.
type ConsumerPolicySpec struct {
	// Placement is the resource-placement policy the Broker applies to this
	// consumer's requests.
	// +optional
	Placement PlacementPolicy `json:"placement,omitempty,omitzero"`

	// UserPrompt is the natural-language selection request used by the
	// ConsumerChoice strategy. The Consumer Agent passes it alongside the
	// provider list to a local LLM (e.g. Ollama) which interprets it and
	// selects a single provider. Ignored for other strategies. Example:
	// "I want the cheapest provider that is also reasonably green".
	// Re-read every heartbeat so changes take effect without a restart.
	// +optional
	UserPrompt string `json:"userPrompt,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=cpolicy
// +kubebuilder:printcolumn:name="Placement",type=string,JSONPath=`.spec.placement.type`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ConsumerPolicy is a consumer-cluster-local declaration of how the Broker
// should place this consumer's borrowed capacity (e.g. prefer the cheapest
// providers). It lives only on the consumer cluster; the Broker never reads it
// directly — the Consumer Agent pushes its spec on the heartbeat, preserving
// the agent-initiated (no Broker→cluster dial-in) communication model.
type ConsumerPolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata.
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty,omitzero"`

	// spec is the desired placement policy.
	// +required
	Spec ConsumerPolicySpec `json:"spec"`
}

// +kubebuilder:object:root=true

// ConsumerPolicyList contains a list of ConsumerPolicy.
type ConsumerPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ConsumerPolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ConsumerPolicy{}, &ConsumerPolicyList{})
}
