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
	"os"
	"os/signal"
	"sort"
	"time"

	brokerv1alpha1 "github.com/netgroup-polito/federation-autoscaler/api/broker/v1alpha1"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
	"github.com/netgroup-polito/federation-autoscaler/tests/testlib"
)

func main() {
	configPath, keepClusters, skipBuild, runID := parseFlags()

	cfg, err := testlib.LoadAutoConfig(configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	orch, err := testlib.NewOrchestrator(cfg, "comparative-eco", keepClusters, skipBuild, runID)
	if err != nil {
		log.Fatalf("orchestrator: %v", err)
	}

	// Ensure cleanup runs on exit.
	defer orch.Teardown(context.Background())

	if err := orch.Setup(ctx); err != nil {
		log.Fatalf("setup failed: %v", err)
	}

	if err := runExperiment(ctx, orch); err != nil {
		log.Fatalf("experiment failed: %v", err)
	}
}

func runExperiment(ctx context.Context, orch *testlib.Orchestrator) error {
	startTime := time.Now()
	cfg := orch.Config
	exp := cfg.Experiment
	clients := orch.Clients

	// Eco test requires mock-eco.
	if exp.GreenRegion == "" {
		return fmt.Errorf("experiment.greenRegion is required for the eco test")
	}

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
	mode := exp.Mode

	// --- Phase A: Random ---
	log.Println("=== PHASE A: Random ===")
	if err := clients.SetPolicyAll(ctx, "Random", exp.PolicyPropagationWait); err != nil {
		return fmt.Errorf("set Random: %w", err)
	}

	var phaseARecords []testlib.SelectionRecord
	if mode == "reserve" {
		var phaseARes []testlib.ReservationRecord
		phaseARecords, phaseARes, err = runReservePhase(ctx, orch, clients, testlib.PhaseA, "Random")
		allReservations = append(allReservations, phaseARes...)
	} else {
		phaseARecords, err = runObservePhase(ctx, orch, clients, testlib.PhaseA, "Random")
	}
	if err != nil {
		return fmt.Errorf("phase A: %w", err)
	}
	allRecords = append(allRecords, phaseARecords...)
	log.Printf("phase A complete: %d samples", len(phaseARecords))

	// --- Transition: set carbon values ---
	log.Println("=== TRANSITION ===")
	providerRegions := make(map[string]bool)
	for _, r := range cfg.ProviderRegions {
		if r != "" {
			providerRegions[r] = true
		}
	}
	if len(providerRegions) == 0 {
		log.Println("WARNING: no provider regions set — mock-eco won't differentiate them")
	}
	for region := range providerRegions {
		carbon := exp.OtherCarbon
		if region == exp.GreenRegion {
			carbon = exp.GreenCarbon
		}
		if err := mockEco.SetCarbon(ctx, region, carbon, nil); err != nil {
			return fmt.Errorf("set carbon for region %s: %w", region, err)
		}
		log.Printf("  mock-eco: %s → %d gCO2eq/kWh", region, carbon)
	}

	// Switch to Eco.
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
		phaseBRecords, phaseBRes, err = runReservePhase(ctx, orch, clients, testlib.PhaseB, "Eco")
		allReservations = append(allReservations, phaseBRes...)
	} else {
		phaseBRecords, err = runObservePhase(ctx, orch, clients, testlib.PhaseB, "Eco")
	}
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

func runObservePhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, phase, policy string) ([]testlib.SelectionRecord, error) {
	exp := orch.Config.Experiment
	var records []testlib.SelectionRecord

	consumerIDs := make([]string, 0, len(clients.Consoles))
	for id := range clients.Consoles {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	for i := 1; i <= exp.Iterations; i++ {
		if ctx.Err() != nil {
			return records, ctx.Err()
		}

		for _, consID := range consumerIDs {
			start := time.Now()

			ngResp, err := clients.Broker.GetNodeGroups(ctx)
			if err != nil {
				records = append(records, testlib.SelectionRecord{
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
				continue
			}

			winner := testlib.FindWinner(ngResp.NodeGroups)
			if winner == nil {
				growable := testlib.GrowableNodeGroups(ngResp.NodeGroups)
				records = append(records, testlib.SelectionRecord{
					Timestamp:    start,
					ConsumerID:   consID,
					Phase:        phase,
					Policy:       policy,
					Iteration:    i,
					Outcome:      "no-winner",
					ErrorMessage: fmt.Sprintf("growable=%d applied=%s", len(growable), ngResp.AppliedPlacement),
					DurationMs:   msSince(start),
				})
				log.Printf("[%s] iter %d %s: no single winner (growable=%d applied=%s)",
					phase, i, consID, len(growable), ngResp.AppliedPlacement)
				continue
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
			records = append(records, rec)

			log.Printf("[%s] iter %02d %s: winner=%-20s metric=%8.2f applied=%s",
				phase, i, consID, winner.ProviderClusterID, winner.PlacementMetric, ngResp.AppliedPlacement)
		}

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return records, err
			}
		}
	}

	return records, nil
}

func runReservePhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, phase, policy string) ([]testlib.SelectionRecord, []testlib.ReservationRecord, error) {
	exp := orch.Config.Experiment
	pollInterval := exp.ReservationPoll

	var selections []testlib.SelectionRecord
	var reservations []testlib.ReservationRecord

	consumerIDs := make([]string, 0, len(clients.Consoles))
	for id := range clients.Consoles {
		consumerIDs = append(consumerIDs, id)
	}
	sort.Strings(consumerIDs)

	active := make(map[string]*testlib.ConsumerReservation)
	seqNo := make(map[string]int)

	defer func() {
		for consID, res := range active {
			if res == nil {
				continue
			}
			log.Printf("[%s] cleanup: releasing %s (%s)", phase, res.ReservationID, consID)
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			if err := clients.Broker.ReleaseAndWait(cleanupCtx, res.ReservationID, res.Request, pollInterval); err != nil {
				log.Printf("[%s] cleanup: release error %s: %v", phase, res.ReservationID, err)
			}
			cancel()
		}
	}()

	for i := 1; i <= exp.Iterations; i++ {
		if ctx.Err() != nil {
			return selections, reservations, ctx.Err()
		}

		for _, consID := range consumerIDs {
			start := time.Now()

			ngResp, err := clients.Broker.GetNodeGroups(ctx)
			if err != nil {
				selections = append(selections, testlib.SelectionRecord{
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
				continue
			}

			winner := testlib.FindWinner(ngResp.NodeGroups)
			if winner == nil {
				growable := testlib.GrowableNodeGroups(ngResp.NodeGroups)
				selections = append(selections, testlib.SelectionRecord{
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
				continue
			}

			cur := active[consID]

			if !testlib.ShouldSwitch(cur, winner.ProviderClusterID, ngResp.NodeGroups) {
				curNG := testlib.FindNodeGroupByProvider(ngResp.NodeGroups, cur.ProviderClusterID)
				var curMetric float64
				var curHasMetric bool
				if curNG != nil {
					curMetric = curNG.PlacementMetric
					curHasMetric = curNG.HasMetric
				}

				selections = append(selections, testlib.SelectionRecord{
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
				reservations = append(reservations, testlib.ReservationRecord{
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
				continue
			}

			var prevProvider string
			var releaseMs float64

			if cur != nil {
				prevProvider = cur.ProviderClusterID
				releaseStart := time.Now()
				releaseCtx, releaseCancel := context.WithTimeout(ctx, 5*time.Minute)
				if err := clients.Broker.ReleaseAndWait(releaseCtx, cur.ReservationID, cur.Request, pollInterval); err != nil {
					log.Printf("[%s] iter %d %s: release error for %s: %v", phase, i, consID, cur.ReservationID, err)
				}
				releaseCancel()
				releaseMs = msSince(releaseStart)
				delete(active, consID)
			}

			seq := seqNo[consID]
			seqNo[consID] = seq + 1
			resID := testlib.MakeReservationID(orch.RunID, phase, consID, seq)

			req := &brokerapi.ReservationRequest{
				ProviderClusterID: winner.ProviderClusterID,
				NodeGroupID:       winner.ID,
				ChunkCount:        1,
				ChunkType:         brokerv1alpha1.ChunkTypeStandard,
			}

			peerStart := time.Now()
			peerCtx, peerCancel := context.WithTimeout(ctx, 5*time.Minute)
			resp, peerErr := clients.Broker.ReserveAndWait(peerCtx, resID, req, pollInterval)
			peerCancel()
			peerMs := msSince(peerStart)

			action := "create"
			if prevProvider != "" {
				action = "switch"
			}

			if peerErr != nil {
				finalPhase := ""
				if resp != nil {
					finalPhase = string(resp.Status)
				}
				selections = append(selections, testlib.SelectionRecord{
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
				reservations = append(reservations, testlib.ReservationRecord{
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
				continue
			}

			active[consID] = &testlib.ConsumerReservation{
				ReservationID:     resID,
				ProviderClusterID: winner.ProviderClusterID,
				NodeGroupID:       winner.ID,
				Request:           req,
			}

			selections = append(selections, testlib.SelectionRecord{
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
			reservations = append(reservations, testlib.ReservationRecord{
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
		}

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return selections, reservations, err
			}
		}
	}

	return selections, reservations, nil
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

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}
