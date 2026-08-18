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
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Phase labels used throughout the comparative tests.
const (
	PhaseSetup      = "setup"
	PhaseWarmupA    = "warmup-a"
	PhaseA          = "phase-a"
	PhaseTransition = "transition"
	PhaseWarmupB    = "warmup-b"
	PhaseB          = "phase-b"
	PhaseCleanup    = "cleanup"
)

// SelectionRecord captures one reservation attempt: which provider was picked,
// under which policy, and any measured RTT.
type SelectionRecord struct {
	Timestamp      time.Time `json:"timestamp"`
	Phase          string    `json:"phase"`
	Policy         string    `json:"policy"`
	Iteration      int       `json:"iteration"`
	SelectedID     string    `json:"selectedProviderId"`
	NodeGroupID    string    `json:"nodeGroupId"`
	ReservationID  string    `json:"reservationId"`
	PlacementValue float64   `json:"placementValue,omitempty"`
	HasMetric      bool      `json:"hasMetric,omitempty"`
	RTTMs          float64   `json:"rttMs,omitempty"`
	DurationMs     float64   `json:"durationMs"`
	Outcome        string    `json:"outcome"`
	ErrorMessage   string    `json:"errorMessage,omitempty"`
}

var selectionCSVHeader = []string{
	"timestamp", "phase", "policy", "iteration",
	"selected_provider_id", "nodegroup_id", "reservation_id",
	"placement_value", "has_metric", "rtt_ms",
	"duration_ms", "outcome", "error_message",
}

func selectionCSVRow(r SelectionRecord) []string {
	return []string{
		r.Timestamp.UTC().Format(time.RFC3339Nano),
		r.Phase,
		r.Policy,
		strconv.Itoa(r.Iteration),
		r.SelectedID,
		r.NodeGroupID,
		r.ReservationID,
		strconv.FormatFloat(r.PlacementValue, 'f', 4, 64),
		strconv.FormatBool(r.HasMetric),
		strconv.FormatFloat(r.RTTMs, 'f', 3, 64),
		strconv.FormatFloat(r.DurationMs, 'f', 3, 64),
		r.Outcome,
		r.ErrorMessage,
	}
}

// WriteSelectionCSV writes selection records to a CSV file in the output dir.
func WriteSelectionCSV(dir, name string, records []SelectionRecord) error {
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	if err := w.Write(selectionCSVHeader); err != nil {
		return err
	}
	for _, r := range records {
		if err := w.Write(selectionCSVRow(r)); err != nil {
			return err
		}
	}
	return w.Error()
}

// ProbeRecord captures one RTT measurement session.
type ProbeRecord struct {
	Timestamp  time.Time          `json:"timestamp"`
	Phase      string             `json:"phase"`
	Policy     string             `json:"policy"`
	Iteration  int                `json:"iteration"`
	Chosen     string             `json:"chosen"`
	RTTs       map[string]float64 `json:"rtts"`
	DurationMs float64            `json:"durationMs"`
}

// WriteProbeCSV writes probe measurement records to a CSV file.
func WriteProbeCSV(dir, name string, records []ProbeRecord) error {
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	defer w.Flush()

	header := []string{"timestamp", "phase", "policy", "iteration", "chosen", "duration_ms", "provider_id", "rtt_ms"}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range records {
		for providerID, rtt := range r.RTTs {
			row := []string{
				r.Timestamp.UTC().Format(time.RFC3339Nano),
				r.Phase,
				r.Policy,
				strconv.Itoa(r.Iteration),
				r.Chosen,
				strconv.FormatFloat(r.DurationMs, 'f', 3, 64),
				providerID,
				strconv.FormatFloat(rtt, 'f', 3, 64),
			}
			if err := w.Write(row); err != nil {
				return err
			}
		}
	}
	return w.Error()
}

// ExperimentSummary is the top-level JSON summary of a comparative run.
type ExperimentSummary struct {
	RunID           string    `json:"runId"`
	TestType        string    `json:"testType"`
	StartTime       time.Time `json:"startTime"`
	EndTime         time.Time `json:"endTime"`
	ConsumerID      string    `json:"consumerId"`
	ConsumerCertFP  string    `json:"consumerCertFingerprint,omitempty"`
	BrokerURL       string    `json:"brokerUrl"`
	ConsoleURL      string    `json:"consoleUrl"`
	ProviderCount   int       `json:"providerCount"`
	IterationsPerPhase int    `json:"iterationsPerPhase"`
	PhaseAPolicy    string    `json:"phaseAPolicy"`
	PhaseBPolicy    string    `json:"phaseBPolicy"`
	PhaseASummary   PhaseSummary `json:"phaseASummary"`
	PhaseBSummary   PhaseSummary `json:"phaseBSummary"`
}

// PhaseSummary aggregates one measurement phase's results.
type PhaseSummary struct {
	Iterations       int                `json:"iterations"`
	Successes        int                `json:"successes"`
	Failures         int                `json:"failures"`
	SelectionCounts  map[string]int     `json:"selectionCounts"`
	MeanRTTMs        float64            `json:"meanRttMs,omitempty"`
	MedianRTTMs      float64            `json:"medianRttMs,omitempty"`
	MeanPlacementVal float64            `json:"meanPlacementValue,omitempty"`
}

// WriteJSONFile writes an arbitrary value as pretty-printed JSON.
func WriteJSONFile(dir, name string, v any) error {
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// WriteSummaryMarkdown renders a human-readable companion to summary.json.
func WriteSummaryMarkdown(dir string, s ExperimentSummary) error {
	f, err := os.Create(filepath.Join(dir, "summary.md"))
	if err != nil {
		return err
	}
	defer f.Close()

	fmt.Fprintf(f, "# Comparative Test Summary: %s\n\n", s.TestType)
	fmt.Fprintf(f, "- Run ID: `%s`\n", s.RunID)
	fmt.Fprintf(f, "- Start: %s\n", s.StartTime.UTC().Format(time.RFC3339))
	fmt.Fprintf(f, "- End: %s\n", s.EndTime.UTC().Format(time.RFC3339))
	fmt.Fprintf(f, "- Consumer: `%s`\n", s.ConsumerID)
	fmt.Fprintf(f, "- Broker: %s\n", s.BrokerURL)
	fmt.Fprintf(f, "- Providers: %d\n", s.ProviderCount)
	fmt.Fprintf(f, "- Iterations per phase: %d\n\n", s.IterationsPerPhase)

	for _, ps := range []struct {
		label   string
		policy  string
		summary PhaseSummary
	}{
		{"Phase A (baseline)", s.PhaseAPolicy, s.PhaseASummary},
		{"Phase B (policy-aware)", s.PhaseBPolicy, s.PhaseBSummary},
	} {
		fmt.Fprintf(f, "## %s — %s\n\n", ps.label, ps.policy)
		fmt.Fprintf(f, "- Iterations: %d (success: %d, fail: %d)\n",
			ps.summary.Iterations, ps.summary.Successes, ps.summary.Failures)
		fmt.Fprintf(f, "- Selection distribution:\n")
		for id, count := range ps.summary.SelectionCounts {
			fmt.Fprintf(f, "  - `%s`: %d (%.1f%%)\n", id, count, 100*float64(count)/float64(max(ps.summary.Iterations, 1)))
		}
		if ps.summary.MeanRTTMs > 0 {
			fmt.Fprintf(f, "- Mean RTT: %.2f ms\n", ps.summary.MeanRTTMs)
			fmt.Fprintf(f, "- Median RTT: %.2f ms\n", ps.summary.MedianRTTMs)
		}
		if ps.summary.MeanPlacementVal > 0 {
			fmt.Fprintf(f, "- Mean placement value: %.4f\n", ps.summary.MeanPlacementVal)
		}
		fmt.Fprintf(f, "\n")
	}
	return nil
}

// EnsureOutputDir creates the output directory for a test run.
func EnsureOutputDir(base, testType string) (string, error) {
	dir := filepath.Join(base, testType, time.Now().UTC().Format("20060102T150405Z"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir %q: %w", dir, err)
	}
	return dir, nil
}
