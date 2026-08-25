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
	"time"

	agentclient "github.com/netgroup-polito/federation-autoscaler/internal/agent/client"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// BrokerClient wraps the agent mTLS client with helpers specific to the
// comparative test harness.
type BrokerClient struct {
	Raw       *agentclient.Client
	ClusterID string
}

// NewBrokerClientFromIdentity constructs a BrokerClient from an Identity.
func NewBrokerClientFromIdentity(brokerURL, serverName string, id Identity, caFile string) (*BrokerClient, error) {
	c, err := NewBrokerClient(brokerURL, serverName, id, caFile)
	if err != nil {
		return nil, err
	}
	return &BrokerClient{Raw: c, ClusterID: id.ClusterID}, nil
}

// CheckReachable verifies the Broker is reachable by fetching node groups.
func (bc *BrokerClient) CheckReachable(ctx context.Context) error {
	_, err := bc.Raw.GetNodeGroups(ctx)
	return err
}

// GetNodeGroups returns the Broker's current view of node groups.
func (bc *BrokerClient) GetNodeGroups(ctx context.Context) (*brokerapi.NodeGroupListResponse, error) {
	return bc.Raw.GetNodeGroups(ctx)
}

// CreateReservation creates a reservation against the specified provider.
func (bc *BrokerClient) CreateReservation(ctx context.Context, reservationID string, req *brokerapi.ReservationRequest) (*brokerapi.ReservationResponse, error) {
	return bc.Raw.PostReservation(ctx, reservationID, req)
}

// DeleteReservation releases a reservation.
func (bc *BrokerClient) DeleteReservation(ctx context.Context, reservationID string) (*brokerapi.ReleaseResponse, error) {
	return bc.Raw.DeleteReservation(ctx, reservationID)
}

// WaitForPhase polls the reservation (via idempotent re-submission) until it
// reaches the target phase or the context expires. The Broker returns the
// current state on idempotent hits (same X-Reservation-Id).
func (bc *BrokerClient) WaitForPhase(ctx context.Context, reservationID string, req *brokerapi.ReservationRequest, targetPhase string, pollInterval time.Duration) (*brokerapi.ReservationResponse, error) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		resp, err := bc.Raw.PostReservation(ctx, reservationID, req)
		if err != nil {
			return nil, fmt.Errorf("poll reservation %s: %w", reservationID, err)
		}
		if string(resp.Status) == targetPhase {
			return resp, nil
		}
		if isTerminalPhase(string(resp.Status)) {
			return resp, fmt.Errorf("reservation %s reached terminal phase %s (wanted %s)", reservationID, resp.Status, targetPhase)
		}
		select {
		case <-ctx.Done():
			return resp, fmt.Errorf("timed out waiting for reservation %s to reach %s (current: %s)", reservationID, targetPhase, resp.Status)
		case <-ticker.C:
		}
	}
}

func isTerminalPhase(phase string) bool {
	switch phase {
	case "Failed", "Released", "Expired":
		return true
	}
	return false
}
