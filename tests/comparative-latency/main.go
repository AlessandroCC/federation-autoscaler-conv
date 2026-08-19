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

	orch, err := testlib.NewOrchestrator(cfg, "comparative-latency", keepClusters, skipBuild, runID)
	if err != nil {
		log.Fatalf("orchestrator: %v", err)
	}

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
	var tcDelays []*testlib.TCDelayKind
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
			for _, applied := range tcDelays {
				_ = applied.Restore()
			}
			return fmt.Errorf("apply tc on %s: %w", containerName, err)
		}
		tcDelays = append(tcDelays, tc)
	}
	defer func() {
		for _, tc := range tcDelays {
			log.Printf("  restoring tc on %s", tc.ContainerName)
			if err := tc.Restore(); err != nil {
				log.Printf("  tc restore error on %s: %v", tc.ContainerName, err)
			}
		}
	}()

	var allSelections []testlib.SelectionRecord
	var allProbes []testlib.ProbeRecord
	var allReservations []testlib.ReservationRecord
	mode := exp.Mode

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

func runLatencyPhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, endpoints map[string]string, phase, policy string) ([]testlib.SelectionRecord, []testlib.ProbeRecord, error) {
	exp := orch.Config.Experiment
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

		for _, consID := range consumerIDs {
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

			ngResp, err := broker.GetNodeGroups(ctx)
			if err != nil {
				rec.Outcome = "error"
				rec.ErrorMessage = fmt.Sprintf("get nodegroups: %v", err)
				rec.DurationMs = msSince(start)
				selections = append(selections, rec)
				log.Printf("[%s] iter %d %s: nodegroups error: %v", phase, i, consID, err)
				continue
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
					selections = append(selections, rec)
					continue
				}

				probeResp, err := console.Probe(ctx, candidates)
				if err != nil {
					rec.Outcome = "probe-error"
					rec.ErrorMessage = err.Error()
					rec.DurationMs = msSince(start)
					selections = append(selections, rec)
					continue
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
				selections = append(selections, rec)

				probes = append(probes, testlib.ProbeRecord{
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
					selections = append(selections, rec)
					continue
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
						probes = append(probes, testlib.ProbeRecord{
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
				selections = append(selections, rec)
			}

			log.Printf("[%s] iter %02d %s: winner=%-20s rtt=%8.2fms shortlist=%v",
				phase, i, consID, rec.SelectedID, rec.RTTMs, ngResp.LatencyShortlist)
		}

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return selections, probes, err
			}
		}
	}

	return selections, probes, nil
}

func runReservePhase(ctx context.Context, orch *testlib.Orchestrator, clients *testlib.ExperimentClients, endpoints map[string]string, phase, policy string) ([]testlib.SelectionRecord, []testlib.ProbeRecord, []testlib.ReservationRecord, error) {
	exp := orch.Config.Experiment
	pollInterval := exp.ReservationPoll

	var selections []testlib.SelectionRecord
	var probes []testlib.ProbeRecord
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
			if err := clients.BrokerFor(consID).ReleaseAndWait(cleanupCtx, res.ReservationID, res.Request, pollInterval); err != nil {
				log.Printf("[%s] cleanup: release error %s: %v", phase, res.ReservationID, err)
			}
			cancel()
		}
	}()

	for i := 1; i <= exp.Iterations; i++ {
		if ctx.Err() != nil {
			return selections, probes, reservations, ctx.Err()
		}

		for _, consID := range consumerIDs {
			console := clients.Consoles[consID]
			broker := clients.BrokerFor(consID)
			start := time.Now()

			ngResp, err := broker.GetNodeGroups(ctx)
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

			// Determine winner via probing (latency shortlist) or single winner.
			var chosenProviderID, chosenNodeGroupID string
			var chosenRTT float64
			var probeRec *testlib.ProbeRecord

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
					selections = append(selections, testlib.SelectionRecord{
						Timestamp:    start,
						ConsumerID:   consID,
						Phase:        phase,
						Policy:       policy,
						Iteration:    i,
						Outcome:      "no-candidates",
						ErrorMessage: "no growable with ProbeEndpoint",
						DurationMs:   msSince(start),
					})
					continue
				}

				probeResp, err := console.Probe(ctx, candidates)
				if err != nil {
					selections = append(selections, testlib.SelectionRecord{
						Timestamp:    start,
						ConsumerID:   consID,
						Phase:        phase,
						Policy:       policy,
						Iteration:    i,
						Outcome:      "probe-error",
						ErrorMessage: err.Error(),
						DurationMs:   msSince(start),
					})
					continue
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
					selections = append(selections, testlib.SelectionRecord{
						Timestamp:  start,
						ConsumerID: consID,
						Phase:      phase,
						Policy:     policy,
						Iteration:  i,
						Outcome:    "no-winner",
						DurationMs: msSince(start),
					})
					continue
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

			cur := active[consID]

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
					probes = append(probes, *probeRec)
				}
				selections = append(selections, testlib.SelectionRecord{
					Timestamp:     start,
					ConsumerID:    consID,
					Phase:         phase,
					Policy:        policy,
					Iteration:     i,
					SelectedID:    cur.ProviderClusterID,
					NodeGroupID:   cur.NodeGroupID,
					ReservationID: cur.ReservationID,
					RTTMs:         chosenRTT,
					Outcome:       "success",
					DurationMs:    msSince(start),
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
					RTTMs:             chosenRTT,
					Outcome:           "success",
					TotalMs:           msSince(start),
				})
				log.Printf("[%s] iter %02d %s: keep  %-20s (rtt=%.2fms)", phase, i, consID, cur.ProviderClusterID, chosenRTT)
				continue
			}

			if probeRec != nil {
				probes = append(probes, *probeRec)
			}

			var prevProvider string
			var releaseMs float64
			if cur != nil {
				prevProvider = cur.ProviderClusterID
				releaseStart := time.Now()
				releaseCtx, releaseCancel := context.WithTimeout(ctx, exp.ReservationTimeout)
				if err := broker.ReleaseAndWait(releaseCtx, cur.ReservationID, cur.Request, pollInterval); err != nil {
					log.Printf("[%s] iter %d %s: release error for %s: %v", phase, i, consID, cur.ReservationID, err)
				}
				releaseCancel()
				releaseMs = msSince(releaseStart)
				delete(active, consID)
			}

			seq := seqNo[consID]
			seqNo[consID] = seq + 1
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
				finalPhase := ""
				if resp != nil {
					finalPhase = string(resp.Status)
				}
				selections = append(selections, testlib.SelectionRecord{
					Timestamp:    start,
					ConsumerID:   consID,
					Phase:        phase,
					Policy:       policy,
					Iteration:    i,
					SelectedID:   chosenProviderID,
					NodeGroupID:  chosenNodeGroupID,
					ReservationID: resID,
					RTTMs:        chosenRTT,
					Outcome:      "reserve-error",
					ErrorMessage: peerErr.Error(),
					DurationMs:   msSince(start),
				})
				reservations = append(reservations, testlib.ReservationRecord{
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
				})
				log.Printf("[%s] iter %d %s: %s error → %s: %v", phase, i, consID, action, chosenProviderID, peerErr)
				continue
			}

			active[consID] = &testlib.ConsumerReservation{
				ReservationID:     resID,
				ProviderClusterID: chosenProviderID,
				NodeGroupID:       chosenNodeGroupID,
				Request:           req,
			}

			selections = append(selections, testlib.SelectionRecord{
				Timestamp:     start,
				ConsumerID:    consID,
				Phase:         phase,
				Policy:        policy,
				Iteration:     i,
				SelectedID:    chosenProviderID,
				NodeGroupID:   chosenNodeGroupID,
				ReservationID: resID,
				RTTMs:         chosenRTT,
				Outcome:       "success",
				DurationMs:    msSince(start),
			})
			reservations = append(reservations, testlib.ReservationRecord{
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
			})
			log.Printf("[%s] iter %02d %s: %s %-20s (res=%s peer=%.0fms rel=%.0fms rtt=%.2fms)",
				phase, i, consID, action, chosenProviderID, resID, peerMs, releaseMs, chosenRTT)
		}

		if i < exp.Iterations {
			if err := testlib.SleepCtx(ctx, exp.PhasePause); err != nil {
				return selections, probes, reservations, err
			}
		}
	}

	return selections, probes, reservations, nil
}

func summarizeLatencyPhase(selections []testlib.SelectionRecord, _ []testlib.ProbeRecord) testlib.PhaseSummary {
	s := testlib.PhaseSummary{
		Iterations:      len(selections),
		SelectionCounts: make(map[string]int),
	}
	var rttValues []float64
	for _, r := range selections {
		if r.Outcome == "success" {
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

func msSince(t time.Time) float64 {
	return float64(time.Since(t).Microseconds()) / 1000.0
}
