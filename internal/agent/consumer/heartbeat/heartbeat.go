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

// Package heartbeat drives the consumer-side 15 s heartbeat loop
// (docs/design.md §7.3.3): every interval a Heartbeater POSTs to
// /api/v1/heartbeat so the Broker's in-memory ConsumerRegistry
// remembers this consumer's liqoClusterID — the broker needs that
// value when it mints provider instructions, since the chain
// `liqoctl generate peering-user --consumer-cluster-id <id>` runs on
// the provider with the consumer's Liqo ID as the only handle.
//
// A stalled heartbeat is a "broker is unreachable" signal of the same
// flavour as a stalled instruction poll, so the Heartbeater plumbs its
// success / failure into health.Probe.RecordPoll alongside the poller.
package heartbeat

import (
	"context"
	"errors"
	"time"

	"github.com/go-logr/logr"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agentclient "github.com/netgroup-polito/federation-autoscaler/internal/agent/client"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// DefaultInterval is the cadence at which the heartbeater POSTs to
// /api/v1/heartbeat when Options.Interval is unset (docs/design.md
// §7.3.3 fixes this at 15 s).
const DefaultInterval = 15 * time.Second

// Options bundles the construction-time settings of a Heartbeater.
type Options struct {
	// Client is the Broker HTTP client built in step 7a/7b. Required.
	Client *agentclient.Client

	// ClusterID and LiqoClusterID are stamped on every
	// HeartbeatRequest. Both required.
	ClusterID     string
	LiqoClusterID string

	// Interval overrides DefaultInterval. Mainly useful in tests.
	Interval time.Duration

	// Logger is the structured logger every heartbeat cycle logs
	// through. Defaults to controller-runtime's logger named "heartbeat".
	Logger logr.Logger

	// OnHeartbeatResult, if non-nil, is invoked after every heartbeat
	// attempt with the success / failure outcome. The consumer role
	// wires this to health.Probe.RecordPoll so a stalled heartbeat
	// reddens /readyz.
	OnHeartbeatResult func(success bool)
}

// Heartbeater drives the 15 s heartbeat loop. A single Run goroutine
// is the only entry point; reflecting the single-replica Recreate
// invariant of agent Deployments, Heartbeater is NOT safe for
// concurrent invocation.
type Heartbeater struct {
	client        *agentclient.Client
	clusterID     string
	liqoClusterID string
	interval      time.Duration
	log           logr.Logger
	onResult      func(success bool)
}

// New validates opts and returns a Heartbeater ready to Run. It
// performs no I/O.
func New(opts Options) (*Heartbeater, error) {
	switch {
	case opts.Client == nil:
		return nil, errors.New("heartbeat: Client is required")
	case opts.ClusterID == "":
		return nil, errors.New("heartbeat: ClusterID is required")
	case opts.LiqoClusterID == "":
		return nil, errors.New("heartbeat: LiqoClusterID is required")
	}
	logger := opts.Logger
	if logger.GetSink() == nil {
		logger = log.Log.WithName("heartbeat")
	}
	interval := opts.Interval
	if interval <= 0 {
		interval = DefaultInterval
	}
	return &Heartbeater{
		client:        opts.Client,
		clusterID:     opts.ClusterID,
		liqoClusterID: opts.LiqoClusterID,
		interval:      interval,
		log:           logger,
		onResult:      opts.OnHeartbeatResult,
	}, nil
}

// Run blocks until ctx is cancelled. It heartbeats immediately so the
// broker's ConsumerRegistry sees this consumer as soon as the agent
// boots (which the broker needs before it will accept a POST
// /api/v1/reservations from this consumer), then on every interval
// tick.
func (h *Heartbeater) Run(ctx context.Context) {
	h.log.Info("starting heartbeat poster",
		"interval", h.interval, "clusterID", h.clusterID)

	t := time.NewTicker(h.interval)
	defer t.Stop()

	h.beatOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			h.log.Info("heartbeat poster stopped", "reason", ctx.Err())
			return
		case <-t.C:
			h.beatOnce(ctx)
		}
	}
}

// beatOnce executes one POST /api/v1/heartbeat cycle. Errors log at
// V(1) and notify onResult(false); a successful call notifies
// onResult(true).
func (h *Heartbeater) beatOnce(ctx context.Context) {
	req := &brokerapi.HeartbeatRequest{
		ClusterID:     h.clusterID,
		LiqoClusterID: h.liqoClusterID,
	}
	if _, err := h.client.PostHeartbeat(ctx, req); err != nil {
		if ctx.Err() == nil {
			h.log.V(1).Info("heartbeat failed", "err", err.Error())
		}
		h.notifyResult(false)
		return
	}
	h.notifyResult(true)
}

func (h *Heartbeater) notifyResult(success bool) {
	if h.onResult == nil {
		return
	}
	defer func() { _ = recover() }() // never let a misbehaving callback kill the loop
	h.onResult(success)
}
