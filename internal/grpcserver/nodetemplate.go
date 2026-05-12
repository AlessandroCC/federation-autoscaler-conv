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

package grpcserver

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// buildNodeTemplate constructs a v1.Node Cluster Autoscaler can use as
// the template for a node group — i.e. the shape every node it would
// spin up in this group would have. CA marshals this into its planner
// via NodeGroupTemplateNodeInfoResponse.NodeBytes.
//
// Shape is lifted from
// `../Multi-Cluster-Autoscaler/server-gRPC/server.go` and
// `node-manager/functions/functions.go:mapNodegroupTemplate`, minus
// the hardcoded STANDARD / GPU cases: chunk shape, labels, taints, and
// topology all come from the per-group NodeGroupView the broker
// advertises through the consumer agent's loopback REST.
//
// Production deployments rely on Liqo to stamp additional labels on
// the actual virtual nodes; the template only needs enough of them
// for CA's scheduler-simulation to admit pods that target this group.
func buildNodeTemplate(group *brokerapi.NodeGroupView) *corev1.Node {
	if group == nil {
		return nil
	}

	labels := mergeLabels(group.Labels, topologyLabels(group))

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "template-" + group.ID,
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			Taints:     append([]corev1.Taint(nil), group.Taints...),
			ProviderID: providerIDForTemplate(group),
		},
		Status: corev1.NodeStatus{
			Capacity:    cloneResourceList(group.ChunkResources),
			Allocatable: cloneResourceList(group.ChunkResources),
			Conditions: []corev1.NodeCondition{{
				Type:   corev1.NodeReady,
				Status: corev1.ConditionTrue,
				Reason: "FederationAutoscalerTemplate",
			}},
		},
	}
}

// topologyLabels translates a NodeGroupView's optional Topology into
// the standard topology.kubernetes.io/{region,zone} labels CA's
// scheduler simulator looks for. Returns nil when no fields are set.
func topologyLabels(group *brokerapi.NodeGroupView) map[string]string {
	if group.Topology == nil {
		return nil
	}
	out := map[string]string{}
	if group.Topology.Region != "" {
		out[corev1.LabelTopologyRegion] = group.Topology.Region
	}
	if group.Topology.Zone != "" {
		out[corev1.LabelTopologyZone] = group.Topology.Zone
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// providerIDForTemplate returns the providerID a real virtual node
// would carry. The proto requires the `<ProviderName>://<NodeID>`
// shape; for the template we use a stable scheme-only prefix so CA's
// per-group bookkeeping can correlate it with real nodes later.
func providerIDForTemplate(group *brokerapi.NodeGroupView) string {
	if group.ProviderClusterID == "" {
		return "liqo://" + group.ID
	}
	return "liqo://" + group.ProviderClusterID + "/" + group.ID
}

// mergeLabels combines several label maps; later maps override earlier
// keys. Returns nil when every input is empty so the resulting
// ObjectMeta.Labels stays nil instead of an empty map (a marshalling
// nicety — empty map vs nil map are wire-distinguishable).
func mergeLabels(a, b map[string]string) map[string]string {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

// cloneResourceList returns a defensive copy of rl. Necessary because
// the response we got from the agent is shared across calls and we
// don't want a downstream mutation to leak back into the cached view.
func cloneResourceList(rl corev1.ResourceList) corev1.ResourceList {
	if rl == nil {
		return nil
	}
	out := make(corev1.ResourceList, len(rl))
	for k, v := range rl {
		out[k] = v.DeepCopy()
	}
	return out
}
