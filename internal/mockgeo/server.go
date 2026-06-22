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

// Package mockgeo is a tiny stand-in for a region→coordinates geo API used by
// the latency placement strategy demo. It is REGION-KEYED: GET /latlon?region=QC
// returns that region's lat/lon. It runs on the dedicated mock cluster; both
// agent roles fetch their own region's coordinates and advertise them (providers
// on the ClusterAdvertisement, the consumer on its heartbeat). The Broker then
// ranks providers by great-circle distance to the consumer.
package mockgeo

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
)

// coords is a region's geographic location in decimal degrees.
type coords struct {
	lat float64
	lon float64
}

// regionCoords maps region codes to representative city coordinates. Codes match
// the carbon service's regions so a single --region value drives both lookups.
var regionCoords = map[string]coords{
	"QC":  {45.6085, -73.5493},  // Montreal, Canada
	"LOM": {45.4642, 9.1900},    // Milan, Italy
	"CA":  {37.3382, -121.8863}, // San Jose, USA
	"HE":  {50.1109, 8.6821},    // Frankfurt, Germany
	"13":  {35.6895, 139.6917},  // Tokyo, Japan
	"NSW": {-33.8688, 151.2093}, // Sydney, Australia
	"IDF": {48.8566, 2.3522},    // Paris, France
	"SP":  {-23.5505, -46.6333}, // Sao Paulo, Brazil
	"SG":  {1.3521, 103.8198},   // Singapore
	"ENG": {51.5074, -0.1278},   // London, UK
}

// latLonResponse is the JSON shape consumed by the agent's geo client.
type latLonResponse struct {
	Region string  `json:"region"`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// Handler serves GET /latlon?region=XX with the region's coordinates.
func Handler(w http.ResponseWriter, r *http.Request) {
	region := r.URL.Query().Get("region")
	w.Header().Set("Content-Type", "application/json")

	if region == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing 'region' query parameter"})
		return
	}
	c, ok := regionCoords[region]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": fmt.Sprintf("unknown region: %s", region)})
		return
	}

	if err := json.NewEncoder(w).Encode(latLonResponse{Region: region, Lat: c.lat, Lon: c.lon}); err != nil {
		log.Printf("mock-geo: encode error: %v", err)
	}
}

// StartServer starts the mock-geo HTTP server on the given port and blocks.
func StartServer(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /latlon", Handler)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })

	addr := fmt.Sprintf(":%d", port)
	log.Printf("mock-geo listening on %s", addr)
	return http.ListenAndServe(addr, mux)
}
