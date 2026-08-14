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

package main

import (
	"context"
	"math/rand"
	"time"

	autoscalingv1alpha1 "github.com/netgroup-polito/federation-autoscaler/api/autoscaling/v1alpha1"
	agentclient "github.com/netgroup-polito/federation-autoscaler/internal/agent/client"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// consumerPayload builds a deterministic-per-index synthetic
// HeartbeatRequest. Placement.Type is fixed to PlacementStrategyRandom per
// the experiment's requirement to exercise the "random" policy
// (api/autoscaling/v1alpha1/consumerpolicy_types.go) — the Broker reads
// this back off the ConsumerRegistry on every GET /api/v1/nodegroups this
// consumer subsequently issues (internal/broker/api/nodegroups.go).
func consumerPayload(clusterID string, index int, cfg *Config) *brokerapi.HeartbeatRequest {
	rng := rand.New(rand.NewSource(cfg.Seed + int64(index) + 1_000_000)) // offset so consumer/provider streams never collide
	region := providerRegions[index%len(providerRegions)]
	lat := region.Lat + (rng.Float64()-0.5)*0.5
	lon := region.Lon + (rng.Float64()-0.5)*0.5

	return &brokerapi.HeartbeatRequest{
		ClusterID:     clusterID,
		LiqoClusterID: clusterID + "-liqo",
		Placement:     &autoscalingv1alpha1.PlacementPolicy{Type: autoscalingv1alpha1.PlacementStrategyRandom},
		Region:        region.Region,
		City:          region.City,
		Latitude:      &lat,
		Longitude:     &lon,
	}
}

// runConsumer drives one logical Consumer's traffic for the whole run: a
// retried warm-up heartbeat, then the steady-state heartbeat and
// (optionally) instruction-poll tickers immediately, and the evaluation
// ticker only once evalGate is closed by the orchestrator (main.go) — i.e.
// once every consumer's warm-up heartbeat has landed, per the required
// lifecycle (README "Warm-up Phase").
func runConsumer(ctx context.Context, cfg *Config, id agentIdentity, index int, collector *Collector, warmupCh chan<- warmupOutcome, evalGate <-chan struct{}) {
	client, err := newAgentClient(cfg, id, brokerCAFile)
	if err != nil {
		warmupCh <- warmupOutcome{ClusterID: id.ClusterID, OK: false, Err: err}
		return
	}

	req := consumerPayload(id.ClusterID, index, cfg)

	warmupCtx, cancel := context.WithTimeout(ctx, cfg.WarmupTimeout)
	ok := retryUntilSuccess(warmupCtx, func() error {
		return doHeartbeat(ctx, client, id, req, collector, PhaseWarmup)
	})
	cancel()
	warmupCh <- warmupOutcome{ClusterID: id.ClusterID, OK: ok, Err: ctx.Err()}
	if !ok || ctx.Err() != nil {
		return
	}

	hbTicker := time.NewTicker(cfg.HeartbeatInterval)
	defer hbTicker.Stop()

	var instrTicker *time.Ticker
	var instrC <-chan time.Time
	if cfg.InstructionPoll {
		instrTicker = time.NewTicker(cfg.InstructionPollInterval)
		defer instrTicker.Stop()
		instrC = instrTicker.C
	}

	// The evaluation ticker only starts once every consumer has completed
	// its warm-up heartbeat, so GET /api/v1/nodegroups traffic never begins
	// while some consumers are still unregistered — but a consumer's own
	// heartbeat/instruction-poll traffic starts immediately, matching a real
	// Consumer Agent that heartbeats on its own cadence regardless of what
	// other clusters are doing.
	var evalTicker *time.Ticker
	var evalC <-chan time.Time
	evalStarted := false
	startEval := func() {
		if evalStarted {
			return
		}
		evalStarted = true
		evalTicker = time.NewTicker(cfg.ConsumerEvalInterval)
		evalC = evalTicker.C
	}
	defer func() {
		if evalTicker != nil {
			evalTicker.Stop()
		}
	}()

	localEvalGate := evalGate
	for {
		select {
		case <-ctx.Done():
			return
		case <-localEvalGate:
			localEvalGate = nil // consume once
			startEval()
		case <-hbTicker.C:
			_ = doHeartbeat(ctx, client, id, req, collector, PhaseMeasurement)
		case <-instrC:
			_ = doInstructionPoll(ctx, client, "consumer", id, collector, PhaseMeasurement)
		case <-evalC:
			_ = doEvaluation(ctx, client, id, collector, PhaseMeasurement)
		}
	}
}

func doHeartbeat(ctx context.Context, client *agentclient.Client, id agentIdentity, req *brokerapi.HeartbeatRequest, collector *Collector, phase Phase) error {
	start := time.Now()
	_, err := client.PostHeartbeat(ctx, req)
	latency := time.Since(start)
	collector.Add(recordFor("consumer", id.ClusterID, OpHeartbeat, start, latency, err, phase))
	return err
}

func doEvaluation(ctx context.Context, client *agentclient.Client, id agentIdentity, collector *Collector, phase Phase) error {
	start := time.Now()
	_, err := client.GetNodeGroups(ctx)
	latency := time.Since(start)
	collector.Add(recordFor("consumer", id.ClusterID, OpEvaluation, start, latency, err, phase))
	return err
}
