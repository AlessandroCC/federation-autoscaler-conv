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

package instructions

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	clientfake "sigs.k8s.io/controller-runtime/pkg/client/fake"

	autoscalingv1alpha1 "github.com/netgroup-polito/federation-autoscaler/api/autoscaling/v1alpha1"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

func reconcileInstruction() *brokerapi.InstructionView {
	return &brokerapi.InstructionView{
		ID:            "reconcile-1",
		Kind:          string(autoscalingv1alpha1.ReservationInstructionReconcile),
		ReservationID: testResID,
	}
}

// newFakeKubeClientWithVNS builds a fake client whose scheme knows
// about autoscaling/v1alpha1 (so VirtualNodeState CRs can be seeded
// and listed).
func newFakeKubeClientWithVNS(objs ...ctrlclient.Object) ctrlclient.Client {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = autoscalingv1alpha1.AddToScheme(scheme)
	return clientfake.NewClientBuilder().WithScheme(scheme).WithObjects(objs...).Build()
}

func vns(name string, spec autoscalingv1alpha1.VirtualNodeStateSpec, status autoscalingv1alpha1.VirtualNodeStateStatus) *autoscalingv1alpha1.VirtualNodeState {
	return &autoscalingv1alpha1.VirtualNodeState{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec:       spec,
		Status:     status,
	}
}

func TestReconcile_EmptyClusterReturnsEmptyList(t *testing.T) {
	c := newFakeKubeClientWithVNS()
	h := NewReconcileHandler(ReconcileConfig{
		LocalClient: c,
		Namespace:   testNamespace,
	})
	res, err := h(context.Background(), reconcileInstruction())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if res.Status != brokerapi.ResultStatusSucceeded {
		t.Fatalf("want Succeeded, got %s", res.Status)
	}
	if res.Payload == nil || res.Payload.Kind != brokerapi.PayloadKindReconcile {
		t.Fatalf("want ReconcilePayload, got %+v", res.Payload)
	}
	if len(res.Payload.VirtualNodeStates) != 0 {
		t.Errorf("want empty VirtualNodeStates, got %+v", res.Payload.VirtualNodeStates)
	}
}

func TestReconcile_ProjectsVNSCRs(t *testing.T) {
	now := metav1.Now()
	v1 := vns("vns-res-1-0",
		autoscalingv1alpha1.VirtualNodeStateSpec{
			ReservationID: "res-1", ChunkIndex: 0, NodeGroupID: "p1/standard",
			ProviderClusterID: "p1", ProviderLiqoClusterID: "liqo-p1",
		},
		autoscalingv1alpha1.VirtualNodeStateStatus{
			Phase:              "Ready",
			VirtualNodeName:    "liqo-virt-1",
			ResourceSliceName:  "rs-res-1",
			LastTransitionTime: &now,
		},
	)
	v2 := vns("vns-res-2-0",
		autoscalingv1alpha1.VirtualNodeStateSpec{
			ReservationID: "res-2", ChunkIndex: 0, NodeGroupID: "p2/gpu",
			ProviderClusterID: "p2", ProviderLiqoClusterID: "liqo-p2",
		},
		autoscalingv1alpha1.VirtualNodeStateStatus{Phase: "Pending"},
	)

	c := newFakeKubeClientWithVNS(v1, v2)
	h := NewReconcileHandler(ReconcileConfig{
		LocalClient: c,
		Namespace:   testNamespace,
	})
	res, err := h(context.Background(), reconcileInstruction())
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	views := res.Payload.VirtualNodeStates
	if len(views) != 2 {
		t.Fatalf("want 2 views, got %d: %+v", len(views), views)
	}
	// Sort-insensitive lookup by ReservationID.
	byID := map[string]brokerapi.ReconcileVirtualNodeStateView{}
	for _, v := range views {
		byID[v.ReservationID] = v
	}
	if got := byID["res-1"]; got.VirtualNodeName != "liqo-virt-1" ||
		got.ResourceSlice != "rs-res-1" || got.Phase != "Ready" ||
		got.NodeGroupID != "p1/standard" || got.LastTransition == nil {
		t.Errorf("res-1 projection mismatch: %+v", got)
	}
	if got := byID["res-2"]; got.Phase != "Pending" || got.NodeGroupID != "p2/gpu" {
		t.Errorf("res-2 projection mismatch: %+v", got)
	}
}

func TestReconcile_RejectsWrongKind(t *testing.T) {
	h := NewReconcileHandler(ReconcileConfig{
		LocalClient: newFakeKubeClientWithVNS(),
		Namespace:   testNamespace,
	})
	in := reconcileInstruction()
	in.Kind = kindPeer
	_, err := h(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "unexpected kind") {
		t.Fatalf("want unexpected-kind error, got %v", err)
	}
}

func TestReconcile_RejectsNilInstruction(t *testing.T) {
	h := NewReconcileHandler(ReconcileConfig{
		LocalClient: newFakeKubeClientWithVNS(),
		Namespace:   testNamespace,
	})
	if _, err := h(context.Background(), nil); err == nil {
		t.Fatal("want error for nil instruction")
	}
}

func TestReconcile_RejectsMissingLocalClient(t *testing.T) {
	h := NewReconcileHandler(ReconcileConfig{Namespace: testNamespace})
	_, err := h(context.Background(), reconcileInstruction())
	if err == nil || !strings.Contains(err.Error(), "LocalClient is nil") {
		t.Fatalf("want LocalClient-nil error, got %v", err)
	}
}
