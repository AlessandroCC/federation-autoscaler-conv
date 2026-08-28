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

// Command comparative-eco runs Phase A (Random) vs Phase B (Eco) with full
// automation: creates Kind clusters, deploys all components, runs the
// experiment, collects results, and cleans up.
//
// Usage:
//
//	go run ./tests/comparative-eco/ --config tests/configs/eco.yaml
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
// clusters orphaned (observed: a transient setup error left 11 running Kind
// clusters + a busy inotify budget behind).
func run() error {
	configPath, keepClusters, skipBuild, runID := parseFlags()

	cfg, err := testlib.LoadAutoConfig(configPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	orch, err := testlib.NewOrchestrator(cfg, "comparative-eco", keepClusters, skipBuild, runID)
	if err != nil {
		return fmt.Errorf("orchestrator: %w", err)
	}

	// Ensure cleanup runs on exit — now reachable on every return path below,
	// since none of them calls os.Exit directly.
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

	// Build mock-eco client.
	mockEcoURL, err := orch.MockEcoURL(ctx)
	if err != nil {
		return fmt.Errorf("get mock-eco URL: %w", err)
	}
	mockEco := testlib.NewMockEcoClient(mockEcoURL)
	log.Printf("[setup] mock-eco at %s", mockEcoURL)

	// Clear stale policies.
	if err := clients.SetPolicyAll(ctx, "None", 0); err != nil {
		return fmt.Errorf("clear policies: %w", err)
	}

	var allRecords []testlib.SelectionRecord
	var allReservations []testlib.ReservationRecord
	var allSnapshots []testlib.NodeGroupSnapshotRecord
	mode := exp.Mode

	// Start carbon refresh for both phases (same background load).
	carbonRefreshCtx, cancelCarbonRefresh := context.WithCancel(ctx)
	var carbonWG sync.WaitGroup
	carbonWG.Add(1)
	go func() {
		defer carbonWG.Done()
		refreshCarbon(carbonRefreshCtx, mockEco, cfg, exp)
	}()

	// --- Phase A: Random ---
	log.Println("=== PHASE A: Random ===")
	if err := clients.SetPolicyAll(ctx, "Random", exp.PolicyPropagationWait); err != nil {
		return fmt.Errorf("set Random: %w", err)
	}

	var phaseARecords []testlib.SelectionRecord
	if mode == "reserve" {
		var phaseARes []testlib.ReservationRecord
		var phaseASnaps []testlib.NodeGroupSnapshotRecord
		phaseARecords, phaseARes, phaseASnaps, err = runReservePhase(ctx, orch, clients, testlib.PhaseA, "Random")
		allReservations = append(allReservations, phaseARes...)
		allSnapshots = append(allSnapshots, phaseASnaps...)
	} else {
		var phaseASnaps []testlib.NodeGroupSnapshotRecord
		phaseARecords, phaseASnaps, err = runObservePhase(ctx, orch, clients, testlib.PhaseA, "Random")
		allSnapshots = append(allSnapshots, phaseASnaps...)
	}
	if err != nil {
		return fmt.Errorf("phase A: %w", err)
	}
	allRecords = append(allRecords, phaseARecords...)
	log.Printf("phase A complete: %d samples", len(phaseARecords))

	// --- Transition: switch to Eco policy ---
	log.Println("=== TRANSITION ===")

	// Switch to Eco (carbon values are already being refreshed by the goroutine).
	if err := clients.SetPolicyAll(ctx, "Eco", 0); err != nil {
		return fmt.Errorf("set Eco: %w", err)
	}
	wait := exp.AdvertisementLag
	if exp.PolicyPropagationWait > wait {
		wait = exp.PolicyPropagationWait
	}
	log.Printf("  waiting %s for policy + advertisement propagation...", wait)
	if err := testlib.SleepCtx(ctx, wait); err != nil {
		return err
	}

	// --- Phase B: Eco ---
	log.Println("=== PHASE B: Eco ===")
	var phaseBRecords []testlib.SelectionRecord
	if mode == "reserve" {
		var phaseBRes []testlib.ReservationRecord
		var phaseBSnaps []testlib.NodeGroupSnapshotRecord
		phaseBRecords, phaseBRes, phaseBSnaps, err = runReservePhase(ctx, orch, clients, testlib.PhaseB, "Eco")
		allReservations = append(allReservations, phaseBRes...)
		allSnapshots = append(allSnapshots, phaseBSnaps...)
	} else {
		var phaseBSnaps []testlib.NodeGroupSnapshotRecord
		phaseBRecords, phaseBSnaps, err = runObservePhase(ctx, orch, clients, testlib.PhaseB, "Eco")
		allSnapshots = append(allSnapshots, phaseBSnaps...)
	}
	cancelCarbonRefresh()
	carbonWG.Wait()
	if err != nil {
		return fmt.Errorf("phase B: %w", err)
	}
	allRecords = append(allRecords, phaseBRecords...)
	log.Printf("phase B complete: %d samples", len(phaseBRecords))

	// --- Cleanup policies ---
	log.Println("=== EXPERIMENT CLEANUP ===")
	if err := clients.SetPolicyAll(ctx, "None", 0); err != nil {
		log.Printf("cleanup: clear policies: %v", err)
	}
	if err := mockEco.ResetCarbon(ctx); err != nil {
		log.Printf("cleanup: reset mock-eco: %v", err)
	}

	// --- Write results ---
	outputDir := orch.OutputDir
	log.Printf("writing results to %s", outputDir)
	if err := testlib.WriteSelectionCSV(outputDir, "selections.csv", allRecords); err != nil {
		return fmt.Errorf("write CSV: %w", err)
	}
	if mode == "reserve" && len(allReservations) > 0 {
		if err := testlib.WriteReservationCSV(outputDir, "reservations.csv", allReservations); err != nil {
			return fmt.Errorf("write reservations CSV: %w", err)
		}
	}
	if len(allSnapshots) > 0 {
		if err := testlib.WriteNodeGroupCSV(outputDir, "nodegroups.csv", allSnapshots); err != nil {
			return fmt.Errorf("write nodegroups CSV: %w", err)
		}
	}

	brokerURL, _ := orch.BrokerURL(ctx)
	consoleURL, _ := orch.ConsoleURL(ctx, 0)

	summary := testlib.ExperimentSummary{
		RunID:              orch.RunID,
		TestType:           "comparative-eco",
		StartTime:          startTime,
		EndTime:            time.Now(),
		ConsumerID:         clients.Identity.ClusterID,
		ConsumerCertFP:     clients.CertFP,
		BrokerURL:          brokerURL,
		ConsoleURL:         consoleURL,
		ProviderCount:      cfg.Providers,
		IterationsPerPhase: exp.Iterations,
		PhaseAPolicy:       "Random",
		PhaseBPolicy:       "Eco",
		PhaseASummary:      summarizePhase(phaseARecords),
		PhaseBSummary:      summarizePhase(phaseBRecords),
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

// runObservePhase drives one consumer-poll cycle per iteration. Consumers are
// dispatched concurrently (one goroutine each) within an iteration, mirroring
// how independent real Consumer Agents behave in production — each polls the
// Broker on its own, none waits in line behind another. All consumers in an
// iteration are joined (awaited) before the next iteration's phasePause, so
// the iteration cadence itself is unchanged; only the per-consumer work
// inside each iteration is now parallel instead of serial.
func runObservePhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, phase, policy string) ([]testlib.SelectionRecord, []testlib.NodeGroupSnapshotRecord, error) {
	exp := orch.Config.Experiment
	var mu sync.Mutex
	var records []testlib.SelectionRecord
	var snapshots []testlib.NodeGroupSnapshotRecord

	consumerIDs := make([]string, 0, len(clients.Consoles))
	for id := range clients.Consoles {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	for i := 1; i <= exp.Iterations; i++ {
		if ctx.Err() != nil {
			return records, snapshots, ctx.Err()
		}

		var wg sync.WaitGroup
		for _, consID := range consumerIDs {
			wg.Add(1)
			go func(consID string) {
				defer wg.Done()
				runObserveConsumerIteration(ctx, clients, phase, policy, i, consID, &mu, &records, &snapshots)
			}(consID)
		}
		wg.Wait()

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return records, snapshots, err
			}
		}
	}

	return records, snapshots, nil
}

// runObserveConsumerIteration is the per-consumer, per-iteration body of
// runObservePhase, safe to run concurrently with other consumers' calls: it
// only touches shared state (records, snapshots) behind mu, and everything
// else is function-local.
func runObserveConsumerIteration(ctx context.Context, clients *testlib.ExperimentClients, phase, policy string, i int, consID string, mu *sync.Mutex, records *[]testlib.SelectionRecord, snapshots *[]testlib.NodeGroupSnapshotRecord) {
	start := time.Now()
	broker := clients.BrokerFor(consID)

	ngResp, err := broker.GetNodeGroups(ctx)
	if err != nil {
		mu.Lock()
		*records = append(*records, testlib.SelectionRecord{
			Timestamp:    start,
			ConsumerID:   consID,
			Phase:        phase,
			Policy:       policy,
			Iteration:    i,
			Outcome:      "error",
			ErrorMessage: fmt.Sprintf("get nodegroups: %v", err),
			DurationMs:   msSince(start),
		})
		mu.Unlock()
		log.Printf("[%s] iter %d %s: nodegroups error: %v", phase, i, consID, err)
		return
	}

	winner := testlib.FindWinner(ngResp.NodeGroups)

	snaps := make([]testlib.NodeGroupSnapshotRecord, 0, len(ngResp.NodeGroups))
	for _, ng := range ngResp.NodeGroups {
		var carbon float64
		var hasCarbon bool
		if ng.CarbonIntensity != nil {
			carbon = *ng.CarbonIntensity
			hasCarbon = true
		}
		snaps = append(snaps, testlib.NodeGroupSnapshotRecord{
			Timestamp:         start,
			ConsumerID:        consID,
			Phase:             phase,
			Policy:            policy,
			Iteration:         i,
			ProviderClusterID: ng.ProviderClusterID,
			NodeGroupID:       ng.ID,
			PlacementMetric:   ng.PlacementMetric,
			HasMetric:         ng.HasMetric,
			CarbonIntensity:   carbon,
			HasCarbon:         hasCarbon,
			CurrentReserved:   ng.CurrentReserved,
			MaxSize:           ng.MaxSize,
			AppliedPlacement:  string(ngResp.AppliedPlacement),
			IsSelected:        winner != nil && ng.ID == winner.ID,
		})
	}
	mu.Lock()
	*snapshots = append(*snapshots, snaps...)
	mu.Unlock()

	if winner == nil {
		growable := testlib.GrowableNodeGroups(ngResp.NodeGroups)
		mu.Lock()
		*records = append(*records, testlib.SelectionRecord{
			Timestamp:    start,
			ConsumerID:   consID,
			Phase:        phase,
			Policy:       policy,
			Iteration:    i,
			Outcome:      "no-winner",
			ErrorMessage: fmt.Sprintf("growable=%d applied=%s", len(growable), ngResp.AppliedPlacement),
			DurationMs:   msSince(start),
		})
		mu.Unlock()
		log.Printf("[%s] iter %d %s: no single winner (growable=%d applied=%s)",
			phase, i, consID, len(growable), ngResp.AppliedPlacement)
		return
	}

	rec := testlib.SelectionRecord{
		Timestamp:      start,
		ConsumerID:     consID,
		Phase:          phase,
		Policy:         policy,
		Iteration:      i,
		SelectedID:     winner.ProviderClusterID,
		NodeGroupID:    winner.ID,
		PlacementValue: winner.PlacementMetric,
		HasMetric:      winner.HasMetric,
		Outcome:        "success",
		DurationMs:     msSince(start),
	}
	mu.Lock()
	*records = append(*records, rec)
	mu.Unlock()

	log.Printf("[%s] iter %02d %s: winner=%-20s metric=%8.2f applied=%s",
		phase, i, consID, winner.ProviderClusterID, winner.PlacementMetric, ngResp.AppliedPlacement)
}

// reservePhaseState is the shared, mutex-protected state one runReservePhase
// call accumulates across all consumers and iterations. Every consumer's own
// entries in active/seqNo are only ever touched by that consumer's own
// goroutine, but Go maps are not safe for concurrent access from multiple
// goroutines even across disjoint keys, so every access — reads included —
// goes through mu.
type reservePhaseState struct {
	mu           sync.Mutex
	selections   []testlib.SelectionRecord
	reservations []testlib.ReservationRecord
	snapshots    []testlib.NodeGroupSnapshotRecord
	active       map[string]*testlib.ConsumerReservation
	seqNo        map[string]int
}

func newReservePhaseState() *reservePhaseState {
	return &reservePhaseState{
		active: make(map[string]*testlib.ConsumerReservation),
		seqNo:  make(map[string]int),
	}
}

func (s *reservePhaseState) addSnapshots(snaps []testlib.NodeGroupSnapshotRecord) {
	s.mu.Lock()
	s.snapshots = append(s.snapshots, snaps...)
	s.mu.Unlock()
}

func (s *reservePhaseState) addSelection(sel testlib.SelectionRecord) {
	s.mu.Lock()
	s.selections = append(s.selections, sel)
	s.mu.Unlock()
}

func (s *reservePhaseState) addReservation(res testlib.ReservationRecord) {
	s.mu.Lock()
	s.reservations = append(s.reservations, res)
	s.mu.Unlock()
}

func (s *reservePhaseState) getActive(consID string) *testlib.ConsumerReservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active[consID]
}

func (s *reservePhaseState) setActive(consID string, res *testlib.ConsumerReservation) {
	s.mu.Lock()
	s.active[consID] = res
	s.mu.Unlock()
}

func (s *reservePhaseState) deleteActive(consID string) {
	s.mu.Lock()
	delete(s.active, consID)
	s.mu.Unlock()
}

func (s *reservePhaseState) nextSeq(consID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	seq := s.seqNo[consID]
	s.seqNo[consID] = seq + 1
	return seq
}

// runReservePhase drives one reserve/keep/switch cycle per iteration.
// Consumers are dispatched concurrently (one goroutine each) within an
// iteration, mirroring how independent real Consumer Agents behave in
// production — each reserves/releases on its own timeline, none blocks
// behind another's Liqo peering. All consumers in an iteration are joined
// before the next iteration's phasePause, so the iteration cadence is
// unchanged; only the per-consumer work inside each iteration is now
// parallel instead of serial.
func runReservePhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, phase, policy string) ([]testlib.SelectionRecord, []testlib.ReservationRecord, []testlib.NodeGroupSnapshotRecord, error) {
	exp := orch.Config.Experiment
	pollInterval := exp.ReservationPoll

	consumerIDs := make([]string, 0, len(clients.Consoles))
	for id := range clients.Consoles {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	state := newReservePhaseState()

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
			return state.selections, state.reservations, state.snapshots, ctx.Err()
		}

		var wg sync.WaitGroup
		for _, consID := range consumerIDs {
			wg.Add(1)
			go func(consID string) {
				defer wg.Done()
				runReserveConsumerIteration(ctx, orch, clients, phase, policy, i, consID, pollInterval, exp, state)
			}(consID)
		}
		wg.Wait()

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return state.selections, state.reservations, state.snapshots, err
			}
		}
	}

	return state.selections, state.reservations, state.snapshots, nil
}

// maxReserveRaceRetries bounds how many times a consumer re-picks after
// losing a race for the last chunk on its computed winner: two consumers
// both read the same "provider X has room" snapshot, only one
// PostReservation actually lands, and the loser's view is now stale. A
// fresh GetNodeGroups + FindWinner naturally exposes the next-best
// candidate — the Broker only ever leaves the cheapest/greenest provider
// WITH HEADROOM unmasked — so retrying mirrors what the real,
// continuously-reconciling ResourceRequest controller / Cluster Autoscaler
// would do on its next tick, just resolved within this iteration instead of
// waiting out a real requeue delay.
const maxReserveRaceRetries = 5

// nodeGroupSnapshots builds one record per node group in resp, flagging
// winner (nil if none) as the selected one.
func nodeGroupSnapshots(resp *brokerapi.NodeGroupListResponse, ts time.Time, consID, phase, policy string, i int, winner *brokerapi.NodeGroupView) []testlib.NodeGroupSnapshotRecord {
	snaps := make([]testlib.NodeGroupSnapshotRecord, 0, len(resp.NodeGroups))
	for _, ng := range resp.NodeGroups {
		var carbon float64
		var hasCarbon bool
		if ng.CarbonIntensity != nil {
			carbon = *ng.CarbonIntensity
			hasCarbon = true
		}
		snaps = append(snaps, testlib.NodeGroupSnapshotRecord{
			Timestamp:         ts,
			ConsumerID:        consID,
			Phase:             phase,
			Policy:            policy,
			Iteration:         i,
			ProviderClusterID: ng.ProviderClusterID,
			NodeGroupID:       ng.ID,
			PlacementMetric:   ng.PlacementMetric,
			HasMetric:         ng.HasMetric,
			CarbonIntensity:   carbon,
			HasCarbon:         hasCarbon,
			CurrentReserved:   ng.CurrentReserved,
			MaxSize:           ng.MaxSize,
			AppliedPlacement:  string(resp.AppliedPlacement),
			IsSelected:        winner != nil && ng.ID == winner.ID,
		})
	}
	return snaps
}

// runReserveConsumerIteration is the per-consumer, per-iteration body of
// runReservePhase, safe to run concurrently with other consumers' calls:
// every access to shared state goes through state's own locking. Records
// exactly one selection/reservation row per (consumer, iteration) — even
// across capacity-race retries — on whichever attempt actually terminates.
func runReserveConsumerIteration(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, phase, policy string, i int, consID string, pollInterval time.Duration, exp testlib.TestParams, state *reservePhaseState) {
	start := time.Now()
	broker := clients.BrokerFor(consID)

	var prevProvider string
	var releaseMs float64

	for attempt := 0; ; attempt++ {
		ngResp, err := broker.GetNodeGroups(ctx)
		if err != nil {
			state.addSelection(testlib.SelectionRecord{
				Timestamp:    start,
				ConsumerID:   consID,
				Phase:        phase,
				Policy:       policy,
				Iteration:    i,
				Outcome:      "error",
				ErrorMessage: fmt.Sprintf("get nodegroups: %v", err),
				DurationMs:   msSince(start),
			})
			log.Printf("[%s] iter %d %s: nodegroups error: %v", phase, i, consID, err)
			return
		}

		winner := testlib.FindWinner(ngResp.NodeGroups)

		if winner == nil {
			growable := testlib.GrowableNodeGroups(ngResp.NodeGroups)
			state.addSnapshots(nodeGroupSnapshots(ngResp, start, consID, phase, policy, i, nil))
			state.addSelection(testlib.SelectionRecord{
				Timestamp:    start,
				ConsumerID:   consID,
				Phase:        phase,
				Policy:       policy,
				Iteration:    i,
				Outcome:      "no-winner",
				ErrorMessage: fmt.Sprintf("growable=%d applied=%s", len(growable), ngResp.AppliedPlacement),
				DurationMs:   msSince(start),
			})
			log.Printf("[%s] iter %d %s: no single winner (growable=%d)", phase, i, consID, len(growable))
			return
		}

		cur := state.getActive(consID)

		if !testlib.ShouldSwitch(cur, winner.ProviderClusterID, ngResp.NodeGroups) {
			state.addSnapshots(nodeGroupSnapshots(ngResp, start, consID, phase, policy, i, winner))
			curNG := testlib.FindNodeGroupByProvider(ngResp.NodeGroups, cur.ProviderClusterID)
			var curMetric float64
			var curHasMetric bool
			if curNG != nil {
				curMetric = curNG.PlacementMetric
				curHasMetric = curNG.HasMetric
			}

			state.addSelection(testlib.SelectionRecord{
				Timestamp:      start,
				ConsumerID:     consID,
				Phase:          phase,
				Policy:         policy,
				Iteration:      i,
				SelectedID:     cur.ProviderClusterID,
				NodeGroupID:    cur.NodeGroupID,
				ReservationID:  cur.ReservationID,
				PlacementValue: curMetric,
				HasMetric:      curHasMetric,
				Outcome:        "success",
				DurationMs:     msSince(start),
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
				PlacementMetric:   curMetric,
				Outcome:           "success",
				TotalMs:           msSince(start),
			})
			log.Printf("[%s] iter %02d %s: keep  %-20s (metric=%.2f)", phase, i, consID, cur.ProviderClusterID, curMetric)
			return
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

		req := &brokerapi.ReservationRequest{
			ProviderClusterID: winner.ProviderClusterID,
			NodeGroupID:       winner.ID,
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
			if agentclient.IsConflict(peerErr) && attempt < maxReserveRaceRetries {
				log.Printf("[%s] iter %d %s: lost race for %s (insufficient capacity) — retrying with next-best",
					phase, i, consID, winner.ProviderClusterID)
				continue
			}

			state.addSnapshots(nodeGroupSnapshots(ngResp, start, consID, phase, policy, i, winner))
			finalPhase := ""
			if resp != nil {
				finalPhase = string(resp.Status)
			}
			state.addSelection(testlib.SelectionRecord{
				Timestamp:      start,
				ConsumerID:     consID,
				Phase:          phase,
				Policy:         policy,
				Iteration:      i,
				SelectedID:     winner.ProviderClusterID,
				NodeGroupID:    winner.ID,
				ReservationID:  resID,
				PlacementValue: winner.PlacementMetric,
				HasMetric:      winner.HasMetric,
				Outcome:        "reserve-error",
				ErrorMessage:   peerErr.Error(),
				DurationMs:     msSince(start),
			})
			state.addReservation(testlib.ReservationRecord{
				Timestamp:         start,
				ConsumerID:        consID,
				Phase:             phase,
				Policy:            policy,
				Iteration:         i,
				ReservationID:     resID,
				ProviderClusterID: winner.ProviderClusterID,
				NodeGroupID:       winner.ID,
				Action:            action,
				PrevProviderID:    prevProvider,
				PeerMs:            peerMs,
				ReleaseMs:         releaseMs,
				TotalMs:           msSince(start),
				FinalPhase:        finalPhase,
				PlacementMetric:   winner.PlacementMetric,
				Outcome:           "error",
				ErrorMessage:      peerErr.Error(),
			})
			log.Printf("[%s] iter %d %s: %s error → %s: %v", phase, i, consID, action, winner.ProviderClusterID, peerErr)
			return
		}

		state.addSnapshots(nodeGroupSnapshots(ngResp, start, consID, phase, policy, i, winner))
		state.setActive(consID, &testlib.ConsumerReservation{
			ReservationID:     resID,
			ProviderClusterID: winner.ProviderClusterID,
			NodeGroupID:       winner.ID,
			Request:           req,
		})

		state.addSelection(testlib.SelectionRecord{
			Timestamp:      start,
			ConsumerID:     consID,
			Phase:          phase,
			Policy:         policy,
			Iteration:      i,
			SelectedID:     winner.ProviderClusterID,
			NodeGroupID:    winner.ID,
			ReservationID:  resID,
			PlacementValue: winner.PlacementMetric,
			HasMetric:      winner.HasMetric,
			Outcome:        "success",
			DurationMs:     msSince(start),
		})
		state.addReservation(testlib.ReservationRecord{
			Timestamp:         start,
			ConsumerID:        consID,
			Phase:             phase,
			Policy:            policy,
			Iteration:         i,
			ReservationID:     resID,
			ProviderClusterID: winner.ProviderClusterID,
			NodeGroupID:       winner.ID,
			Action:            action,
			PrevProviderID:    prevProvider,
			PeerMs:            peerMs,
			ReleaseMs:         releaseMs,
			TotalMs:           msSince(start),
			FinalPhase:        string(resp.Status),
			PlacementMetric:   winner.PlacementMetric,
			Outcome:           "success",
		})
		log.Printf("[%s] iter %02d %s: %s %-20s (res=%s peer=%.0fms rel=%.0fms metric=%.2f)",
			phase, i, consID, action, winner.ProviderClusterID, resID, peerMs, releaseMs, winner.PlacementMetric)
		return
	}
}

func summarizePhase(records []testlib.SelectionRecord) testlib.PhaseSummary {
	s := testlib.PhaseSummary{
		Iterations:      len(records),
		SelectionCounts: make(map[string]int),
	}
	var metricSum float64
	var metricCount int
	for _, r := range records {
		if r.Outcome == "success" {
			s.Successes++
			s.SelectionCounts[r.SelectedID]++
			if r.HasMetric {
				metricSum += r.PlacementValue
				metricCount++
			}
		} else {
			s.Failures++
		}
	}
	if metricCount > 0 {
		s.MeanPlacementVal = metricSum / float64(metricCount)
	}
	return s
}

func printSummary(s testlib.ExperimentSummary) {
	log.Println("─── Results ───")
	log.Printf("Phase A (%s): %d success, %d fail", s.PhaseAPolicy, s.PhaseASummary.Successes, s.PhaseASummary.Failures)
	for id, count := range s.PhaseASummary.SelectionCounts {
		pct := 100 * float64(count) / float64(max(s.PhaseASummary.Successes, 1))
		log.Printf("  %-20s %d selections (%.0f%%)", id, count, pct)
	}
	log.Printf("Phase B (%s): %d success, %d fail", s.PhaseBPolicy, s.PhaseBSummary.Successes, s.PhaseBSummary.Failures)
	for id, count := range s.PhaseBSummary.SelectionCounts {
		pct := 100 * float64(count) / float64(max(s.PhaseBSummary.Successes, 1))
		log.Printf("  %-20s %d selections (%.0f%%)", id, count, pct)
	}
	if s.PhaseBSummary.MeanPlacementVal > 0 {
		log.Printf("  mean eco metric: %.4f", s.PhaseBSummary.MeanPlacementVal)
	}
}

func refreshCarbon(ctx context.Context, mockEco *testlib.MockEcoClient, cfg *testlib.AutoConfig, exp testlib.TestParams) {
	interval := exp.CarbonRefreshInterval
	log.Printf("[carbon-refresh] started (interval=%s, green fraction=%.0f%%–%.0f%%, low=%d high=%d)",
		interval, exp.CarbonGreenFractionMin*100, exp.CarbonGreenFractionMax*100,
		exp.CarbonLow, exp.CarbonHigh)

	assignCarbon := func() {
		unique := uniqueRegions(cfg.ProviderRegions)
		n := len(unique)
		if n == 0 {
			return
		}

		frac := exp.CarbonGreenFractionMin +
			rand.Float64()*(exp.CarbonGreenFractionMax-exp.CarbonGreenFractionMin)
		greenCount := int(math.Round(frac * float64(n)))
		if greenCount < 1 {
			greenCount = 1
		}
		if greenCount > n-1 {
			greenCount = n - 1
		}

		perm := rand.Perm(n)
		greenSet := make(map[int]bool, greenCount)
		for i := 0; i < greenCount; i++ {
			greenSet[perm[i]] = true
		}

		for i, region := range unique {
			var carbon int
			if greenSet[i] {
				jitter := 0.7 + rand.Float64()*0.6
				carbon = max(1, int(float64(exp.CarbonLow)*jitter))
			} else {
				jitter := 0.7 + rand.Float64()*0.6
				carbon = max(1, int(float64(exp.CarbonHigh)*jitter))
			}
			if err := mockEco.SetCarbon(ctx, region, carbon, nil); err != nil {
				if ctx.Err() != nil {
					return
				}
				log.Printf("[carbon-refresh] error setting %s: %v", region, err)
			}
		}
		log.Printf("[carbon-refresh] assigned %d/%d green regions", greenCount, n)
	}

	assignCarbon()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Println("[carbon-refresh] stopped")
			return
		case <-ticker.C:
			assignCarbon()
		}
	}
}

func uniqueRegions(regions []string) []string {
	seen := make(map[string]bool, len(regions))
	var result []string
	for _, r := range regions {
		if r != "" && !seen[r] {
			seen[r] = true
			result = append(result, r)
		}
	}
	return result
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}
