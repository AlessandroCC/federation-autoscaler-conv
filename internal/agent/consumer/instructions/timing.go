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
	"time"

	"github.com/go-logr/logr"
)

// logStep emits one millisecond-resolution timing line for an internal step of
// an instruction handler.
//
// Why a log line rather than a status field: the instruction CR already records
// when the agent picked the work up (Status.LastDeliveredAt) and when it
// reported back (Status.LastUpdateTime), which is enough to separate poll
// latency from handler execution. But both are metav1.Time — RFC3339 at
// one-second resolution — so a cold `liqoctl peer` shows up as one opaque
// 40-90 s block, and the sub-second steps bracketing it cannot be resolved at
// all. These lines carry the agent's own millisecond clock, and
// deploy/bench/collect-run.sh merges them with the broker's to reconstruct a
// full scale-up / scale-down timeline.
//
// The shape is load-bearing: message "timing", key "event" holding
// "<handler>.<step>", and "elapsedMs" holding an integer millisecond count.
// deploy/bench parses on exactly that, so extend the key set rather than
// renaming what is already there.
func logStep(logger logr.Logger, handler, step string, start time.Time) {
	logger.Info("timing",
		"event", handler+"."+step,
		"elapsedMs", time.Since(start).Milliseconds())
}
