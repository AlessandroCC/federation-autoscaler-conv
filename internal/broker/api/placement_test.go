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

package api

import (
	"testing"

	autoscalingv1alpha1 "github.com/netgroup-polito/federation-autoscaler/api/autoscaling/v1alpha1"
	brokerv1alpha1 "github.com/netgroup-polito/federation-autoscaler/api/broker/v1alpha1"
)

// Representative coordinates reused across the latency cases.
const (
	montrealLat, montrealLon = 45.6085, -73.5493  // consumer + the "near" provider
	sydneyLat, sydneyLon     = -33.8688, 151.2093 // the "far" provider
)

// stdAdvCarbon is stdAdv with an advertised current carbon intensity (gCO2/kWh).
func stdAdvCarbon(name string, reserved int32, carbon float64) *brokerv1alpha1.ClusterAdvertisement {
	a := stdAdv(name, reserved, nil)
	c := carbon
	a.Spec.CarbonIntensity = &c
	return a
}

// stdAdvAt is stdAdv with advertised coordinates (the latency input).
func stdAdvAt(name string, reserved int32, lat, lon float64) *brokerv1alpha1.ClusterAdvertisement {
	a := stdAdv(name, reserved, nil)
	a.Spec.Topology = &brokerv1alpha1.Topology{Region: name, Latitude: lat, Longitude: lon}
	return a
}

func withEcoPolicy(s *Server) {
	s.consumers.Touch(consumerCluster, "liqo-c",
		autoscalingv1alpha1.PlacementPolicy{Type: autoscalingv1alpha1.PlacementStrategyEco}, "", nil, nil)
}

// withLatencyPolicy records a Latency-preferring heartbeat WITH a consumer location.
func withLatencyPolicy(s *Server, lat, lon float64) {
	la, lo := lat, lon
	s.consumers.Touch(consumerCluster, "liqo-c",
		autoscalingv1alpha1.PlacementPolicy{Type: autoscalingv1alpha1.PlacementStrategyLatency}, "QC", &la, &lo)
}

// withLatencyPolicyNoLocation records a Latency-preferring heartbeat with NO
// consumer location — the no-op case (R2).
func withLatencyPolicyNoLocation(s *Server) {
	s.consumers.Touch(consumerCluster, "liqo-c",
		autoscalingv1alpha1.PlacementPolicy{Type: autoscalingv1alpha1.PlacementStrategyLatency}, "", nil, nil)
}

// TestNodeGroupsEcoPreference mirrors the price matrix: masking happens only
// when an Eco policy is set AND a carbon-bearing provider has capacity; lowest
// carbon wins and carbon-less providers are a last resort.
func TestNodeGroupsEcoPreference(t *testing.T) {
	t.Run("no policy, carbon set -> all exposed (carbon inert)", func(t *testing.T) {
		s := newDashboardTestServer(t, stdAdvCarbon("p-green", 0, 25), stdAdvCarbon("p-dirty", 0, 650))
		hr := headroomByProvider(callNodeGroups(t, s))
		if hr["p-green"] != 3 || hr["p-dirty"] != 3 {
			t.Errorf("without a policy carbon must not narrow; got %+v", hr)
		}
	})

	t.Run("policy, no carbon -> all exposed (no narrowing)", func(t *testing.T) {
		s := newDashboardTestServer(t, stdAdv("p-a", 0, nil), stdAdv("p-b", 0, nil))
		withEcoPolicy(s)
		hr := headroomByProvider(callNodeGroups(t, s))
		if hr["p-a"] != 3 || hr["p-b"] != 3 {
			t.Errorf("no carbon-bearing provider => no narrowing; got %+v", hr)
		}
	})

	t.Run("policy + carbon -> only greenest grows; carbon-less is last resort", func(t *testing.T) {
		s := newDashboardTestServer(t,
			stdAdvCarbon("p-green", 0, 25),
			stdAdvCarbon("p-dirty", 0, 650),
			stdAdv("p-nocarbon", 0, nil))
		withEcoPolicy(s)
		hr := headroomByProvider(callNodeGroups(t, s))
		if hr["p-green"] != 3 {
			t.Errorf("greenest provider must keep head-room; got %+v", hr)
		}
		if hr["p-dirty"] != 0 || hr["p-nocarbon"] != 0 {
			t.Errorf("dirtier and carbon-less providers must be masked; got %+v", hr)
		}
	})

	t.Run("greedy spill: greenest full -> next-greenest grows", func(t *testing.T) {
		s := newDashboardTestServer(t,
			stdAdvCarbon("p-green", 3, 25), // fully reserved
			stdAdvCarbon("p-dirty", 0, 650))
		withEcoPolicy(s)
		hr := headroomByProvider(callNodeGroups(t, s))
		if hr["p-green"] != 0 {
			t.Errorf("exhausted greenest must have no head-room; got %+v", hr)
		}
		if hr["p-dirty"] != 3 {
			t.Errorf("next-greenest must be promoted to growable; got %+v", hr)
		}
	})
}

// TestNodeGroupsLatencyPreference covers the closest-first greedy plus the
// genuinely-new branch: a consumer with no location yields NO masking (R2).
func TestNodeGroupsLatencyPreference(t *testing.T) {
	t.Run("policy + coords -> only closest grows; far + coordless masked", func(t *testing.T) {
		s := newDashboardTestServer(t,
			stdAdvAt("p-near", 0, montrealLat, montrealLon),
			stdAdvAt("p-far", 0, sydneyLat, sydneyLon),
			stdAdv("p-nocoords", 0, nil))
		withLatencyPolicy(s, montrealLat, montrealLon)
		hr := headroomByProvider(callNodeGroups(t, s))
		if hr["p-near"] != 3 {
			t.Errorf("closest provider must keep head-room; got %+v", hr)
		}
		if hr["p-far"] != 0 || hr["p-nocoords"] != 0 {
			t.Errorf("farther and coordless providers must be masked; got %+v", hr)
		}
	})

	t.Run("consumer has NO location -> no masking (all exposed)", func(t *testing.T) {
		s := newDashboardTestServer(t,
			stdAdvAt("p-near", 0, montrealLat, montrealLon),
			stdAdvAt("p-far", 0, sydneyLat, sydneyLon))
		withLatencyPolicyNoLocation(s)
		hr := headroomByProvider(callNodeGroups(t, s))
		if hr["p-near"] != 3 || hr["p-far"] != 3 {
			t.Errorf("no consumer location must leave all providers exposed; got %+v", hr)
		}
	})

	t.Run("greedy spill: closest full -> next-closest grows", func(t *testing.T) {
		s := newDashboardTestServer(t,
			stdAdvAt("p-near", 3, montrealLat, montrealLon), // fully reserved
			stdAdvAt("p-far", 0, sydneyLat, sydneyLon))
		withLatencyPolicy(s, montrealLat, montrealLon)
		hr := headroomByProvider(callNodeGroups(t, s))
		if hr["p-near"] != 0 {
			t.Errorf("exhausted closest must have no head-room; got %+v", hr)
		}
		if hr["p-far"] != 3 {
			t.Errorf("next-closest must be promoted to growable; got %+v", hr)
		}
	})
}
