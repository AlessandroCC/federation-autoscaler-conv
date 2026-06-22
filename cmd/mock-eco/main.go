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

// Command mock-eco is a standalone HTTP stand-in for a grid carbon-intensity
// API, used by the eco placement strategy demo. It serves
// GET /carbon?region=<code> with that region's current-hour carbon intensity.
// Deployed on the dedicated mock cluster; provider agents call it via
// --mock-eco-url.
package main

import (
	"flag"
	"log"

	"github.com/netgroup-polito/federation-autoscaler/internal/mockeco"
)

func main() {
	port := flag.Int("port", 8081, "TCP port to listen on")
	flag.Parse()

	if err := mockeco.StartServer(*port); err != nil {
		log.Fatalf("mock-eco server failed: %v", err)
	}
}
