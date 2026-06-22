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

// Package geo is a tiny, unauthenticated HTTP client for a region→coordinates
// lookup service (mock-geo in the demo, a real geo API in production). It is
// used by BOTH agent roles for the latency placement strategy: each agent looks
// up its OWN region's coordinates and advertises them (providers on the
// ClusterAdvertisement, the consumer on its heartbeat). It deliberately does
// NOT go through the mTLS Broker client — the geo service is a separate,
// credential-free endpoint.
package geo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// LatLon is a region's geographic coordinates in decimal degrees.
type LatLon struct {
	Lat float64
	Lon float64
}

// latLonResponse mirrors the mock-geo GET /latlon?region=XX JSON response.
type latLonResponse struct {
	Region string  `json:"region"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// Client looks up region coordinates and caches them permanently per region (a
// region's location is static). Safe for concurrent use.
type Client struct {
	mu    sync.Mutex
	cache map[string]LatLon
	http  *http.Client
}

// NewClient returns a geo Client with a 5 s per-request timeout.
func NewClient() *Client {
	return &Client{
		cache: make(map[string]LatLon),
		http:  &http.Client{Timeout: 5 * time.Second},
	}
}

// Lookup returns the coordinates for region from baseURL (e.g.
// http://mock-geo:8080), caching the result. ok is false with a nil error when
// the lookup is disabled (baseURL or region empty). A non-nil error means the
// service was configured but unreachable/invalid; the caller should proceed
// without coordinates.
func (c *Client) Lookup(ctx context.Context, baseURL, region string) (LatLon, bool, error) {
	if baseURL == "" || region == "" {
		return LatLon{}, false, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if v, ok := c.cache[region]; ok {
		return v, true, nil
	}

	reqURL := fmt.Sprintf("%s/latlon?region=%s", baseURL, url.QueryEscape(region))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return LatLon{}, false, fmt.Errorf("geo: build request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return LatLon{}, false, fmt.Errorf("geo: call mock-geo: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return LatLon{}, false, fmt.Errorf("geo: mock-geo returned status %d for region %q", resp.StatusCode, region)
	}

	var out latLonResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return LatLon{}, false, fmt.Errorf("geo: decode response: %w", err)
	}

	v := LatLon{Lat: out.Lat, Lon: out.Lon}
	c.cache[region] = v
	return v, true, nil
}
