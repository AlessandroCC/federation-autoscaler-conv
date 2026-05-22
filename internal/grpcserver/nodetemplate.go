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
	"maps"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// Liqo stamps these on every VirtualNode it materialises. The template
// node CA sees must carry both so its scheduler simulator (a) accepts
// workloads that selected federation capacity via the documented Liqo
// label and (b) only accepts workloads that opted-in via the matching
// toleration — without the taint we'd scale up for any unschedulable
// pod, then Liqo's real taint would keep it Pending forever.
const (
	LiqoNodeTypeLabel = "liqo.io/type"
	LiqoNodeTypeValue = "virtual-node"

	LiqoNotAllowedTaintKey    = "virtual-node.liqo.io/not-allowed"
	LiqoNotAllowedTaintEffect = corev1.TaintEffectNoExecute
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

	// CA's scheduler simulator dereferences these labels when matching
	// node selectors / topology constraints; if any are missing it can
	// panic deep in the simulator. Stamp the standard set so a template
	// produced from minimally-populated broker advertisements still
	// works.
	// LabelOSStable / LabelArchStable expand to "kubernetes.io/os" and
	// "kubernetes.io/arch" — the values CA's scheduler simulator
	// reads when matching nodeSelectors. LabelInstanceTypeStable is
	// "node.kubernetes.io/instance-type"; the deprecated
	// LabelInstanceType ("beta.kubernetes.io/instance-type") is
	// included for backwards compat with older selectors.
	//
	// LiqoNodeTypeLabel / LiqoNodeTypeValue mirror the label Liqo
	// stamps on every materialised VirtualNode. Without it on the
	// template, any workload that selects federation capacity via
	// `nodeSelector: liqo.io/type: virtual-node` (the documented Liqo
	// pattern, and what real users will write) gets rejected by CA's
	// NodeAffinity predicate during scale-up evaluation, so CA returns
	// "No expansion options" and the Pods stay Pending forever.
	defaults := map[string]string{
		corev1.LabelHostname:           "template-" + group.ID,
		corev1.LabelOSStable:           "linux",
		corev1.LabelArchStable:         "amd64",
		corev1.LabelInstanceType:       "liqo-virtual",
		corev1.LabelInstanceTypeStable: "liqo-virtual",
		LiqoNodeTypeLabel:              LiqoNodeTypeValue,
	}
	labels := mergeLabels(mergeLabels(defaults, group.Labels), topologyLabels(group))

	// Capacity / Allocatable must contain at least cpu+memory+pods or
	// CA's scheduler-simulator will refuse to fit anything onto the
	// template. Backfill from a safe minimum when the broker hasn't
	// surfaced ChunkResources.
	resources := nonEmptyResources(cloneResourceList(group.ChunkResources))

	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "template-" + group.ID,
			Labels: labels,
		},
		Spec: corev1.NodeSpec{
			Taints:     ensureLiqoTaint(append([]corev1.Taint(nil), group.Taints...)),
			ProviderID: providerIDForTemplate(group),
		},
		Status: corev1.NodeStatus{
			Capacity:    resources,
			Allocatable: resources.DeepCopy(),
			Conditions: []corev1.NodeCondition{{
				Type:               corev1.NodeReady,
				Status:             corev1.ConditionTrue,
				Reason:             "FederationAutoscalerTemplate",
				LastHeartbeatTime:  metav1.Now(),
				LastTransitionTime: metav1.Now(),
			}},
			NodeInfo: corev1.NodeSystemInfo{
				OperatingSystem: "linux",
				Architecture:    "amd64",
			},
		},
	}
}

// ensureLiqoTaint appends the Liqo NoExecute taint to the template's
// taint set if the broker advertisement didn't already include it.
// Matching by (key, effect) so a broker-supplied value override wins.
func ensureLiqoTaint(taints []corev1.Taint) []corev1.Taint {
	for _, t := range taints {
		if t.Key == LiqoNotAllowedTaintKey && t.Effect == LiqoNotAllowedTaintEffect {
			return taints
		}
	}
	return append(taints, corev1.Taint{
		Key:    LiqoNotAllowedTaintKey,
		Effect: LiqoNotAllowedTaintEffect,
	})
}

// nonEmptyResources guarantees cpu / memory / pods are present on the
// returned ResourceList. CA's scheduler simulator panics if any of the
// three is unset on the template.
func nonEmptyResources(rl corev1.ResourceList) corev1.ResourceList {
	if rl == nil {
		rl = corev1.ResourceList{}
	}
	if _, ok := rl[corev1.ResourceCPU]; !ok {
		rl[corev1.ResourceCPU] = resource.MustParse("2")
	}
	if _, ok := rl[corev1.ResourceMemory]; !ok {
		rl[corev1.ResourceMemory] = resource.MustParse("4Gi")
	}
	if _, ok := rl[corev1.ResourcePods]; !ok {
		rl[corev1.ResourcePods] = resource.MustParse("110")
	}
	return rl
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
	maps.Copy(out, a)
	maps.Copy(out, b)
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
