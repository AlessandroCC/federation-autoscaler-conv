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

// Package instructions implements the per-kind handlers the consumer-
// role agent registers on the shared poller.Registry. Mirror of the
// provider's instructions package; the two are intentionally siblings
// rather than a shared module so a future tightening of either's API
// surface doesn't ripple across roles.
package instructions

import (
	"bytes"
	"context"
	"os/exec"
)

// RunFunc is the seam the handlers call to invoke an external binary.
// Production code uses defaultRunFunc; tests inject a fake so they do
// not spawn liqoctl processes.
type RunFunc func(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error)

// defaultRunFunc is the production exec implementation: it builds an
// exec.CommandContext, captures stdout and stderr separately, and
// returns the captured byte slices regardless of exit status.
func defaultRunFunc(ctx context.Context, name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err = cmd.Run()
	return outBuf.Bytes(), errBuf.Bytes(), err
}
