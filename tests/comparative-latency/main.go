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

// Command comparative-latency runs Phase A (Random) vs Phase B (Latency) with
// full automation: creates Kind clusters, deploys all components, applies tc
// netem delays, runs the experiment, collects results, and cleans up.
//
// Usage:
//
//	go run ./tests/comparative-latency/ --config tests/configs/latency.yaml
package main

import (
	"context"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"os/signal"
	"sort"
	"sync"
	"time"

	brokerv1alpha1 "github.com/netgroup-polito/federation-autoscaler/api/broker/v1alpha1"
	agentclient "github.com/netgroup-polito/federation-autoscaler/internal/agent/client"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
	"github.com/netgroup-polito/federation-autoscaler/tests/testlib"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

// run holds the actual program body so orch.Teardown (deferred) always
// executes on any error path, including a Setup failure — log.Fatal in main
// calls os.Exit, which would otherwise skip deferred cleanup and leave Kind
// clusters orphaned.
func run() error {
	configPath, keepClusters, skipBuild, runID := parseFlags()

	cfg, err := testlib.LoadAutoConfig(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	orch, err := testlib.NewOrchestrator(cfg, "comparative-latency", keepClusters, skipBuild, runID)
	if err != nil {
		return fmt.Errorf("orchestrator: %w", err)
	}

	defer orch.Teardown(context.Background())

	if err := orch.Setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	if err := runExperiment(ctx, orch); err != nil {
		return fmt.Errorf("experiment failed: %w", err)
	}
	return nil
}

func runExperiment(ctx context.Context, orch *testlib.Orchestrator) error {
	startTime := time.Now()
	cfg := orch.Config
	exp := cfg.Experiment
	clients := orch.Clients

	// Collect probe endpoints from initial nodegroups.
	ngResp, err := clients.Broker.GetNodeGroups(ctx)
	if err != nil {
		return fmt.Errorf("get nodegroups: %w", err)
	}
	probeEndpoints := make(map[string]string)
	for _, ng := range ngResp.NodeGroups {
		if ng.ProbeEndpoint != "" {
			probeEndpoints[ng.ProviderClusterID] = ng.ProbeEndpoint
		}
	}
	if len(probeEndpoints) < 2 {
		return fmt.Errorf("need >= 2 providers with ProbeEndpoint, got %d (check udpecho deployment)", len(probeEndpoints))
	}
	log.Printf("probe endpoints: %v", probeEndpoints)

	// Clear stale policies.
	if err := clients.SetPolicyAll(ctx, "None", 0); err != nil {
		return fmt.Errorf("clear policies: %w", err)
	}

	// Apply tc delays BEFORE any phase so both phases run under identical
	// network conditions — the only variable between them is the policy.
	log.Println("=== APPLY TC DELAYS ===")
	var consumerTCs []*testlib.TCConsumerDelay
	var providerTCs []*testlib.TCDelayKind
	if len(exp.ConsumerDelays) > 0 {
		// Consumer-side tc: per-consumer × per-provider delay matrix.
		for _, cd := range exp.ConsumerDelays {
			containerName := orch.ConsumerContainerName(cd.ConsumerIndex)
			var entries []testlib.ProviderDelayEntry
			for _, pd := range cd.ProviderDelays {
				provContainer := orch.ProviderContainerName(pd.ProviderIndex)
				provIP, err := testlib.ContainerIP(ctx, provContainer)
				if err != nil {
					return fmt.Errorf("get IP for provider-%d (%s): %w", pd.ProviderIndex, provContainer, err)
				}
				entries = append(entries, testlib.ProviderDelayEntry{
					ProviderIP: provIP,
					DelayMs:    pd.DelayMs,
					Label:      fmt.Sprintf("provider-%d", pd.ProviderIndex),
				})
			}
			tc := &testlib.TCConsumerDelay{
				ContainerName:  containerName,
				Interface:      "eth0",
				ProviderDelays: entries,
			}
			if err := tc.Apply(); err != nil {
				for _, applied := range consumerTCs {
					_ = applied.Restore()
				}
				return fmt.Errorf("apply consumer tc on %s: %w", containerName, err)
			}
			consumerTCs = append(consumerTCs, tc)
		}
		defer func() {
			for _, tc := range consumerTCs {
				log.Printf("  restoring consumer tc on %s", tc.ContainerName)
				if err := tc.Restore(); err != nil {
					log.Printf("  tc restore error on %s: %v", tc.ContainerName, err)
				}
			}
		}()
	} else {
		// Provider-side tc: uniform delay per provider (legacy mode).
		for _, tcd := range exp.TCDelaysAuto {
			containerName := orch.ProviderContainerName(tcd.ProviderIndex)
			iface := tcd.Interface
			if iface == "" {
				iface = "eth0"
			}
			tc := &testlib.TCDelayKind{
				ContainerName: containerName,
				Interface:     iface,
				DelayMs:       tcd.DelayMs,
			}
			log.Printf("  tc: %s +%dms", containerName, tcd.DelayMs)
			if err := tc.Apply(); err != nil {
				for _, applied := range providerTCs {
					_ = applied.Restore()
				}
				return fmt.Errorf("apply tc on %s: %w", containerName, err)
			}
			providerTCs = append(providerTCs, tc)
		}
		defer func() {
			for _, tc := range providerTCs {
				log.Printf("  restoring tc on %s", tc.ContainerName)
				if err := tc.Restore(); err != nil {
					log.Printf("  tc restore error on %s: %v", tc.ContainerName, err)
				}
			}
		}()
	}

	var allSelections []testlib.SelectionRecord
	var allProbes []testlib.ProbeRecord
	var allReservations []testlib.ReservationRecord
	mode := exp.Mode

	// Start latency refresh for both phases (same background jitter).
	latencyRefreshCtx, cancelLatencyRefresh := context.WithCancel(ctx)
	var latencyWG sync.WaitGroup
	latencyWG.Add(1)
	go func() {
		defer latencyWG.Done()
		refreshLatency(latencyRefreshCtx, exp, consumerTCs, providerTCs)
	}()

	// --- Phase A: Random ---
	log.Println("=== PHASE A: Random ===")
	if err := clients.SetPolicyAll(ctx, "Random", exp.PolicyPropagationWait); err != nil {
		return fmt.Errorf("set Random: %w", err)
	}

	var phaseASel []testlib.SelectionRecord
	var phaseAProbe []testlib.ProbeRecord
	if mode == "reserve" {
		var phaseARes []testlib.ReservationRecord
		phaseASel, phaseAProbe, phaseARes, err = runReservePhase(ctx, orch, clients, probeEndpoints, testlib.PhaseA, "Random")
		allReservations = append(allReservations, phaseARes...)
	} else {
		phaseASel, phaseAProbe, err = runLatencyPhase(ctx, orch, clients, probeEndpoints, testlib.PhaseA, "Random")
	}
	if err != nil {
		return fmt.Errorf("phase A: %w", err)
	}
	allSelections = append(allSelections, phaseASel...)
	allProbes = append(allProbes, phaseAProbe...)
	log.Printf("phase A complete: %d samples", len(phaseASel))

	// --- Transition: switch to Latency (tc delays already active) ---
	log.Println("=== TRANSITION ===")
	if err := clients.SetPolicyAll(ctx, "Latency", exp.PolicyPropagationWait); err != nil {
		return fmt.Errorf("set Latency: %w", err)
	}

	// --- Phase B: Latency ---
	log.Println("=== PHASE B: Latency ===")
	var phaseBSel []testlib.SelectionRecord
	var phaseBProbe []testlib.ProbeRecord
	if mode == "reserve" {
		var phaseBRes []testlib.ReservationRecord
		phaseBSel, phaseBProbe, phaseBRes, err = runReservePhase(ctx, orch, clients, probeEndpoints, testlib.PhaseB, "Latency")
		allReservations = append(allReservations, phaseBRes...)
	} else {
		phaseBSel, phaseBProbe, err = runLatencyPhase(ctx, orch, clients, probeEndpoints, testlib.PhaseB, "Latency")
	}
	cancelLatencyRefresh()
	latencyWG.Wait()
	if err != nil {
		return fmt.Errorf("phase B: %w", err)
	}
	allSelections = append(allSelections, phaseBSel...)
	allProbes = append(allProbes, phaseBProbe...)
	log.Printf("phase B complete: %d samples", len(phaseBSel))

	// --- Cleanup ---
	log.Println("=== EXPERIMENT CLEANUP ===")
	if err := clients.SetPolicyAll(ctx, "None", 0); err != nil {
		log.Printf("cleanup: clear policies: %v", err)
	}

	// --- Write results ---
	outputDir := orch.OutputDir
	log.Printf("writing results to %s", outputDir)
	if err := testlib.WriteSelectionCSV(outputDir, "selections.csv", allSelections); err != nil {
		return fmt.Errorf("write selections CSV: %w", err)
	}
	if err := testlib.WriteProbeCSV(outputDir, "probes.csv", allProbes); err != nil {
		return fmt.Errorf("write probes CSV: %w", err)
	}
	if mode == "reserve" && len(allReservations) > 0 {
		if err := testlib.WriteReservationCSV(outputDir, "reservations.csv", allReservations); err != nil {
			return fmt.Errorf("write reservations CSV: %w", err)
		}
	}

	brokerURL, _ := orch.BrokerURL(ctx)
	consoleURL, _ := orch.ConsoleURL(ctx, 0)

	summary := testlib.ExperimentSummary{
		RunID:              orch.RunID,
		TestType:           "comparative-latency",
		StartTime:          startTime,
		EndTime:            time.Now(),
		ConsumerID:         clients.Identity.ClusterID,
		ConsumerCertFP:     clients.CertFP,
		BrokerURL:          brokerURL,
		ConsoleURL:         consoleURL,
		ProviderCount:      cfg.Providers,
		IterationsPerPhase: exp.Iterations,
		PhaseAPolicy:       "Random",
		PhaseBPolicy:       "Latency",
		PhaseASummary:      summarizeLatencyPhase(phaseASel, phaseAProbe),
		PhaseBSummary:      summarizeLatencyPhase(phaseBSel, phaseBProbe),
	}
	if err := testlib.WriteJSONFile(outputDir, "summary.json", summary); err != nil {
		return fmt.Errorf("write summary: %w", err)
	}
	if err := testlib.WriteSummaryMarkdown(outputDir, summary); err != nil {
		return fmt.Errorf("write markdown: %w", err)
	}

	log.Println("=== DONE ===")
	printSummary(summary)
	return nil
}

// runLatencyPhase drives one probe/select cycle per iteration. Consumers are
// dispatched concurrently (one goroutine each) within an iteration, mirroring
// how independent real Consumer Agents behave in production. All consumers
// in an iteration are joined before the next iteration's phasePause, so the
// iteration cadence is unchanged; only the per-consumer work inside each
// iteration is now parallel instead of serial.
func runLatencyPhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, endpoints map[string]string, phase, policy string) ([]testlib.SelectionRecord, []testlib.ProbeRecord, error) {
	exp := orch.Config.Experiment
	var mu sync.Mutex
	var selections []testlib.SelectionRecord
	var probes []testlib.ProbeRecord

	consumerIDs := make([]string, 0, len(clients.Consoles))
	for id := range clients.Consoles {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	for i := 1; i <= exp.Iterations; i++ {
		if ctx.Err() != nil {
			return selections, probes, ctx.Err()
		}

		var wg sync.WaitGroup
		for _, consID := range consumerIDs {
			wg.Add(1)
			go func(consID string) {
				defer wg.Done()
				runLatencyConsumerIteration(ctx, clients, endpoints, phase, policy, i, consID, &mu, &selections, &probes)
			}(consID)
		}
		wg.Wait()

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return selections, probes, err
			}
		}
	}

	return selections, probes, nil
}

// runLatencyConsumerIteration is the per-consumer, per-iteration body of
// runLatencyPhase, safe to run concurrently with other consumers' calls: it
// only touches shared state (selections, probes) behind mu.
func runLatencyConsumerIteration(ctx context.Context, clients *testlib.ExperimentClients, endpoints map[string]string, phase, policy string, i int, consID string, mu *sync.Mutex, selections *[]testlib.SelectionRecord, probes *[]testlib.ProbeRecord) {
	console := clients.Consoles[consID]
	broker := clients.BrokerFor(consID)
	start := time.Now()
	rec := testlib.SelectionRecord{
		Timestamp:  start,
		ConsumerID: consID,
		Phase:      phase,
		Policy:     policy,
		Iteration:  i,
	}
	appendSel := func(r testlib.SelectionRecord) {
		mu.Lock()
		*selections = append(*selections, r)
		mu.Unlock()
	}
	appendProbe := func(p testlib.ProbeRecord) {
		mu.Lock()
		*probes = append(*probes, p)
		mu.Unlock()
	}

	ngResp, err := broker.GetNodeGroups(ctx)
	if err != nil {
		rec.Outcome = "error"
		rec.ErrorMessage = fmt.Sprintf("get nodegroups: %v", err)
		rec.DurationMs = msSince(start)
		appendSel(rec)
		log.Printf("[%s] iter %d %s: nodegroups error: %v", phase, i, consID, err)
		return
	}

	if ngResp.LatencyShortlist {
		growable := testlib.GrowableNodeGroups(ngResp.NodeGroups)
		var candidates []testlib.ProbeCandidate
		for _, ng := range growable {
			if ep, ok := endpoints[ng.ProviderClusterID]; ok {
				candidates = append(candidates, testlib.ProbeCandidate{
					ProviderClusterID: ng.ProviderClusterID,
					Endpoint:          ep,
				})
			}
		}
		if len(candidates) == 0 {
			rec.Outcome = "no-candidates"
			rec.ErrorMessage = "no growable with ProbeEndpoint"
			rec.DurationMs = msSince(start)
			appendSel(rec)
			return
		}

		probeResp, err := console.Probe(ctx, candidates)
		if err != nil {
			rec.Outcome = "probe-error"
			rec.ErrorMessage = err.Error()
			rec.DurationMs = msSince(start)
			appendSel(rec)
			return
		}

		rec.SelectedID = probeResp.Chosen
		if ng := testlib.FindNodeGroupByProvider(ngResp.NodeGroups, probeResp.Chosen); ng != nil {
			rec.NodeGroupID = ng.ID
		}
		if rtt, ok := probeResp.RTTs[probeResp.Chosen]; ok {
			rec.RTTMs = rtt
		}
		rec.Outcome = "success"
		rec.DurationMs = msSince(start)
		appendSel(rec)

		appendProbe(testlib.ProbeRecord{
			Timestamp:  start,
			ConsumerID: consID,
			Phase:      phase,
			Policy:     policy,
			Iteration:  i,
			Chosen:     probeResp.Chosen,
			RTTs:       probeResp.RTTs,
			DurationMs: probeResp.Duration,
		})
	} else {
		winner := testlib.FindWinner(ngResp.NodeGroups)
		if winner == nil {
			rec.Outcome = "no-winner"
			rec.DurationMs = msSince(start)
			appendSel(rec)
			return
		}

		rec.SelectedID = winner.ProviderClusterID
		rec.NodeGroupID = winner.ID

		if ep, ok := endpoints[winner.ProviderClusterID]; ok {
			probeResp, err := console.Probe(ctx, []testlib.ProbeCandidate{{
				ProviderClusterID: winner.ProviderClusterID,
				Endpoint:          ep,
			}})
			if err == nil {
				if rtt, ok := probeResp.RTTs[winner.ProviderClusterID]; ok {
					rec.RTTMs = rtt
				}
				appendProbe(testlib.ProbeRecord{
					Timestamp:  start,
					ConsumerID: consID,
					Phase:      phase,
					Policy:     policy,
					Iteration:  i,
					Chosen:     winner.ProviderClusterID,
					RTTs:       probeResp.RTTs,
					DurationMs: probeResp.Duration,
				})
			}
		}

		rec.Outcome = "success"
		rec.DurationMs = msSince(start)
		appendSel(rec)
	}

	log.Printf("[%s] iter %02d %s: winner=%-20s rtt=%8.2fms shortlist=%v",
		phase, i, consID, rec.SelectedID, rec.RTTMs, ngResp.LatencyShortlist)
}

// latencyReservePhaseState is the shared, mutex-protected state one
// runReservePhase call accumulates across all consumers and iterations. Every
// consumer's own entries in active/seqNo are only ever touched by that
// consumer's own goroutine, but Go maps are not safe for concurrent access
// from multiple goroutines even across disjoint keys, so every access —
// reads included — goes through mu.
type latencyReservePhaseState struct {
	mu           sync.Mutex
	selections   []testlib.SelectionRecord
	probes       []testlib.ProbeRecord
	reservations []testlib.ReservationRecord
	active       map[string]*testlib.ConsumerReservation
	seqNo        map[string]int
}

func newLatencyReservePhaseState() *latencyReservePhaseState {
	return &latencyReservePhaseState{
		active: make(map[string]*testlib.ConsumerReservation),
		seqNo:  make(map[string]int),
	}
}

func (s *latencyReservePhaseState) addSelection(sel testlib.SelectionRecord) {
	s.mu.Lock()
	s.selections = append(s.selections, sel)
	s.mu.Unlock()
}

func (s *latencyReservePhaseState) addProbe(p testlib.ProbeRecord) {
	s.mu.Lock()
	s.probes = append(s.probes, p)
	s.mu.Unlock()
}

func (s *latencyReservePhaseState) addReservation(res testlib.ReservationRecord) {
	s.mu.Lock()
	s.reservations = append(s.reservations, res)
	s.mu.Unlock()
}

func (s *latencyReservePhaseState) getActive(consID string) *testlib.ConsumerReservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[consID]
}

func (s *latencyReservePhaseState) setActive(consID string, res *testlib.ConsumerReservation) {
	s.mu.Lock()
	s.active[consID] = res
	s.mu.Unlock()
}

func (s *latencyReservePhaseState) deleteActive(consID string) {
	s.mu.Lock()
	delete(s.active, consID)
	s.mu.Unlock()
}

func (s *latencyReservePhaseState) nextSeq(consID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.seqNo[consID]
	s.seqNo[consID] = seq + 1
	return seq
}

// runReservePhase drives one reserve/keep/switch cycle per iteration.
// Consumers are dispatched concurrently (one goroutine each) within an
// iteration, mirroring how independent real Consumer Agents behave in
// production — each probes/reserves/releases on its own timeline, none
// blocks behind another's Liqo peering. All consumers in an iteration are
// joined before the next iteration's phasePause, so the iteration cadence is
// unchanged; only the per-consumer work inside each iteration is now
// parallel instead of serial.
func runReservePhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, endpoints map[string]string, phase, policy string) ([]testlib.SelectionRecord, []testlib.ProbeRecord, []testlib.ReservationRecord, error) {
	exp := orch.Config.Experiment
	pollInterval := exp.ReservationPoll

	consumerIDs := make([]string, 0, len(clients.Consoles))
	for id := range clients.Consoles {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	state := newLatencyReservePhaseState()

	defer func() {
		for consID, res := range state.active {
			if res == nil {
				continue
			}
			log.Printf("[%s] cleanup: releasing %s (%s)", phase, res.ReservationID, consID)
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			if err := clients.BrokerFor(consID).ReleaseAndWait(cleanupCtx, res.ReservationID, res.Request, pollInterval); err != nil {
				log.Printf("[%s] cleanup: release error %s: %v", phase, res.ReservationID, err)
			}
			cancel()
		}
	}()

	for i := 1; i <= exp.Iterations; i++ {
		if ctx.Err() != nil {
			return state.selections, state.probes, state.reservations, ctx.Err()
		}

		var wg sync.WaitGroup
		for _, consID := range consumerIDs {
			wg.Add(1)
			go func(consID string) {
				defer wg.Done()
				runLatencyReserveConsumerIteration(ctx, orch, clients, endpoints, phase, policy, i, consID, pollInterval, exp, state)
			}(consID)
		}
		wg.Wait()

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return state.selections, state.probes, state.reservations, err
			}
		}
	}

	return state.selections, state.probes, state.reservations, nil
}

// maxReserveRaceRetries bounds how many times a consumer re-picks (re-probing
// if needed) after losing a race for the last chunk on its chosen provider:
// two consumers can both read the same "provider X has room" snapshot, only
// one PostReservation lands, and the loser's view is now stale. A fresh
// GetNodeGroups naturally exposes the next-best candidate — the Broker only
// ever leaves providers with headroom unmasked — so retrying mirrors what
// the real, continuously-reconciling ResourceRequest controller / Cluster
// Autoscaler would do on its next tick, just resolved within this iteration.
//
// 5 was tight at high concurrency: with many consumers converging on a few
// exposed candidates (Eco's single best-with-headroom, Latency's top-3
// shortlist), a consumer unlucky in the spread cascade could exhaust its
// budget before ever reaching a provider with room — observed as ~10% of
// iterations failing on pure capacity exhaustion in a 30-consumer/70-provider
// run. Raised to give more room to find a free slot; the cost only lands on
// iterations with real contention.
const maxReserveRaceRetries = 10

// raceRetryBackoff is how long to wait before retrying after a 429
// (per-cluster rate limit — 10 burst / 5rps, see internal/broker/api's
// RateLimitMiddleware). Unlike a lost capacity race, hammering again
// immediately just refires the same limiter; the token bucket refills at
// 5/s, so this leaves ample margin. 409 (capacity) and 5xx (e.g. a
// provider's advertisement gone stale) get no backoff — the retry loop
// already re-reads GetNodeGroups from scratch, which is the correct
// response to both: a different provider, or the same one once fresh.
const raceRetryBackoff = 750 * time.Millisecond

// runLatencyReserveConsumerIteration is the per-consumer, per-iteration body
// of runReservePhase, safe to run concurrently with other consumers' calls:
// every access to shared state goes through state's own locking.
func runLatencyReserveConsumerIteration(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, endpoints map[string]string, phase, policy string, i int, consID string, pollInterval time.Duration, exp testlib.TestParams, state *latencyReservePhaseState) {
	console := clients.Consoles[consID]
	broker := clients.BrokerFor(consID)
	start := time.Now()

	var prevProvider string
	var releaseMs float64
	var initialProvider string

	for attempt := 0; ; attempt++ {
		ngResp, err := broker.GetNodeGroups(ctx)
		if err != nil {
			state.addSelection(testlib.SelectionRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				Outcome:           "error",
				ErrorMessage:      fmt.Sprintf("get nodegroups: %v", err),
				DurationMs:        msSince(start),
				InitialProviderID: initialProvider,
				RetryCount:        attempt,
			})
			log.Printf("[%s] iter %d %s: nodegroups error: %v", phase, i, consID, err)
			return
		}

		// Determine winner via probing (latency shortlist) or single winner.
		var chosenProviderID, chosenNodeGroupID string
		var chosenRTT float64
		var probeRec *testlib.ProbeRecord

		// Read the consumer's current reservation up front, BEFORE the
		// no-candidates / no-winner branches below. A fully booked federation
		// (every node group at MaxSize == CurrentReserved, so nothing is
		// growable) says nothing about the reservation this consumer already
		// holds — that one is still Peered and serving. Deciding "no winner ⇒
		// failure" without looking at it recorded a healthy consumer as a
		// failure on every iteration once capacity ran out.
		cur := state.getActive(consID)

		// keepNoAlternative records the iteration as a keep on the current
		// reservation because the Broker offered nothing to move to. Mirrors
		// the record pair built by the regular !shouldSwitch path below.
		keepNoAlternative := func(reason string) {
			state.addSelection(testlib.SelectionRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				SelectedID:        cur.ProviderClusterID,
				NodeGroupID:       cur.NodeGroupID,
				ReservationID:     cur.ReservationID,
				Outcome:           testlib.OutcomeKeepNoAlternative,
				ErrorMessage:      reason,
				DurationMs:        msSince(start),
				InitialProviderID: initialProvider,
				RetryCount:        attempt,
			})
			state.addReservation(testlib.ReservationRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				ReservationID:     cur.ReservationID,
				ProviderClusterID: cur.ProviderClusterID,
				NodeGroupID:       cur.NodeGroupID,
				Action:            "keep",
				FinalPhase:        "Peered",
				Outcome:           testlib.OutcomeKeepNoAlternative,
				ErrorMessage:      reason,
				TotalMs:           msSince(start),
				InitialProviderID: initialProvider,
				RetryCount:        attempt,
			})
			log.Printf("[%s] iter %02d %s: keep  %-20s (no alternative: %s)",
				phase, i, consID, cur.ProviderClusterID, reason)
		}

		if ngResp.LatencyShortlist {
			growable := testlib.GrowableNodeGroups(ngResp.NodeGroups)
			var candidates []testlib.ProbeCandidate
			for _, ng := range growable {
				if ep, ok := endpoints[ng.ProviderClusterID]; ok {
					candidates = append(candidates, testlib.ProbeCandidate{
						ProviderClusterID: ng.ProviderClusterID,
						Endpoint:          ep,
					})
				}
			}
			if len(candidates) == 0 {
				if cur != nil {
					keepNoAlternative(fmt.Sprintf("growable=%d, none with ProbeEndpoint", len(growable)))
					return
				}
				state.addSelection(testlib.SelectionRecord{
					Timestamp:         start,
					ConsumerID:        consID,
					Phase:             phase,
					Policy:            policy,
					Iteration:         i,
					Outcome:           "no-candidates",
					ErrorMessage:      "no growable with ProbeEndpoint",
					DurationMs:        msSince(start),
					InitialProviderID: initialProvider,
					RetryCount:        attempt,
				})
				log.Printf("[%s] iter %d %s: no probe candidates (growable=%d)",
					phase, i, consID, len(growable))
				return
			}

			probeResp, err := console.Probe(ctx, candidates)
			if err != nil {
				state.addSelection(testlib.SelectionRecord{
					Timestamp:         start,
					ConsumerID:        consID,
					Phase:             phase,
					Policy:            policy,
					Iteration:         i,
					Outcome:           "probe-error",
					ErrorMessage:      err.Error(),
					DurationMs:        msSince(start),
					InitialProviderID: initialProvider,
					RetryCount:        attempt,
				})
				return
			}

			chosenProviderID = probeResp.Chosen
			if ng := testlib.FindNodeGroupByProvider(ngResp.NodeGroups, probeResp.Chosen); ng != nil {
				chosenNodeGroupID = ng.ID
			}
			if rtt, ok := probeResp.RTTs[probeResp.Chosen]; ok {
				chosenRTT = rtt
			}
			probeRec = &testlib.ProbeRecord{
				Timestamp:  start,
				ConsumerID: consID,
				Phase:      phase,
				Policy:     policy,
				Iteration:  i,
				Chosen:     probeResp.Chosen,
				RTTs:       probeResp.RTTs,
				DurationMs: probeResp.Duration,
			}
		} else {
			winner := testlib.FindWinner(ngResp.NodeGroups)
			if winner == nil {
				growable := testlib.GrowableNodeGroups(ngResp.NodeGroups)
				if cur != nil {
					keepNoAlternative(fmt.Sprintf("growable=%d", len(growable)))
					return
				}
				state.addSelection(testlib.SelectionRecord{
					Timestamp:         start,
					ConsumerID:        consID,
					Phase:             phase,
					Policy:            policy,
					Iteration:         i,
					Outcome:           "no-winner",
					ErrorMessage:      fmt.Sprintf("growable=%d applied=%s", len(growable), ngResp.AppliedPlacement),
					DurationMs:        msSince(start),
					InitialProviderID: initialProvider,
					RetryCount:        attempt,
				})
				log.Printf("[%s] iter %d %s: no single winner (growable=%d)", phase, i, consID, len(growable))
				return
			}
			chosenProviderID = winner.ProviderClusterID
			chosenNodeGroupID = winner.ID

			if ep, ok := endpoints[winner.ProviderClusterID]; ok {
				probeResp, err := console.Probe(ctx, []testlib.ProbeCandidate{{
					ProviderClusterID: winner.ProviderClusterID,
					Endpoint:          ep,
				}})
				if err == nil {
					if rtt, ok := probeResp.RTTs[winner.ProviderClusterID]; ok {
						chosenRTT = rtt
					}
					probeRec = &testlib.ProbeRecord{
						Timestamp:  start,
						ConsumerID: consID,
						Phase:      phase,
						Policy:     policy,
						Iteration:  i,
						Chosen:     winner.ProviderClusterID,
						RTTs:       probeResp.RTTs,
						DurationMs: probeResp.Duration,
					}
				}
			}
		}

		if initialProvider == "" && chosenProviderID != "" {
			initialProvider = chosenProviderID
		}

		// For latency: if current provider was probed and not chosen, switch.
		// If current provider was NOT probed (not in shortlist), keep.
		shouldSwitch := cur == nil
		if cur != nil && chosenProviderID != cur.ProviderClusterID {
			if probeRec != nil {
				_, curProbed := probeRec.RTTs[cur.ProviderClusterID]
				shouldSwitch = curProbed
			} else {
				shouldSwitch = true
			}
		}

		if !shouldSwitch {
			if probeRec != nil {
				state.addProbe(*probeRec)
			}
			state.addSelection(testlib.SelectionRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				SelectedID:        cur.ProviderClusterID,
				NodeGroupID:       cur.NodeGroupID,
				ReservationID:     cur.ReservationID,
				RTTMs:             chosenRTT,
				Outcome:           "success",
				DurationMs:        msSince(start),
				InitialProviderID: initialProvider,
				RetryCount:        attempt,
			})
			state.addReservation(testlib.ReservationRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				ReservationID:     cur.ReservationID,
				ProviderClusterID: cur.ProviderClusterID,
				NodeGroupID:       cur.NodeGroupID,
				Action:            "keep",
				FinalPhase:        "Peered",
				RTTMs:             chosenRTT,
				Outcome:           "success",
				TotalMs:           msSince(start),
				InitialProviderID: initialProvider,
				RetryCount:        attempt,
			})
			log.Printf("[%s] iter %02d %s: keep  %-20s (rtt=%.2fms)", phase, i, consID, cur.ProviderClusterID, chosenRTT)
			return
		}

		if probeRec != nil {
			state.addProbe(*probeRec)
		}

		if cur != nil && prevProvider == "" {
			prevProvider = cur.ProviderClusterID
			releaseStart := time.Now()
			releaseCtx, releaseCancel := context.WithTimeout(ctx, exp.ReservationTimeout)
			if err := broker.ReleaseAndWait(releaseCtx, cur.ReservationID, cur.Request, pollInterval); err != nil {
				log.Printf("[%s] iter %d %s: release error for %s: %v", phase, i, consID, cur.ReservationID, err)
			}
			releaseCancel()
			releaseMs = msSince(releaseStart)
			state.deleteActive(consID)
		}

		seq := state.nextSeq(consID)
		resID := testlib.MakeReservationID(orch.RunID, phase, consID, seq)

		if chosenNodeGroupID == "" {
			if ng := testlib.FindNodeGroupByProvider(ngResp.NodeGroups, chosenProviderID); ng != nil {
				chosenNodeGroupID = ng.ID
			}
		}

		req := &brokerapi.ReservationRequest{
			ProviderClusterID: chosenProviderID,
			NodeGroupID:       chosenNodeGroupID,
			ChunkCount:        1,
			ChunkType:         brokerv1alpha1.ChunkTypeStandard,
		}

		peerStart := time.Now()
		peerCtx, peerCancel := context.WithTimeout(ctx, exp.ReservationTimeout)
		resp, peerErr := broker.ReserveAndWait(peerCtx, resID, req, pollInterval)
		peerCancel()
		peerMs := msSince(peerStart)

		action := "create"
		if prevProvider != "" {
			action = "switch"
		}

		if peerErr != nil {
			switch {
			case agentclient.IsTooManyRequests(peerErr) && attempt < maxReserveRaceRetries:
				log.Printf("[%s] iter %d %s: rate limited on %s — backing off %s before retrying",
					phase, i, consID, chosenProviderID, raceRetryBackoff)
				if sleepErr := testlib.SleepCtx(ctx, raceRetryBackoff); sleepErr != nil {
					return
				}
				continue
			case (agentclient.IsConflict(peerErr) || agentclient.IsTransient(peerErr)) && attempt < maxReserveRaceRetries:
				log.Printf("[%s] iter %d %s: %s — retrying with next-best (%v)",
					phase, i, consID, chosenProviderID, peerErr)
				continue
			}

			finalPhase := ""
			if resp != nil {
				finalPhase = string(resp.Status)
			}
			state.addSelection(testlib.SelectionRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				SelectedID:        chosenProviderID,
				NodeGroupID:       chosenNodeGroupID,
				ReservationID:     resID,
				RTTMs:             chosenRTT,
				Outcome:           "reserve-error",
				ErrorMessage:      peerErr.Error(),
				DurationMs:        msSince(start),
				InitialProviderID: initialProvider,
				RetryCount:        attempt,
			})
			state.addReservation(testlib.ReservationRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				ReservationID:     resID,
				ProviderClusterID: chosenProviderID,
				NodeGroupID:       chosenNodeGroupID,
				Action:            action,
				PrevProviderID:    prevProvider,
				PeerMs:            peerMs,
				ReleaseMs:         releaseMs,
				TotalMs:           msSince(start),
				FinalPhase:        finalPhase,
				RTTMs:             chosenRTT,
				Outcome:           "error",
				ErrorMessage:      peerErr.Error(),
				InitialProviderID: initialProvider,
				RetryCount:        attempt,
			})
			log.Printf("[%s] iter %d %s: %s error → %s: %v", phase, i, consID, action, chosenProviderID, peerErr)
			return
		}

		state.setActive(consID, &testlib.ConsumerReservation{
			ReservationID:     resID,
			ProviderClusterID: chosenProviderID,
			NodeGroupID:       chosenNodeGroupID,
			Request:           req,
		})

		state.addSelection(testlib.SelectionRecord{
			Timestamp:         start,
			ConsumerID:        consID,
			Phase:             phase,
			Policy:            policy,
			Iteration:         i,
			SelectedID:        chosenProviderID,
			NodeGroupID:       chosenNodeGroupID,
			ReservationID:     resID,
			RTTMs:             chosenRTT,
			Outcome:           "success",
			DurationMs:        msSince(start),
			InitialProviderID: initialProvider,
			RetryCount:        attempt,
		})
		state.addReservation(testlib.ReservationRecord{
			Timestamp:         start,
			ConsumerID:        consID,
			Phase:             phase,
			Policy:            policy,
			Iteration:         i,
			ReservationID:     resID,
			ProviderClusterID: chosenProviderID,
			NodeGroupID:       chosenNodeGroupID,
			Action:            action,
			PrevProviderID:    prevProvider,
			PeerMs:            peerMs,
			ReleaseMs:         releaseMs,
			TotalMs:           msSince(start),
			FinalPhase:        string(resp.Status),
			RTTMs:             chosenRTT,
			Outcome:           "success",
			InitialProviderID: initialProvider,
			RetryCount:        attempt,
		})
		log.Printf("[%s] iter %02d %s: %s %-20s (res=%s peer=%.0fms rel=%.0fms rtt=%.2fms)",
			phase, i, consID, action, chosenProviderID, resID, peerMs, releaseMs, chosenRTT)
		return
	}
}

func summarizeLatencyPhase(selections []testlib.SelectionRecord, _ []testlib.ProbeRecord) testlib.PhaseSummary {
	s := testlib.PhaseSummary{
		Iterations:      len(selections),
		SelectionCounts: make(map[string]int),
	}
	var rttValues []float64
	for _, r := range selections {
		// A keep-no-alternative iteration ended with the consumer holding
		// working capacity, so it counts as a success here; selections.csv
		// keeps the two apart for anyone who needs the distinction.
		if r.Outcome == "success" || r.Outcome == testlib.OutcomeKeepNoAlternative {
			s.Successes++
			s.SelectionCounts[r.SelectedID]++
			if r.RTTMs > 0 && !math.IsInf(r.RTTMs, 1) {
				rttValues = append(rttValues, r.RTTMs)
			}
		} else {
			s.Failures++
		}
	}
	if len(rttValues) > 0 {
		sort.Float64s(rttValues)
		var sum float64
		for _, v := range rttValues {
			sum += v
		}
		s.MeanRTTMs = sum / float64(len(rttValues))
		s.MedianRTTMs = rttValues[len(rttValues)/2]
	}
	return s
}

func printSummary(s testlib.ExperimentSummary) {
	log.Println("─── Results ───")
	for _, ps := range []struct {
		label   string
		summary testlib.PhaseSummary
	}{
		{fmt.Sprintf("Phase A (%s)", s.PhaseAPolicy), s.PhaseASummary},
		{fmt.Sprintf("Phase B (%s)", s.PhaseBPolicy), s.PhaseBSummary},
	} {
		log.Printf("%s: %d success, %d fail", ps.label, ps.summary.Successes, ps.summary.Failures)
		for id, count := range ps.summary.SelectionCounts {
			pct := 100 * float64(count) / float64(max(ps.summary.Successes, 1))
			log.Printf("  %-20s %d selections (%.0f%%)", id, count, pct)
		}
		if ps.summary.MeanRTTMs > 0 {
			log.Printf("  mean RTT: %.2fms  median RTT: %.2fms", ps.summary.MeanRTTMs, ps.summary.MedianRTTMs)
		}
	}
}

func refreshLatency(ctx context.Context, exp testlib.TestParams, consumerTCs []*testlib.TCConsumerDelay, providerTCs []*testlib.TCDelayKind) {
	interval := exp.LatencyRefreshInterval
	jitter := exp.LatencyJitterMs
	log.Printf("[latency-refresh] started (interval=%s, jitter=±%dms)", interval, jitter)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[latency-refresh] stopped")
			return
		case <-ticker.C:
			for _, tc := range consumerTCs {
				newDelays := make([]testlib.ProviderDelayEntry, len(tc.ProviderDelays))
				for i, pd := range tc.ProviderDelays {
					delta := rand.Intn(2*jitter+1) - jitter
					newDelay := pd.DelayMs + delta
					if newDelay < 1 {
						newDelay = 1
					}
					newDelays[i] = testlib.ProviderDelayEntry{
						ProviderIP: pd.ProviderIP,
						DelayMs:    newDelay,
						Label:      pd.Label,
					}
				}
				if err := tc.UpdateDelays(newDelays); err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("[latency-refresh] error updating %s: %v", tc.ContainerName, err)
					continue
				}
				for _, pd := range newDelays {
					log.Printf("[latency-refresh]   %s → %s: %dms", tc.ContainerName, pd.Label, pd.DelayMs)
				}
			}
			for _, tc := range providerTCs {
				delta := rand.Intn(2*jitter+1) - jitter
				newDelay := tc.DelayMs + delta
				if newDelay < 1 {
					newDelay = 1
				}
				if err := tc.UpdateDelay(newDelay); err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("[latency-refresh] error updating %s: %v", tc.ContainerName, err)
					continue
				}
				log.Printf("[latency-refresh]   %s: %dms", tc.ContainerName, newDelay)
			}
		}
	}
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}
