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

// Package localapi is the consumer-side loopback REST server
// (docs/design.md §4.3). It runs on 127.0.0.1 by default and is
// consumed by the co-located gRPC server (step 10) — every Cluster
// Autoscaler RPC that needs broker-side state flows through this
// surface so the gRPC server never holds Broker mTLS credentials.
//
// Routes:
//
//	GET    /local/nodegroups             proxy to client.GetNodeGroups
//	POST   /local/reservations           proxy to client.PostReservation
//	DELETE /local/reservations/{id}      proxy to client.DeleteReservation
//	GET    /local/virtual-nodes          read VirtualNodeState CRs (empty
//	                                     until step 11 wires the reconciler)
//
// Failures from the broker are forwarded with the broker's HTTP status
// and ErrorResponse body so the gRPC server can map them to gRPC codes
// without losing context.
package localapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/google/uuid"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	agentclient "github.com/netgroup-polito/federation-autoscaler/internal/agent/client"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// Options bundles the construction-time settings of the loopback REST
// server.
type Options struct {
	// BindAddress is the listener address. Production wires
	// "127.0.0.1:9090" from --local-api-bind-address; binding to a
	// non-loopback host is allowed but logged as a warning.
	BindAddress string

	// Client is the Broker HTTP client. Every proxy route flows through
	// it. Required.
	Client *agentclient.Client

	// LocalClient reads the consumer-cluster's VirtualNodeState CRs for
	// /local/virtual-nodes. Required (even when the endpoint returns an
	// empty list, the server's startup contract is symmetric with the
	// other consumer subsystems).
	LocalClient ctrlclient.Client

	// Namespace is the namespace under which VirtualNodeState CRs are
	// expected to live. Empty means "all namespaces"; the reconciler in
	// step 11 will pick one canonical namespace.
	Namespace string

	// Logger is the structured logger every handler logs through.
	// Defaults to controller-runtime's logger named "consumer-localapi".
	Logger logr.Logger

	// ShutdownTimeout caps how long Run waits for in-flight requests
	// to drain when ctx is cancelled. Defaults to 5 s.
	ShutdownTimeout time.Duration
}

// Server is the local-API HTTP listener.
type Server struct {
	bind     string
	client   *agentclient.Client
	local    ctrlclient.Client
	ns       string
	log      logr.Logger
	shutdown time.Duration

	srv *http.Server
}

// New validates opts and returns a Server ready to Run. It performs no
// network I/O.
func New(opts Options) (*Server, error) {
	switch {
	case opts.BindAddress == "":
		return nil, errors.New("localapi: BindAddress is required")
	case opts.Client == nil:
		return nil, errors.New("localapi: Client is required")
	case opts.LocalClient == nil:
		return nil, errors.New("localapi: LocalClient is required")
	}
	logger := opts.Logger
	if logger.GetSink() == nil {
		logger = log.Log.WithName("consumer-localapi")
	}
	shutdown := opts.ShutdownTimeout
	if shutdown <= 0 {
		shutdown = 5 * time.Second
	}
	s := &Server{
		bind:     opts.BindAddress,
		client:   opts.Client,
		local:    opts.LocalClient,
		ns:       opts.Namespace,
		log:      logger,
		shutdown: shutdown,
	}
	s.srv = &http.Server{
		Addr:              opts.BindAddress,
		Handler:           s.handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s, nil
}

// Handler returns the bare router. Exposed so tests can mount the
// server on an httptest.NewServer without binding a real socket.
func (s *Server) Handler() http.Handler { return s.handler() }

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /local/nodegroups", s.handleNodeGroups)
	mux.HandleFunc("POST /local/reservations", s.handleReservationCreate)
	mux.HandleFunc("DELETE /local/reservations/{id}", s.handleReservationDelete)
	mux.HandleFunc("GET /local/virtual-nodes", s.handleVirtualNodes)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	return mux
}

// Run binds the listener and blocks until ctx is cancelled. Returns nil
// on graceful shutdown, the underlying error otherwise.
func (s *Server) Run(ctx context.Context) error {
	if !strings.HasPrefix(s.bind, "127.0.0.1:") && !strings.HasPrefix(s.bind, "[::1]:") {
		s.log.Info("WARNING: binding loopback REST server to a non-loopback address",
			"bindAddress", s.bind)
	}
	s.log.Info("starting consumer loopback REST server", "bindAddress", s.bind)

	shutdownDone := make(chan error, 1)
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdown)
		defer cancel()
		shutdownDone <- s.srv.Shutdown(shutdownCtx)
	}()

	err := s.srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return <-shutdownDone
	}
	return err
}

// -----------------------------------------------------------------------------
// Handlers
// -----------------------------------------------------------------------------

func (s *Server) handleNodeGroups(w http.ResponseWriter, r *http.Request) {
	resp, err := s.client.GetNodeGroups(r.Context())
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleReservationCreate(w http.ResponseWriter, r *http.Request) {
	var req brokerapi.ReservationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeRawError(w, http.StatusBadRequest, brokerapi.ErrCodeInvalidRequest,
			fmt.Sprintf("decode body: %v", err))
		return
	}

	// Propagate the caller's idempotency key when present; mint a UUID
	// when absent so the broker still benefits from CRD-backed dedup.
	reservationID := r.Header.Get(brokerapi.HeaderReservationID)
	if reservationID == "" {
		reservationID = "res-" + uuid.NewString()
	}

	resp, err := s.client.PostReservation(r.Context(), reservationID, &req)
	if err != nil {
		s.writeError(w, err)
		return
	}
	w.Header().Set(brokerapi.HeaderReservationID, reservationID)
	s.writeJSON(w, http.StatusCreated, resp)
}

func (s *Server) handleReservationDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeRawError(w, http.StatusBadRequest, brokerapi.ErrCodeInvalidRequest,
			"reservation id is required")
		return
	}
	resp, err := s.client.DeleteReservation(r.Context(), id)
	if err != nil {
		s.writeError(w, err)
		return
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleVirtualNodes(w http.ResponseWriter, _ *http.Request) {
	// Step 11 will populate this list from VirtualNodeState CRs. For
	// 9c the consumer has no reconciler producing those, so we return
	// an empty slice — the gRPC server's NodeGroupTargetSize will just
	// report 0 for every node group, which is correct in the pre-step-11
	// state where no peering has materialised yet.
	s.writeJSON(w, http.StatusOK, VirtualNodeListResponse{VirtualNodes: []VirtualNodeView{}})
}

// -----------------------------------------------------------------------------
// Response helpers
// -----------------------------------------------------------------------------

func (s *Server) writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", brokerapi.ContentTypeJSON)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		s.log.V(1).Info("encode response failed", "err", err.Error())
	}
}

// writeError forwards broker-originated errors to the caller with the
// broker's status code preserved, so the gRPC server in step 10 can
// map e.g. 412 PreconditionFailed → FAILED_PRECONDITION without
// re-inspecting strings.
func (s *Server) writeError(w http.ResponseWriter, err error) {
	var ce *agentclient.Error
	if errors.As(err, &ce) && ce.Status > 0 {
		s.writeJSON(w, ce.Status, brokerapi.ErrorResponse{
			Code:      ce.Code,
			Message:   ce.Message,
			RequestID: ce.RequestID,
		})
		return
	}
	s.writeRawError(w, http.StatusInternalServerError, brokerapi.ErrCodeInternalError, err.Error())
}

func (s *Server) writeRawError(w http.ResponseWriter, status int, code brokerapi.ErrorCode, msg string) {
	s.writeJSON(w, status, brokerapi.ErrorResponse{Code: code, Message: msg})
}
