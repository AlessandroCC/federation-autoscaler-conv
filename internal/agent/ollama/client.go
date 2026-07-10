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

// Package ollama is a lightweight HTTP client for a local Ollama instance
// (https://ollama.ai) used by the ConsumerChoice placement strategy. It sends
// a structured provider list and a natural-language user request to the LLM,
// which returns the ID of the chosen provider as a JSON object.
//
// The client is used ONLY by the consumer role's localapi server when the
// active ConsumerPolicy has placement type "ConsumerChoice". It never contacts
// the Broker or any external service — it dials localhost (or an in-cluster
// Ollama Service) only.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
)

// Client calls a local Ollama instance to select a provider. Safe for
// concurrent use; each Select call is independent.
type Client struct {
	baseURL string        // e.g. "http://localhost:11434"
	model   string        // e.g. "llama3.2"
	timeout time.Duration // per-request timeout for the /api/generate call
	http    *http.Client
}

// New returns an Ollama Client. baseURL is the Ollama API root (e.g.
// "http://localhost:11434"), model is the model name (e.g. "llama3.2").
func New(baseURL, model string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		model:   model,
		timeout: 30 * time.Second,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// ollamaRequest is the JSON body sent to POST /api/generate.
type ollamaRequest struct {
	Model  string `json:"model"`
	System string `json:"system"`
	Prompt string `json:"prompt"`
	Format string `json:"format"`
	Stream bool   `json:"stream"`
}

// ollamaResponse is the JSON body returned by POST /api/generate (non-streaming).
type ollamaResponse struct {
	Response string `json:"response"`
	Done     bool   `json:"done"`
}

// Select sends the provider list and user prompt to Ollama and returns the
// chosen provider's ClusterID. Returns ("", err) on failure; the caller MUST
// fall back to a deterministic strategy when err != nil.
func (c *Client) Select(ctx context.Context, userPrompt string, nodeGroups []brokerapi.NodeGroupView) (string, error) {
	// Build provider info list (only providers with available capacity).
	var providers []ProviderInfo
	validIDs := make(map[string]struct{})
	for _, ng := range nodeGroups {
		if ng.MaxSize > ng.CurrentReserved {
			info := NodeGroupViewToProviderInfo(ng)
			providers = append(providers, info)
			validIDs[ng.ProviderClusterID] = struct{}{}
		}
	}
	if len(providers) == 0 {
		return "", fmt.Errorf("no providers with available capacity")
	}

	// If there is only one provider, skip the AI call entirely.
	if len(providers) == 1 {
		return providers[0].ProviderID, nil
	}

	// Build the prompt.
	prompt := BuildUserPrompt(userPrompt, providers)

	// Build the Ollama request.
	reqBody := ollamaRequest{
		Model:  c.model,
		System: SystemPrompt,
		Prompt: prompt,
		Format: "json",
		Stream: false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("marshal ollama request: %w", err)
	}

	// Make the HTTP call.
	reqCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost,
		c.baseURL+"/api/generate", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("build ollama HTTP request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("call ollama: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("ollama returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse the response.
	var ollamaResp ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&ollamaResp); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}

	// Parse the LLM's JSON output to extract the provider ID.
	var selection SelectionResponse
	if err := json.Unmarshal([]byte(ollamaResp.Response), &selection); err != nil {
		return "", fmt.Errorf("parse LLM selection JSON %q: %w", ollamaResp.Response, err)
	}

	if selection.ProviderID == "" {
		return "", fmt.Errorf("LLM returned empty providerId in response %q", ollamaResp.Response)
	}

	// Validate the returned ID exists in the input list.
	if _, ok := validIDs[selection.ProviderID]; !ok {
		return "", fmt.Errorf("LLM returned unknown providerId %q (valid: %v)",
			selection.ProviderID, validIDsList(validIDs))
	}

	return selection.ProviderID, nil
}

// validIDsList returns a sorted list of valid IDs for error messages.
func validIDsList(ids map[string]struct{}) []string {
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// DeterministicFallback selects a provider using a deterministic strategy when
// the AI is unavailable or returns an invalid choice. Priority:
//  1. Lowest cost (if priced)
//  2. Lowest carbon intensity (if advertised)
//  3. Most available chunks
//
// Returns ("", false) if no provider has available capacity.
func DeterministicFallback(nodeGroups []brokerapi.NodeGroupView) (string, bool) {
	type candidate struct {
		id        string
		cost      float64
		hasCost   bool
		carbon    float64
		hasCarbon bool
		available int32
	}

	var candidates []candidate
	for _, ng := range nodeGroups {
		avail := ng.MaxSize - ng.CurrentReserved
		if avail <= 0 {
			continue
		}
		c := candidate{
			id:        ng.ProviderClusterID,
			available: avail,
		}
		if ng.Cost != nil {
			c.cost = ng.Cost.AsApproximateFloat64()
			c.hasCost = true
		}
		if ng.CarbonIntensity != nil {
			c.carbon = *ng.CarbonIntensity
			c.hasCarbon = true
		}
		candidates = append(candidates, c)
	}

	if len(candidates) == 0 {
		return "", false
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		ci, cj := candidates[i], candidates[j]
		// 1. Prefer priced over unpriced, then lowest cost.
		if ci.hasCost != cj.hasCost {
			return ci.hasCost
		}
		if ci.hasCost && cj.hasCost && ci.cost != cj.cost {
			return ci.cost < cj.cost
		}
		// 2. Prefer carbon-advertised, then lowest carbon.
		if ci.hasCarbon != cj.hasCarbon {
			return ci.hasCarbon
		}
		if ci.hasCarbon && cj.hasCarbon && ci.carbon != cj.carbon {
			return ci.carbon < cj.carbon
		}
		// 3. Most available chunks.
		return ci.available > cj.available
	})

	return candidates[0].id, true
}
