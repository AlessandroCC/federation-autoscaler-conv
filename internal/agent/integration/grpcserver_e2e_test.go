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

package integration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	brokerv1alpha1 "github.com/netgroup-polito/federation-autoscaler/api/broker/v1alpha1"
	agentclient "github.com/netgroup-polito/federation-autoscaler/internal/agent/client"
	"github.com/netgroup-polito/federation-autoscaler/internal/agent/consumer/localapi"
	brokerapi "github.com/netgroup-polito/federation-autoscaler/internal/broker/api"
	"github.com/netgroup-polito/federation-autoscaler/internal/grpcserver"
	gAgentClient "github.com/netgroup-polito/federation-autoscaler/internal/grpcserver/agentclient"
	"github.com/netgroup-polito/federation-autoscaler/internal/grpcserver/protos"
)

// stageGrpcCertDir copies the suite's existing server keypair + CA
// bundle into a fresh tmp dir under the file names grpcserver.TLSConfig
// expects (tls.crt / tls.key / ca.crt). Reuses the bundle so the
// gRPC client dialled from this test can trust the server cert
// without minting an entirely new CA.
func stageGrpcCertDir() string {
	dir, err := os.MkdirTemp("", "grpc-cert-")
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() { _ = os.RemoveAll(dir) })

	copyFile := func(src, dstName string) {
		b, err := os.ReadFile(src)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(dir, dstName), b, 0o600)).To(Succeed())
	}
	copyFile(bundle.serverCertPath, "tls.crt")
	copyFile(bundle.serverKeyPath, "tls.key")
	copyFile(bundle.caPath, "ca.crt")
	return dir
}

// grpcClientTLS builds the *tls.Config the test's "fake CA" gRPC
// client uses to dial the gRPC server. The client cert is freshly
// issued from the bundle's CA so it chains correctly.
func grpcClientTLS(commonName string, serial int64) *tls.Config {
	clientCert, err := bundleBuilder_.issueAgentCert(commonName, serial)
	Expect(err).NotTo(HaveOccurred())

	caBytes, err := os.ReadFile(bundle.caPath)
	Expect(err).NotTo(HaveOccurred())
	rootCAs := x509.NewCertPool()
	rootCAs.AppendCertsFromPEM(caBytes)

	cert, err := tls.LoadX509KeyPair(clientCert.certPath, clientCert.keyPath)
	Expect(err).NotTo(HaveOccurred())

	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      rootCAs,
		ServerName:   "127.0.0.1",
		MinVersion:   tls.VersionTLS13,
	}
}

var _ = Describe("Step 10 end-to-end: gRPC server → localapi → broker over the full chain", func() {
	const (
		providerCluster = "provider-10f"
		consumerCluster = "consumer-10f"
	)
	cadvKey := types.NamespacedName{Name: providerCluster, Namespace: suiteNamespace}

	AfterEach(func() {
		_ = k8sClient.Delete(suiteCtx, &brokerv1alpha1.ClusterAdvertisement{
			ObjectMeta: metav1.ObjectMeta{Name: providerCluster, Namespace: suiteNamespace},
		})
	})

	It("exposes all 14 implemented RPCs end-to-end and returns the right shapes", func() {
		By("issuing a CN=consumer-10f cert for the consumer agent's broker client")
		consumerCert, err := bundleBuilder_.issueAgentCert(consumerCluster, 500)
		Expect(err).NotTo(HaveOccurred())

		By("seeding an Available ClusterAdvertisement so the broker has node groups to advertise")
		cadv := &brokerv1alpha1.ClusterAdvertisement{
			ObjectMeta: metav1.ObjectMeta{Name: providerCluster, Namespace: suiteNamespace},
			Spec: brokerv1alpha1.ClusterAdvertisementSpec{
				ClusterID:     providerCluster,
				LiqoClusterID: "liqo-" + providerCluster,
				ClusterType:   brokerv1alpha1.ChunkTypeStandard,
				Resources: brokerv1alpha1.AdvertisedResources{
					Allocatable: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("8"),
						corev1.ResourceMemory: resource.MustParse("16Gi"),
					},
				},
			},
		}
		Expect(k8sClient.Create(suiteCtx, cadv)).To(Succeed())
		Eventually(func() error {
			c := &brokerv1alpha1.ClusterAdvertisement{}
			if err := k8sClient.Get(suiteCtx, cadvKey, c); err != nil {
				return err
			}
			now := metav1.Now()
			c.Status.Available = true
			c.Status.LastSeen = &now
			c.Status.TotalChunks = 4
			c.Status.AvailableChunks = 4
			return k8sClient.Status().Update(suiteCtx, c)
		}, suiteTimeout, suiteInterval).Should(Succeed())

		By("building the broker client the consumer agent's localapi will proxy through")
		brokerClient, err := agentclient.New(agentclient.Options{
			BrokerURL: brokerURL(),
			TLS: agentclient.TLSConfig{
				CertFile:     consumerCert.certPath,
				KeyFile:      consumerCert.keyPath,
				BrokerCAFile: bundle.caPath,
				ServerName:   serverNameFromAddr(brokerListen),
			},
			RequestTimeout: 5 * time.Second,
		})
		Expect(err).NotTo(HaveOccurred())

		By("posting a heartbeat so the broker's ConsumerRegistry knows this consumer")
		Eventually(func() error {
			_, err := brokerClient.PostHeartbeat(suiteCtx, &brokerapi.HeartbeatRequest{
				ClusterID:     consumerCluster,
				LiqoClusterID: "liqo-" + consumerCluster,
			})
			return err
		}, suiteTimeout, suiteInterval).Should(Succeed())

		By("mounting the consumer agent's localapi.Handler on an httptest server")
		// Using httptest avoids picking a port + waiting-for-bind
		// boilerplate; the handler is the same exported type the
		// production cmd/agent uses (only the listener is different).
		localServer, err := localapi.New(localapi.Options{
			BindAddress: "127.0.0.1:0", // unused: we mount Handler() directly
			Client:      brokerClient,
			LocalClient: k8sClient,
		})
		Expect(err).NotTo(HaveOccurred())
		localTS := httptest.NewServer(localServer.Handler())
		defer localTS.Close()

		By("building the gRPC server's typed agentclient pointed at the localapi httptest URL")
		grpcAgent, err := gAgentClient.New(gAgentClient.Options{
			BaseURL: localTS.URL,
		})
		Expect(err).NotTo(HaveOccurred())

		By("starting the gRPC server with mTLS on a free 127.0.0.1 port")
		grpcCertDir := stageGrpcCertDir()
		grpcBind := pickListener()
		gs, err := grpcserver.New(grpcserver.Options{
			BindAddress: grpcBind,
			TLS: grpcserver.TLSConfig{
				CertDir:  grpcCertDir,
				CertName: "tls.crt",
				KeyName:  "tls.key",
				CAName:   "ca.crt",
			},
			AgentClient:     grpcAgent,
			ShutdownTimeout: time.Second,
		})
		Expect(err).NotTo(HaveOccurred())

		grpcCtx, grpcCancel := context.WithCancel(suiteCtx)
		grpcDone := make(chan error, 1)
		go func() { grpcDone <- gs.Run(grpcCtx) }()
		defer func() {
			grpcCancel()
			Eventually(grpcDone, 3*time.Second, 50*time.Millisecond).Should(Receive())
		}()
		Eventually(func() bool { return gs.Addr() != nil }, time.Second, 10*time.Millisecond).Should(BeTrue())

		By("dialling the gRPC server as Cluster Autoscaler would")
		conn, err := grpc.NewClient(gs.Addr().String(),
			grpc.WithTransportCredentials(credentials.NewTLS(grpcClientTLS("ca-10f", 600))))
		Expect(err).NotTo(HaveOccurred())
		defer func() { _ = conn.Close() }()
		client := protos.NewCloudProviderClient(conn)

		ctx, cancel := context.WithTimeout(suiteCtx, 10*time.Second)
		defer cancel()

		// -------------------------------------------------------------
		// Read-mostly RPCs (10c)
		// -------------------------------------------------------------

		By("NodeGroups: the advertised ClusterAdvertisement surfaces as a node group")
		var nodeGroupID string
		Eventually(func(g Gomega) {
			ng, err := client.NodeGroups(ctx, &protos.NodeGroupsRequest{})
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(ng.NodeGroups).NotTo(BeEmpty())
			// Capture the first group's id so subsequent RPCs target a
			// real broker-advertised value rather than a hard-coded
			// string.
			nodeGroupID = ng.NodeGroups[0].Id
			g.Expect(nodeGroupID).NotTo(BeEmpty())
		}, suiteTimeout, suiteInterval).Should(Succeed())

		By("NodeGroupForNode: unknown node yields empty NodeGroup (proto contract)")
		resForNode, err := client.NodeGroupForNode(ctx, &protos.NodeGroupForNodeRequest{
			Node: &protos.ExternalGrpcNode{Name: "no-such-virtual-node"},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resForNode.NodeGroup).NotTo(BeNil())
		Expect(resForNode.NodeGroup.Id).To(BeEmpty())

		By("NodeGroupTargetSize: zero — no VirtualNodeState CRs materialised yet")
		resSize, err := client.NodeGroupTargetSize(ctx, &protos.NodeGroupTargetSizeRequest{Id: nodeGroupID})
		Expect(err).NotTo(HaveOccurred())
		Expect(resSize.TargetSize).To(Equal(int32(0)))

		By("NodeGroupTemplateNodeInfo: returns a marshalled v1.Node with the chunk allocatable")
		resTpl, err := client.NodeGroupTemplateNodeInfo(ctx,
			&protos.NodeGroupTemplateNodeInfoRequest{Id: nodeGroupID})
		Expect(err).NotTo(HaveOccurred())
		Expect(resTpl.NodeBytes).NotTo(BeEmpty())
		var tpl corev1.Node
		Expect(tpl.Unmarshal(resTpl.NodeBytes)).To(Succeed())
		Expect(tpl.Status.Allocatable).NotTo(BeEmpty())

		// -------------------------------------------------------------
		// Mutating RPCs (10d)
		// -------------------------------------------------------------

		By("NodeGroupIncreaseSize: creates a Reservation CR on the broker")
		_, err = client.NodeGroupIncreaseSize(ctx, &protos.NodeGroupIncreaseSizeRequest{
			Id: nodeGroupID, Delta: 1,
		})
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() int {
			var resvs brokerv1alpha1.ReservationList
			_ = k8sClient.List(suiteCtx, &resvs)
			n := 0
			for i := range resvs.Items {
				if resvs.Items[i].Spec.ProviderClusterID == providerCluster {
					n++
				}
			}
			return n
		}, suiteTimeout, suiteInterval).Should(BeNumerically(">=", 1))

		By("NodeGroupDeleteNodes: unknown node → NotFound")
		_, err = client.NodeGroupDeleteNodes(ctx, &protos.NodeGroupDeleteNodesRequest{
			Id:    nodeGroupID,
			Nodes: []*protos.ExternalGrpcNode{{Name: "no-such-virtual-node"}},
		})
		Expect(status.Code(err)).To(Equal(codes.NotFound))

		By("NodeGroupDecreaseTargetSize: no-op success in v1")
		_, err = client.NodeGroupDecreaseTargetSize(ctx, &protos.NodeGroupDecreaseTargetSizeRequest{
			Id: nodeGroupID, Delta: -1,
		})
		Expect(err).NotTo(HaveOccurred())

		// -------------------------------------------------------------
		// Pricing + lifecycle RPCs (10e)
		// -------------------------------------------------------------

		By("PricingNodePrice: unknown node returns 0, not an error")
		resPrice, err := client.PricingNodePrice(ctx, &protos.PricingNodePriceRequest{
			Node:           &protos.ExternalGrpcNode{Name: "no-such-virtual-node"},
			StartTimestamp: timestamppb.Now(),
			EndTimestamp:   timestamppb.New(time.Now().Add(time.Hour)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resPrice.Price).To(Equal(float64(0)))

		By("PricingPodPrice: v1 returns 0 for any pod")
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "any"}}
		podBytes, err := pod.Marshal()
		Expect(err).NotTo(HaveOccurred())
		resPodPrice, err := client.PricingPodPrice(ctx, &protos.PricingPodPriceRequest{
			PodBytes:       podBytes,
			StartTimestamp: timestamppb.Now(),
			EndTimestamp:   timestamppb.New(time.Now().Add(time.Hour)),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resPodPrice.Price).To(Equal(float64(0)))

		By("GPULabel: NVIDIA convention")
		resGPULabel, err := client.GPULabel(ctx, &protos.GPULabelRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resGPULabel.Label).To(Equal("nvidia.com/gpu"))

		By("GetAvailableGPUTypes: empty (no GPU groups advertised)")
		resGPUTypes, err := client.GetAvailableGPUTypes(ctx, &protos.GetAvailableGPUTypesRequest{})
		Expect(err).NotTo(HaveOccurred())
		Expect(resGPUTypes.GpuTypes).To(BeEmpty())

		By("Refresh: no-op success")
		_, err = client.Refresh(ctx, &protos.RefreshRequest{})
		Expect(err).NotTo(HaveOccurred())

		By("Cleanup: no-op success")
		_, err = client.Cleanup(ctx, &protos.CleanupRequest{})
		Expect(err).NotTo(HaveOccurred())

		By("NodeGroupNodes: empty (no VirtualNodeState CRs in pre-step-11 state)")
		resNodes, err := client.NodeGroupNodes(ctx, &protos.NodeGroupNodesRequest{Id: nodeGroupID})
		Expect(err).NotTo(HaveOccurred())
		Expect(resNodes.Instances).To(BeEmpty())

		By("NodeGroupGetOptions: still Unimplemented by design — the proto permits it")
		_, err = client.NodeGroupGetOptions(ctx, &protos.NodeGroupAutoscalingOptionsRequest{})
		Expect(status.Code(err)).To(Equal(codes.Unimplemented))
	})
})
