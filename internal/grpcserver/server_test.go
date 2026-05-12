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
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	"github.com/netgroup-polito/federation-autoscaler/internal/grpcserver/protos"
)

// stageCertDir produces a directory holding tls.crt / tls.key / ca.crt
// usable both as the server's TLSConfig and as a client trust anchor.
// Returns the dir plus a client tls.Config wired to dial the server.
func stageCertDir(t *testing.T) (dir string, clientTLS *tls.Config) {
	t.Helper()
	dir = t.TempDir()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "grpc-server-test"},
		NotBefore:             time.Now().Add(-time.Minute),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	mustWrite := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite("tls.crt", certPEM)
	mustWrite("tls.key", keyPEM)
	mustWrite("ca.crt", certPEM) // self-signed: same cert serves as CA bundle

	clientCAs := x509.NewCertPool()
	clientCAs.AppendCertsFromPEM(certPEM)
	clientCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	clientTLS = &tls.Config{
		Certificates: []tls.Certificate{clientCert},
		RootCAs:      clientCAs,
		ServerName:   "127.0.0.1",
		MinVersion:   tls.VersionTLS13,
	}
	return dir, clientTLS
}

func TestNew_Validation(t *testing.T) {
	dir, _ := stageCertDir(t)

	cases := []struct {
		name string
		opts Options
		want string
	}{
		{"missing bind", Options{TLS: TLSConfig{CertDir: dir}}, "BindAddress is required"},
		{"missing certdir", Options{BindAddress: "127.0.0.1:0"}, "CertDir is required"},
		{"bad cert path", Options{
			BindAddress: "127.0.0.1:0",
			TLS:         TLSConfig{CertDir: "/nonexistent"},
		}, "load server keypair"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.opts)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestServer_RunStartsAndStops(t *testing.T) {
	dir, _ := stageCertDir(t)

	s, err := New(Options{
		BindAddress:     "127.0.0.1:0",
		TLS:             TLSConfig{CertDir: dir},
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	// Wait for the listener to bind.
	deadline := time.Now().Add(2 * time.Second)
	for s.Addr() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Addr() == nil {
		t.Fatal("server did not bind within 2s")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned error on shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit within 3s of ctx cancel")
	}
}

// TestServer_AllRPCsReturnUnimplemented dials the server with a real
// gRPC client and verifies every method on the CloudProvider service
// returns codes.Unimplemented. When 10c/10d/10e replace the stubs
// with real bodies, the corresponding sub-test will start failing —
// that is intentional: it forces the implementer to update the test
// in the same PR.
func TestServer_AllRPCsReturnUnimplemented(t *testing.T) {
	dir, clientTLS := stageCertDir(t)

	s, err := New(Options{
		BindAddress:     "127.0.0.1:0",
		TLS:             TLSConfig{CertDir: dir},
		ShutdownTimeout: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	go func() { runDone <- s.Run(ctx) }()

	// Wait for bind.
	deadline := time.Now().Add(2 * time.Second)
	for s.Addr() == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Addr() == nil {
		t.Fatal("server did not bind")
	}

	conn, err := grpc.NewClient(s.Addr().String(),
		grpc.WithTransportCredentials(credentials.NewTLS(clientTLS)))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	client := protos.NewCloudProviderClient(conn)

	// Only the optional NodeGroupGetOptions still stubs out — CA's
	// proto explicitly permits providers to leave it Unimplemented
	// when they have no per-group autoscaling-options overrides.
	calls := []struct {
		name string
		call func(context.Context) error
	}{
		{"NodeGroupGetOptions", func(ctx context.Context) error {
			_, err := client.NodeGroupGetOptions(ctx, &protos.NodeGroupAutoscalingOptionsRequest{})
			return err
		}},
	}

	for _, tc := range calls {
		t.Run(tc.name, func(t *testing.T) {
			callCtx, cancelCall := context.WithTimeout(ctx, 2*time.Second)
			defer cancelCall()
			err := tc.call(callCtx)
			if err == nil {
				t.Fatalf("expected Unimplemented, got nil error")
			}
			st, ok := status.FromError(err)
			if !ok {
				t.Fatalf("error is not a gRPC status: %v", err)
			}
			if st.Code() != codes.Unimplemented {
				t.Fatalf("expected Unimplemented, got %s: %s", st.Code(), st.Message())
			}
		})
	}

	// Tear down the server cleanly.
	cancel()
	select {
	case err := <-runDone:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not exit within 3s")
	}
}
