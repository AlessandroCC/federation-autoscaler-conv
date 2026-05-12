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

package grpcserver

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
)

// TLSConfig is the file-backed configuration the gRPC server uses to
// terminate Cluster Autoscaler's mTLS connection. Mirrors the broker's
// TLSConfig posture: client-cert-required, TLS 1.3 floor, no fallback
// to plaintext.
type TLSConfig struct {
	// CertDir is the directory holding the three PEM files below. The
	// cmd/grpc-server binary surfaces it via --grpc-cert-path; tests
	// fill it from a t.TempDir-staged pair.
	CertDir string

	// CertName / KeyName / CAName are the file names under CertDir.
	// Defaults match the cmd-side flag defaults.
	CertName string
	KeyName  string
	CAName   string

	// MinVersion overrides the floor for negotiated TLS versions.
	// Defaults to TLS 1.3 to match the rest of the project.
	MinVersion uint16
}

// Validate enforces the file-path contract. Existence on disk is left
// for Build to surface so helpful "no such file" errors point at the
// actual offending path.
func (c TLSConfig) Validate() error {
	if c.CertDir == "" {
		return fmt.Errorf("grpcserver: TLS.CertDir is required")
	}
	return nil
}

// Build loads the server keypair and the CA bundle and returns a
// *tls.Config wired for mTLS: client cert is required and verified
// against the same CA that signed Cluster Autoscaler's own keypair.
func (c TLSConfig) Build() (*tls.Config, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	certName := orDefault(c.CertName, "tls.crt")
	keyName := orDefault(c.KeyName, "tls.key")
	caName := orDefault(c.CAName, "ca.crt")

	serverCert, err := tls.LoadX509KeyPair(
		filepath.Join(c.CertDir, certName),
		filepath.Join(c.CertDir, keyName),
	)
	if err != nil {
		return nil, fmt.Errorf("grpcserver: load server keypair: %w", err)
	}

	caBytes, err := os.ReadFile(filepath.Join(c.CertDir, caName))
	if err != nil {
		return nil, fmt.Errorf("grpcserver: read ca bundle: %w", err)
	}
	clientCAs := x509.NewCertPool()
	if !clientCAs.AppendCertsFromPEM(caBytes) {
		return nil, fmt.Errorf("grpcserver: ca bundle %q has no PEM certs",
			filepath.Join(c.CertDir, caName))
	}

	min := c.MinVersion
	if min == 0 {
		min = tls.VersionTLS13
	}
	return &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAs,
		MinVersion:   min,
	}, nil
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
