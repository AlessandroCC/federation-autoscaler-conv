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

package agentclient

import (
	"errors"
	"fmt"
	"net/http"

	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// Error is the typed shape returned for every non-2xx response. The
// gRPC server's mutating RPCs (substep 10d) map these onto gRPC status
// codes — e.g. CategoryPreconditionFailed → FAILED_PRECONDITION —
// without parsing strings.
//
// The consumer agent's loopback REST proxy (internal/agent/consumer/
// localapi) forwards broker errors with the broker's status code
// preserved, so the values surfaced here are the broker's own
// classifications.
type Error struct {
	Category Category

	// Code mirrors brokerapi.ErrorCode in the wire body when present.
	// Empty for transport-level failures with no body.
	Code brokerapi.ErrorCode

	Message   string
	Status    int    // HTTP status code; 0 for pre-response failures.
	RequestID string // mirrors X-Request-Id on the response.

	// Cause carries the underlying network or decode error when one
	// exists; nil for clean HTTP-layer rejections.
	Cause error
}

// Error formats the typed error in a stable shape callers can grep for
// in logs.
func (e *Error) Error() string {
	switch {
	case e == nil:
		return ""
	case e.Status == 0:
		return fmt.Sprintf("agentclient: transient: %v", e.Cause)
	case e.Code == "":
		return fmt.Sprintf("agentclient: HTTP %d: %s", e.Status, e.Message)
	default:
		return fmt.Sprintf("agentclient: HTTP %d %s: %s", e.Status, e.Code, e.Message)
	}
}

// Unwrap exposes the underlying Cause so errors.Is / errors.As traverse
// past the categorisation envelope.
func (e *Error) Unwrap() error { return e.Cause }

// Category groups HTTP failures into stable buckets the gRPC layer can
// switch on when mapping to gRPC codes.
type Category int

const (
	CategoryUnknown            Category = iota
	CategoryTransient                   // 5xx / network / decode failures
	CategoryBadRequest                  // 400
	CategoryUnauthenticated             // 401
	CategoryForbidden                   // 403
	CategoryNotFound                    // 404
	CategoryConflict                    // 409
	CategoryPreconditionFailed          // 412
	CategoryTooManyRequests             // 429
)

func IsTransient(err error) bool          { return is(err, CategoryTransient) }
func IsBadRequest(err error) bool         { return is(err, CategoryBadRequest) }
func IsUnauthenticated(err error) bool    { return is(err, CategoryUnauthenticated) }
func IsForbidden(err error) bool          { return is(err, CategoryForbidden) }
func IsNotFound(err error) bool           { return is(err, CategoryNotFound) }
func IsConflict(err error) bool           { return is(err, CategoryConflict) }
func IsPreconditionFailed(err error) bool { return is(err, CategoryPreconditionFailed) }
func IsTooManyRequests(err error) bool    { return is(err, CategoryTooManyRequests) }

func is(err error, want Category) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Category == want
}

// classify maps an HTTP status to a Category. Unknown 4xx fall through
// to CategoryBadRequest; unknown 5xx to CategoryTransient.
func classify(status int) Category {
	switch {
	case status >= 500:
		return CategoryTransient
	case status == http.StatusBadRequest:
		return CategoryBadRequest
	case status == http.StatusUnauthorized:
		return CategoryUnauthenticated
	case status == http.StatusForbidden:
		return CategoryForbidden
	case status == http.StatusNotFound:
		return CategoryNotFound
	case status == http.StatusConflict:
		return CategoryConflict
	case status == http.StatusPreconditionFailed:
		return CategoryPreconditionFailed
	case status == http.StatusTooManyRequests:
		return CategoryTooManyRequests
	default:
		return CategoryBadRequest
	}
}
