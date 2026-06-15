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
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/manager"
)

// Compile-time interface checks: like Runnable, DashboardRunnable plugs into the
// controller-runtime manager and opts into leader election.
var (
	_ manager.Runnable               = (*DashboardRunnable)(nil)
	_ manager.LeaderElectionRunnable = (*DashboardRunnable)(nil)
)

// DashboardRunnableOptions configures the read-only dashboard listener. Empty
// fields fall back to defaults.
type DashboardRunnableOptions struct {
	// BindAddress is the host:port the plain-HTTP listener binds to. Defaults
	// to ":9444". An empty value is treated by cmd/broker as "dashboard
	// disabled" and the runnable is never constructed.
	BindAddress string

	// ShutdownTimeout caps how long Start waits for in-flight requests to drain
	// on ctx cancellation. Defaults to 15s.
	ShutdownTimeout time.Duration

	// Logger is used for setup/serve messages. Defaults to
	// ctrl.Log.WithName("broker-dashboard").
	Logger logr.Logger
}

// DashboardRunnable adapts the Broker's read-only dashboard to the
// manager.Runnable interface. Unlike Runnable it serves PLAIN HTTP — no TLS,
// no mTLS middleware — so a browser can reach it directly. It wraps the SAME
// *Server the mTLS API runnable uses, so it shares that server's cached client,
// chunk sizer, and (crucially) the consumer registry the heartbeat handler
// fills; otherwise the Consumers panel would always be empty.
type DashboardRunnable struct {
	httpSrv  *http.Server
	bindAddr string
	shutdown time.Duration
	log      logr.Logger
}

// NewDashboardRunnableFromServer builds a dashboard runnable that serves srv's
// read-only views. srv MUST be the same *Server registered for the mTLS API
// (see Runnable.Server) so the Consumers panel reflects live heartbeats.
func NewDashboardRunnableFromServer(srv *Server, opts DashboardRunnableOptions) (*DashboardRunnable, error) {
	if srv == nil {
		return nil, errors.New("broker dashboard: server is required")
	}

	bind := opts.BindAddress
	if bind == "" {
		bind = ":9444"
	}
	shutdown := opts.ShutdownTimeout
	if shutdown <= 0 {
		shutdown = 15 * time.Second
	}
	log := opts.Logger
	if log.GetSink() == nil {
		log = ctrl.Log.WithName("broker-dashboard")
	}

	httpSrv := &http.Server{
		Addr: bind,
		// Auth-free chain (recover/requestID/logger) over the dashboard-only
		// mux. No TLSConfig — this is the deliberate, browser-reachable,
		// unauthenticated read-only surface.
		Handler: srv.DashboardMiddlewareChain(srv.DashboardHandler()),

		// Conservative timeouts mirror the mTLS API runnable.
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

	return &DashboardRunnable{
		httpSrv:  httpSrv,
		bindAddr: bind,
		shutdown: shutdown,
		log:      log,
	}, nil
}

// NeedLeaderElection returns true so the dashboard serves only on the elected
// Broker pod — the same pod that terminates agent traffic and has a warm
// informer cache. Production runs a single replica, so this is invisible; at
// >1 replicas a NodePort would intermittently hit a non-leader and refuse,
// matching the mTLS API's behaviour. The dashboard is read-only, so flipping
// this to false (serve on every pod) would also be safe if ever desired.
func (*DashboardRunnable) NeedLeaderElection() bool { return true }

// Start serves plain HTTP until ctx is cancelled, then drains gracefully within
// ShutdownTimeout. Mirrors Runnable.Start but calls ListenAndServe (no TLS).
func (r *DashboardRunnable) Start(ctx context.Context) error {
	r.log.Info("starting broker dashboard server (read-only, plain HTTP)",
		"bindAddress", r.bindAddr)

	serveErr := make(chan error, 1)
	go func() {
		err := r.httpSrv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		r.log.Info("draining broker dashboard server", "shutdownTimeout", r.shutdown)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), r.shutdown)
		defer cancel()
		if err := r.httpSrv.Shutdown(shutdownCtx); err != nil {
			// Force-close on shutdown failure so the manager is not blocked.
			_ = r.httpSrv.Close()
			return fmt.Errorf("broker dashboard: graceful shutdown: %w", err)
		}
		if err := <-serveErr; err != nil {
			return fmt.Errorf("broker dashboard: serve: %w", err)
		}
		return nil
	case err := <-serveErr:
		return fmt.Errorf("broker dashboard: serve: %w", err)
	}
}
