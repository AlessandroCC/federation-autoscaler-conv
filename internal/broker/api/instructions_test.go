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
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestDeliveryTouchDue pins the rate limit on the LastDeliveredAt write.
//
// This gate is what decouples broker write load from the agent poll interval:
// before it, every poll that carried a pending instruction wrote to the
// apiserver and triggered a reconcile cascade, so halving the poll interval
// doubled the load. A regression here would silently reintroduce that coupling
// and quietly punish every future attempt to lower --poll-interval.
func TestDeliveryTouchDue(t *testing.T) {
	now := metav1.NewTime(time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC))

	tests := []struct {
		name string
		last *metav1.Time
		want bool
	}{
		{
			name: "first delivery always writes",
			last: nil,
			want: true,
		},
		{
			name: "redelivery inside the window is suppressed",
			last: ptrTime(now.Add(-deliveryTouchInterval / 2)),
			want: false,
		},
		{
			name: "redelivery exactly at the window writes",
			last: ptrTime(now.Add(-deliveryTouchInterval)),
			want: true,
		},
		{
			name: "stale delivery writes",
			last: ptrTime(now.Add(-10 * deliveryTouchInterval)),
			want: true,
		},
		{
			// Clock skew between the broker and a stamped value must not wedge
			// the field permanently: a future timestamp reads as "recent".
			name: "timestamp in the future is treated as recent",
			last: ptrTime(now.Add(time.Minute)),
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := deliveryTouchDue(tc.last, now); got != tc.want {
				t.Errorf("deliveryTouchDue() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDeliveryTouchIntervalBoundsWriteRate documents the property the constant
// exists for: the write rate must not scale with the poll rate.
func TestDeliveryTouchIntervalBoundsWriteRate(t *testing.T) {
	start := metav1.NewTime(time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC))

	for _, pollInterval := range []time.Duration{
		5 * time.Second, 2 * time.Second, time.Second, 250 * time.Millisecond,
	} {
		// Replay one minute of polls at this cadence against the gate,
		// advancing the recorded timestamp whenever a write would happen.
		var writes int
		last := (*metav1.Time)(nil)
		for elapsed := time.Duration(0); elapsed < time.Minute; elapsed += pollInterval {
			at := metav1.NewTime(start.Add(elapsed))
			if deliveryTouchDue(last, at) {
				writes++
				stamped := at
				last = &stamped
			}
		}

		// 60 s of polling can produce at most ⌈60/30⌉ writes regardless of how
		// fast the agent polls. Without the gate a 250 ms poll would write 240
		// times in the same window.
		if maxWrites := 2; writes > maxWrites {
			t.Errorf("poll=%v produced %d writes in 1 min, want <= %d",
				pollInterval, writes, maxWrites)
		}
	}
}

func ptrTime(t time.Time) *metav1.Time {
	mt := metav1.NewTime(t)
	return &mt
}
