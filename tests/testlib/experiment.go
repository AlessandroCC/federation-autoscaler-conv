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

package testlib

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// regionPool lists the 50 world regions available for auto-generation.
// Each must have a corresponding entry in regionLocation (deploy.go).
var regionPool = []string{
	"QC", "CA", "NSW", "IDF", "ENG", "13", "HE", "SP",
	"MH", "AB", "LOM", "SG", "VA", "TX", "OR", "IE",
	"NL", "KR", "ZA", "AE",
	"IL", "OH", "GA", "BC", "BY", "RM", "CT", "AN",
	"VIC", "NCL", "HK", "TW", "PH", "CL", "CO", "MX",
	"FI", "NO", "DK", "PL", "CZ", "AT", "CH", "BE",
	"PT", "GR", "RO", "IL2", "EG", "NG",
}

func autoGenerateRegions(n int) []string {
	pool := make([]string, len(regionPool))
	copy(pool, regionPool)
	rand.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	regions := make([]string, n)
	for i := range regions {
		regions[i] = pool[i%len(pool)]
	}
	return regions
}

func autoGenerateTCDelays(n int) []TCDelayAutoConfig {
	delays := make([]TCDelayAutoConfig, n)
	for i := range delays {
		delays[i] = TCDelayAutoConfig{
			ProviderIndex: i + 1,
			DelayMs:       1 + rand.Intn(300),
			Interface:     "eth0",
		}
	}
	return delays
}

func autoGenerateConsumerDelays(consumers, providers int) []ConsumerDelayConfig {
	configs := make([]ConsumerDelayConfig, consumers)
	for c := range configs {
		pds := make([]ProviderDelay, providers)
		for p := range pds {
			pds[p] = ProviderDelay{
				ProviderIndex: p + 1,
				DelayMs:       5 + rand.Intn(100),
			}
		}
		configs[c] = ConsumerDelayConfig{
			ConsumerIndex:  c + 1,
			ProviderDelays: pds,
		}
	}
	return configs
}

// AutoConfig is the fully automated YAML schema. The user specifies counts,
// regions, and experiment parameters; everything else is computed at runtime.
type AutoConfig struct {
	Consumers       int             `yaml:"consumers"`
	Providers       int             `yaml:"providers"`
	ProviderRegions []string        `yaml:"providerRegions"`
	Experiment      TestParams      `yaml:"experiment"`
	Infra           InfraConfig     `yaml:"infra"`
	Cleanup         *bool           `yaml:"cleanup,omitempty"`
	Output          OutputConfig    `yaml:"output"`
}

// InfraConfig controls how the orchestrator builds infrastructure.
type InfraConfig struct {
	Registry         string        `yaml:"registry"`
	ReadinessTimeout time.Duration `yaml:"readinessTimeout"`
	LiqoProvider     string        `yaml:"liqoProvider"`
}

// ExperimentConfig is the single YAML file that describes the full
// topology and test parameters. Both comparative-eco and comparative-latency
// read it.
type ExperimentConfig struct {
	Broker    BrokerConfig     `yaml:"broker"`
	Consumers []ConsumerConfig `yaml:"consumers"`
	Providers []ProviderConfig `yaml:"providers"`
	Certs     CertsConfig      `yaml:"certs"`
	MockEco   *MockEcoConfig   `yaml:"mockEco,omitempty"`
	Experiment TestParams      `yaml:"experiment"`
	Output    OutputConfig     `yaml:"output"`
}

// BrokerConfig identifies the Broker endpoint.
type BrokerConfig struct {
	URL        string `yaml:"url"`
	ServerName string `yaml:"serverName"`
}

// ConsumerConfig identifies one consumer cluster.
type ConsumerConfig struct {
	ID         string `yaml:"id"`
	ConsoleURL string `yaml:"consoleURL"`
}

// ProviderConfig identifies one provider cluster and its region.
type ProviderConfig struct {
	ID     string `yaml:"id"`
	Region string `yaml:"region"`
}

// CertsConfig points to the mTLS certificates.
type CertsConfig struct {
	Dir    string `yaml:"dir"`
	Prefix string `yaml:"prefix"`
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
	CAFile   string `yaml:"caFile"`
}

// MockEcoConfig identifies the controllable mock-eco service.
type MockEcoConfig struct {
	URL string `yaml:"url"`
}

// TestParams are the knobs for the experiment.
type TestParams struct {
	Mode                  string        `yaml:"mode"`
	Iterations            int           `yaml:"iterations"`
	PhasePause            time.Duration `yaml:"phasePause"`
	PolicyPropagationWait time.Duration `yaml:"policyPropagationWait"`
	AdvertisementLag      time.Duration `yaml:"advertisementLag"`
	WarmupTimeout         time.Duration `yaml:"warmupTimeout"`
	ReservationPoll       time.Duration `yaml:"reservationPoll"`
	ReservationTimeout    time.Duration `yaml:"reservationTimeout"`

	// Eco-specific.
	CarbonLow              int           `yaml:"carbonLow"`
	CarbonHigh             int           `yaml:"carbonHigh"`
	CarbonGreenFractionMin float64       `yaml:"carbonGreenFractionMin"`
	CarbonGreenFractionMax float64       `yaml:"carbonGreenFractionMax"`
	CarbonRefreshInterval  time.Duration `yaml:"carbonRefreshInterval"`

	// Latency-specific (automated Kind mode).
	TCDelaysAuto           []TCDelayAutoConfig   `yaml:"tcDelays,omitempty"`
	ConsumerDelays         []ConsumerDelayConfig `yaml:"consumerDelays,omitempty"`
	SSHKey                 string                `yaml:"sshKey,omitempty"`
	LatencyRefreshInterval time.Duration         `yaml:"latencyRefreshInterval"`
	LatencyJitterMs        int                   `yaml:"latencyJitterMs"`
}

// TCDelayAutoConfig is the automated tc delay config (uses providerIndex).
type TCDelayAutoConfig struct {
	ProviderIndex int    `yaml:"providerIndex"` // 1-based index
	DelayMs       int    `yaml:"delayMs"`
	Interface     string `yaml:"interface,omitempty"` // default "eth0"
}

// ConsumerDelayConfig specifies per-provider delays for one consumer.
// When present, delays are applied on consumer containers (lato consumer)
// instead of on provider containers, enabling per-consumer latency simulation.
type ConsumerDelayConfig struct {
	ConsumerIndex  int             `yaml:"consumerIndex"`
	ProviderDelays []ProviderDelay `yaml:"providerDelays"`
}

// ProviderDelay is one entry in the consumer delay matrix.
type ProviderDelay struct {
	ProviderIndex int `yaml:"providerIndex"`
	DelayMs       int `yaml:"delayMs"`
}

// TCDelayConfig is one per-provider netem delay entry (legacy SSH mode).
type TCDelayConfig struct {
	Host      string `yaml:"host"`
	Interface string `yaml:"interface"`
	DelayMs   int    `yaml:"delayMs"`
}

// OutputConfig controls where results go.
type OutputConfig struct {
	Dir string `yaml:"dir"`
}

// LoadAutoConfig reads the automated YAML config file.
func LoadAutoConfig(path string) (*AutoConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg AutoConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	cfg.applyDefaults()
	return &cfg, cfg.Validate()
}

func (c *AutoConfig) applyDefaults() {
	if c.Consumers <= 0 {
		c.Consumers = 1
	}
	if c.Providers <= 0 {
		c.Providers = 2
	}
	if c.Experiment.Mode == "" {
		c.Experiment.Mode = "observe"
	}
	if c.Experiment.Iterations <= 0 {
		c.Experiment.Iterations = 10
	}
	if c.Experiment.PhasePause <= 0 {
		c.Experiment.PhasePause = 30 * time.Second
	}
	if c.Experiment.PolicyPropagationWait <= 0 {
		c.Experiment.PolicyPropagationWait = 20 * time.Second
	}
	if c.Experiment.AdvertisementLag <= 0 {
		c.Experiment.AdvertisementLag = 35 * time.Second
	}
	if c.Experiment.WarmupTimeout <= 0 {
		c.Experiment.WarmupTimeout = 5 * time.Minute
	}
	if c.Experiment.ReservationPoll <= 0 {
		c.Experiment.ReservationPoll = 5 * time.Second
	}
	if c.Experiment.ReservationTimeout <= 0 {
		c.Experiment.ReservationTimeout = 10 * time.Minute
	}
	if c.Experiment.CarbonLow <= 0 {
		c.Experiment.CarbonLow = 50
	}
	if c.Experiment.CarbonHigh <= 0 {
		c.Experiment.CarbonHigh = 800
	}
	if c.Experiment.CarbonGreenFractionMin <= 0 {
		c.Experiment.CarbonGreenFractionMin = 0.3
	}
	if c.Experiment.CarbonGreenFractionMax <= 0 {
		c.Experiment.CarbonGreenFractionMax = 0.7
	}
	if c.Experiment.CarbonRefreshInterval <= 0 {
		c.Experiment.CarbonRefreshInterval = 3 * time.Minute
	}
	if c.Experiment.LatencyRefreshInterval <= 0 {
		c.Experiment.LatencyRefreshInterval = 3 * time.Minute
	}
	if c.Experiment.LatencyJitterMs <= 0 {
		c.Experiment.LatencyJitterMs = 20
	}
	if c.Output.Dir == "" {
		c.Output.Dir = "results"
	}
	if c.Infra.ReadinessTimeout <= 0 {
		c.Infra.ReadinessTimeout = 10 * time.Minute
	}
	if c.Infra.LiqoProvider == "" {
		c.Infra.LiqoProvider = "kind"
	}
	if c.Cleanup == nil {
		t := true
		c.Cleanup = &t
	}
	if len(c.ProviderRegions) == 0 {
		c.ProviderRegions = autoGenerateRegions(c.Providers)
		log.Printf("[config] auto-generated regions: %v", c.ProviderRegions)
	}
	if len(c.Experiment.ConsumerDelays) == 0 && len(c.Experiment.TCDelaysAuto) == 0 {
		c.Experiment.ConsumerDelays = autoGenerateConsumerDelays(c.Consumers, c.Providers)
		for _, cd := range c.Experiment.ConsumerDelays {
			for _, pd := range cd.ProviderDelays {
				log.Printf("[config] auto-generated consumer delay: consumer-%d → provider-%d: %dms",
					cd.ConsumerIndex, pd.ProviderIndex, pd.DelayMs)
			}
		}
	}
	for i := range c.Experiment.TCDelaysAuto {
		if c.Experiment.TCDelaysAuto[i].Interface == "" {
			c.Experiment.TCDelaysAuto[i].Interface = "eth0"
		}
	}
}

// Validate checks the automated config for completeness.
func (c *AutoConfig) Validate() error {
	if c.Consumers < 1 {
		return fmt.Errorf("consumers must be >= 1")
	}
	if c.Providers < 2 {
		return fmt.Errorf("providers must be >= 2")
	}
	if len(c.ProviderRegions) > 0 && len(c.ProviderRegions) != c.Providers {
		return fmt.Errorf("providerRegions length (%d) must match providers (%d)", len(c.ProviderRegions), c.Providers)
	}
	if c.Experiment.Mode != "observe" && c.Experiment.Mode != "reserve" {
		return fmt.Errorf("experiment.mode must be observe or reserve (got %q)", c.Experiment.Mode)
	}
	for _, td := range c.Experiment.TCDelaysAuto {
		if td.ProviderIndex < 1 || td.ProviderIndex > c.Providers {
			return fmt.Errorf("tcDelays.providerIndex %d out of range [1, %d]", td.ProviderIndex, c.Providers)
		}
	}
	for _, cd := range c.Experiment.ConsumerDelays {
		if cd.ConsumerIndex < 1 || cd.ConsumerIndex > c.Consumers {
			return fmt.Errorf("consumerDelays.consumerIndex %d out of range [1, %d]", cd.ConsumerIndex, c.Consumers)
		}
		for _, pd := range cd.ProviderDelays {
			if pd.ProviderIndex < 1 || pd.ProviderIndex > c.Providers {
				return fmt.Errorf("consumerDelays[consumer-%d].providerIndex %d out of range [1, %d]",
					cd.ConsumerIndex, pd.ProviderIndex, c.Providers)
			}
		}
	}
	return nil
}

// ShouldCleanup returns whether cleanup should run.
func (c *AutoConfig) ShouldCleanup() bool {
	return c.Cleanup == nil || *c.Cleanup
}

// Orchestrator manages the full lifecycle of a comparative test run.
type Orchestrator struct {
	Config      *AutoConfig
	TestType    string // "comparative-eco" or "comparative-latency"
	RunID       string
	RepoRoot    string
	Specs       []ClusterSpec
	KubeconfigDir string
	CADir       string
	Clients     *ExperimentClients
	OutputDir   string
	KeepClusters bool
	SkipBuild    bool
}

// NewOrchestrator creates an orchestrator for the given config.
func NewOrchestrator(cfg *AutoConfig, testType string, keepClusters, skipBuild bool, runID string) (*Orchestrator, error) {
	repoRoot, err := RepoRoot()
	if err != nil {
		return nil, fmt.Errorf("find repo root: %w", err)
	}
	if runID == "" {
		runID = RunID()
	}
	return &Orchestrator{
		Config:       cfg,
		TestType:     testType,
		RunID:        runID,
		RepoRoot:     repoRoot,
		KeepClusters: keepClusters,
		SkipBuild:    skipBuild,
	}, nil
}

// Setup creates clusters, builds images, deploys components, and waits for readiness.
func (o *Orchestrator) Setup(ctx context.Context) error {
	log.Println("=== PREREQUISITES ===")
	if err := CheckPrerequisites(); err != nil {
		return err
	}

	// Create temp dirs for PKI and kubeconfigs.
	var err error
	o.KubeconfigDir, err = os.MkdirTemp("", o.RunID+"-kubeconfigs-")
	if err != nil {
		return fmt.Errorf("create kubeconfig dir: %w", err)
	}
	o.CADir, err = os.MkdirTemp("", o.RunID+"-pki-")
	if err != nil {
		return fmt.Errorf("create CA dir: %w", err)
	}

	imgPrefix := "federation-autoscaler"
	imgTag := "latest"
	registry := DefaultStandaloneRegistry

	// Build Docker images.
	if !o.SkipBuild {
		log.Println("=== BUILD IMAGES ===")
		if err := DockerBuild(ctx, o.RepoRoot); err != nil {
			return fmt.Errorf("docker build: %w", err)
		}
	}

	// Retag images to match standalone scripts' naming convention.
	log.Println("=== RETAG IMAGES ===")
	if err := RetagForStandalone(ctx, imgPrefix, imgTag, registry); err != nil {
		return fmt.Errorf("retag: %w", err)
	}

	// Generate cluster specs.
	log.Println("=== CREATE CLUSTERS ===")
	o.Specs = GenerateClusterSpecs(o.RunID, o.Config.Consumers, o.Config.Providers)
	for i := range o.Specs {
		if err := KindCreateCluster(ctx, &o.Specs[i], o.KubeconfigDir); err != nil {
			return fmt.Errorf("create cluster %s: %w", o.Specs[i].Name, err)
		}
	}

	// Load images into clusters (using standalone naming).
	log.Println("=== LOAD IMAGES ===")
	centralImgs := CentralImages(registry, imgTag)
	agentImgs := AgentImages(registry, imgTag)
	consumerExtra := ConsumerExtraImages(registry, imgTag)

	if err := KindLoadImages(ctx, o.Specs[0].Name, centralImgs); err != nil {
		return fmt.Errorf("load images into central: %w", err)
	}
	for i := 0; i < o.Config.Consumers; i++ {
		imgs := append([]string{}, agentImgs...)
		imgs = append(imgs, consumerExtra...)
		if err := KindLoadImages(ctx, o.Specs[1+i].Name, imgs); err != nil {
			return fmt.Errorf("load images into consumer-%d: %w", i+1, err)
		}
	}
	for i := 0; i < o.Config.Providers; i++ {
		if err := KindLoadImages(ctx, o.Specs[1+o.Config.Consumers+i].Name, agentImgs); err != nil {
			return fmt.Errorf("load images into provider-%d: %w", i+1, err)
		}
	}

	// Deploy all components (don't pass --registry/--tag — use standalone defaults).
	log.Println("=== DEPLOY COMPONENTS ===")
	deployOpts := DeployOpts{
		RepoRoot:        o.RepoRoot,
		RunID:           o.RunID,
		Specs:           o.Specs,
		KubeconfigDir:   o.KubeconfigDir,
		CADir:           o.CADir,
		NumConsumers:    o.Config.Consumers,
		NumProviders:    o.Config.Providers,
		ProviderRegions: o.Config.ProviderRegions,
		ImgPrefix:       registry,
		ImgTag:          imgTag,
		LiqoProvider:    o.Config.Infra.LiqoProvider,
		EcoCacheTTL:     o.Config.Experiment.CarbonRefreshInterval,
	}
	if err := DeployAll(ctx, deployOpts); err != nil {
		return fmt.Errorf("deploy: %w", err)
	}

	// Scale down cluster autoscaler on each consumer.
	for i := 0; i < o.Config.Consumers; i++ {
		spec := o.Specs[1+i]
		log.Printf("[deploy] scaling cluster-autoscaler to 0 on consumer-%d", i+1)
		if err := ScaleClusterAutoscaler(ctx, spec.Kubeconfig, 0); err != nil {
			log.Printf("[deploy] warning: could not scale CA on consumer-%d: %v", i+1, err)
		}
	}

	// Resolve identities from all consumer join bundles.
	log.Println("=== WAIT FOR READINESS ===")
	identities, caFile, err := o.resolveIdentities()
	if err != nil {
		return fmt.Errorf("resolve identities: %w", err)
	}

	centralIP, err := ContainerIP(ctx, o.Specs[0].Name+"-control-plane")
	if err != nil {
		return fmt.Errorf("get central IP: %w", err)
	}
	brokerURL := fmt.Sprintf("https://%s:30443", centralIP)
	serverName := "broker.federation-autoscaler-system.svc"

	var consoleURLs []string
	for i := 0; i < o.Config.Consumers; i++ {
		consIP, err := ContainerIP(ctx, o.Specs[1+i].Name+"-control-plane")
		if err != nil {
			return fmt.Errorf("get consumer-%d IP: %w", i+1, err)
		}
		consoleURLs = append(consoleURLs, fmt.Sprintf("http://%s:30445", consIP))
	}

	var expectedProviders []string
	for i := 1; i <= o.Config.Providers; i++ {
		expectedProviders = append(expectedProviders, fmt.Sprintf("provider-%d", i))
	}

	o.Clients, err = WaitForReadiness(ctx, brokerURL, serverName, consoleURLs, expectedProviders, identities, caFile, o.Config.Infra.ReadinessTimeout)
	if err != nil {
		return fmt.Errorf("readiness: %w", err)
	}

	// Create output dir.
	o.OutputDir, err = EnsureOutputDir(o.Config.Output.Dir, o.TestType)
	if err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	log.Printf("[setup] run=%s output=%s", o.RunID, o.OutputDir)
	return nil
}

func (o *Orchestrator) resolveIdentities() (map[string]Identity, string, error) {
	bundleDir := filepath.Join(o.CADir, "bundles")
	identities := make(map[string]Identity, o.Config.Consumers)
	var caFile string

	for i := 1; i <= o.Config.Consumers; i++ {
		cid := fmt.Sprintf("consumer-%d", i)
		bundlePath := filepath.Join(bundleDir, cid+"-bundle.tgz")

		extractDir, err := os.MkdirTemp("", fmt.Sprintf("%s-%s-extract-", o.RunID, cid))
		if err != nil {
			return nil, "", err
		}

		cmd := exec.Command("tar", "-xzf", bundlePath, "-C", extractDir)
		if err := cmd.Run(); err != nil {
			os.RemoveAll(extractDir)
			return nil, "", fmt.Errorf("extract bundle for %s: %w", cid, err)
		}

		certFile := filepath.Join(extractDir, "client.crt")
		keyFile := filepath.Join(extractDir, "client.key")

		id, err := ResolveIdentityFromFiles(certFile, keyFile, filepath.Join(extractDir, "ca.crt"))
		if err != nil {
			os.RemoveAll(extractDir)
			return nil, "", fmt.Errorf("resolve identity for %s: %w", cid, err)
		}
		identities[cid] = id

		if caFile == "" {
			caFile = filepath.Join(extractDir, "ca.crt")
		}
	}

	return identities, caFile, nil
}

// Teardown deletes all Kind clusters created by this run.
func (o *Orchestrator) Teardown(ctx context.Context) {
	if o.KeepClusters {
		log.Printf("[cleanup] --keep-clusters: skipping cleanup. Delete manually:")
		for _, s := range o.Specs {
			log.Printf("  kind delete cluster --name %s", s.Name)
		}
		return
	}

	log.Println("=== CLEANUP ===")
	for _, s := range o.Specs {
		if err := KindDeleteCluster(ctx, s.Name); err != nil {
			log.Printf("[cleanup] warning: failed to delete %s: %v", s.Name, err)
		}
	}

	if o.KubeconfigDir != "" {
		os.RemoveAll(o.KubeconfigDir)
	}
	if o.CADir != "" {
		os.RemoveAll(o.CADir)
	}
	log.Println("[cleanup] done")
}

// BrokerURL returns the broker URL computed from the central cluster.
func (o *Orchestrator) BrokerURL(ctx context.Context) (string, error) {
	centralIP, err := ContainerIP(ctx, o.Specs[0].Name+"-control-plane")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("https://%s:30443", centralIP), nil
}

// ConsoleURL returns the console URL for a consumer by 0-based index.
func (o *Orchestrator) ConsoleURL(ctx context.Context, consumerIdx int) (string, error) {
	consIP, err := ContainerIP(ctx, o.Specs[1+consumerIdx].Name+"-control-plane")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:30445", consIP), nil
}

// MockEcoURL returns the mock-eco URL on the central cluster.
func (o *Orchestrator) MockEcoURL(ctx context.Context) (string, error) {
	centralIP, err := ContainerIP(ctx, o.Specs[0].Name+"-control-plane")
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("http://%s:30081", centralIP), nil
}

// ConsumerContainerName returns the Docker container name for a consumer by 1-based index.
func (o *Orchestrator) ConsumerContainerName(consumerIdx int) string {
	return o.Specs[1+consumerIdx-1].Name + "-control-plane"
}

// ProviderContainerName returns the Docker container name for a provider by 1-based index.
// For clusters with a worker node the agent (and its probe endpoint) lives on
// the worker, so tc delays must target that container.
func (o *Orchestrator) ProviderContainerName(providerIdx int) string {
	spec := o.Specs[1+o.Config.Consumers+providerIdx-1]
	if spec.HasWorker {
		return spec.Name + "-worker"
	}
	return spec.Name + "-control-plane"
}

// BuildExperimentConfig converts the AutoConfig into the runtime ExperimentConfig
// used by the experiment logic (for backward compatibility with existing code).
func (o *Orchestrator) BuildExperimentConfig(ctx context.Context) (*ExperimentConfig, error) {
	brokerURL, err := o.BrokerURL(ctx)
	if err != nil {
		return nil, err
	}

	var consumers []ConsumerConfig
	for i := 0; i < o.Config.Consumers; i++ {
		cURL, err := o.ConsoleURL(ctx, i)
		if err != nil {
			return nil, err
		}
		consumers = append(consumers, ConsumerConfig{
			ID:         fmt.Sprintf("consumer-%d", i+1),
			ConsoleURL: cURL,
		})
	}

	var providers []ProviderConfig
	for i := 0; i < o.Config.Providers; i++ {
		region := ""
		if i < len(o.Config.ProviderRegions) {
			region = o.Config.ProviderRegions[i]
		}
		providers = append(providers, ProviderConfig{
			ID:     fmt.Sprintf("provider-%d", i+1),
			Region: region,
		})
	}

	var mockEco *MockEcoConfig
	meURL, err := o.MockEcoURL(ctx)
	if err == nil {
		mockEco = &MockEcoConfig{URL: meURL}
	}

	return &ExperimentConfig{
		Broker: BrokerConfig{
			URL:        brokerURL,
			ServerName: "broker.federation-autoscaler-system.svc",
		},
		Consumers:  consumers,
		Providers:  providers,
		MockEco:    mockEco,
		Experiment: o.Config.Experiment,
		Output:     o.Config.Output,
	}, nil
}

// --- Legacy functions kept for backward compatibility ---

// LoadExperimentConfig reads and validates a YAML config file (legacy format).
func LoadExperimentConfig(path string) (*ExperimentConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg ExperimentConfig
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config %q: %w", path, err)
	}
	cfg.applyDefaults()
	return &cfg, cfg.ValidateLegacy()
}

func (c *ExperimentConfig) applyDefaults() {
	if c.Certs.Prefix == "" {
		c.Certs.Prefix = "scaltest"
	}
	if c.Experiment.Mode == "" {
		c.Experiment.Mode = "observe"
	}
	if c.Experiment.Iterations <= 0 {
		c.Experiment.Iterations = 10
	}
	if c.Experiment.PhasePause <= 0 {
		c.Experiment.PhasePause = 30 * time.Second
	}
	if c.Experiment.PolicyPropagationWait <= 0 {
		c.Experiment.PolicyPropagationWait = 20 * time.Second
	}
	if c.Experiment.AdvertisementLag <= 0 {
		c.Experiment.AdvertisementLag = 35 * time.Second
	}
	if c.Experiment.WarmupTimeout <= 0 {
		c.Experiment.WarmupTimeout = 5 * time.Minute
	}
	if c.Experiment.ReservationPoll <= 0 {
		c.Experiment.ReservationPoll = 5 * time.Second
	}
	if c.Experiment.ReservationTimeout <= 0 {
		c.Experiment.ReservationTimeout = 10 * time.Minute
	}
	if c.Experiment.CarbonLow <= 0 {
		c.Experiment.CarbonLow = 50
	}
	if c.Experiment.CarbonHigh <= 0 {
		c.Experiment.CarbonHigh = 800
	}
	if c.Experiment.CarbonGreenFractionMin <= 0 {
		c.Experiment.CarbonGreenFractionMin = 0.3
	}
	if c.Experiment.CarbonGreenFractionMax <= 0 {
		c.Experiment.CarbonGreenFractionMax = 0.7
	}
	if c.Experiment.CarbonRefreshInterval <= 0 {
		c.Experiment.CarbonRefreshInterval = 3 * time.Minute
	}
	if c.Experiment.LatencyRefreshInterval <= 0 {
		c.Experiment.LatencyRefreshInterval = 3 * time.Minute
	}
	if c.Experiment.LatencyJitterMs <= 0 {
		c.Experiment.LatencyJitterMs = 20
	}
	if c.Output.Dir == "" {
		c.Output.Dir = "results"
	}
}

// ValidateLegacy checks the legacy config for completeness.
func (c *ExperimentConfig) ValidateLegacy() error {
	if c.Broker.URL == "" {
		return fmt.Errorf("broker.url is required")
	}
	if len(c.Consumers) == 0 {
		return fmt.Errorf("at least one consumer is required")
	}
	for i, cons := range c.Consumers {
		if cons.ID == "" {
			return fmt.Errorf("consumers[%d].id is required", i)
		}
		if cons.ConsoleURL == "" {
			return fmt.Errorf("consumers[%d].consoleURL is required", i)
		}
	}
	if len(c.Providers) == 0 {
		return fmt.Errorf("at least one provider is required")
	}
	for i, prov := range c.Providers {
		if prov.ID == "" {
			return fmt.Errorf("providers[%d].id is required", i)
		}
	}
	if c.Certs.Dir == "" && (c.Certs.CertFile == "" || c.Certs.KeyFile == "" || c.Certs.CAFile == "") {
		return fmt.Errorf("certs: either dir or certFile+keyFile+caFile is required")
	}
	if c.Experiment.Mode != "observe" && c.Experiment.Mode != "reserve" {
		return fmt.Errorf("experiment.mode must be observe or reserve (got %q)", c.Experiment.Mode)
	}
	return nil
}

// BuildClients builds all the clients needed for the experiment and validates
// that every component is reachable.
func (c *ExperimentConfig) BuildClients(ctx context.Context) (*ExperimentClients, error) {
	var id Identity
	var caFile string
	var err error
	if c.Certs.Dir != "" {
		id, caFile, err = ResolveConsumerIdentity(c.Certs.Dir, c.Certs.Prefix)
	} else {
		id, err = ResolveIdentityFromFiles(c.Certs.CertFile, c.Certs.KeyFile, c.Certs.CAFile)
		caFile = c.Certs.CAFile
	}
	if err != nil {
		return nil, fmt.Errorf("resolve identity: %w", err)
	}

	certFP, _ := id.CertFingerprint()

	broker, err := NewBrokerClientFromIdentity(c.Broker.URL, c.Broker.ServerName, id, caFile)
	if err != nil {
		return nil, fmt.Errorf("build broker client: %w", err)
	}

	log.Printf("[setup] checking broker at %s...", c.Broker.URL)
	if err := broker.CheckReachable(ctx); err != nil {
		return nil, fmt.Errorf("broker not reachable at %s: %w", c.Broker.URL, err)
	}
	log.Println("[setup] broker: OK")

	ngResp, err := broker.GetNodeGroups(ctx)
	if err != nil {
		return nil, fmt.Errorf("get nodegroups: %w", err)
	}
	advertisingIDs := UniqueProviderIDs(ngResp.NodeGroups)
	advertisingSet := make(map[string]bool, len(advertisingIDs))
	for _, pid := range advertisingIDs {
		advertisingSet[pid] = true
	}
	for _, prov := range c.Providers {
		if !advertisingSet[prov.ID] {
			return nil, fmt.Errorf("provider %q (region=%s) is not advertising to the broker — expected: %v, advertising: %v",
				prov.ID, prov.Region, providerIDs(c.Providers), advertisingIDs)
		}
	}
	log.Printf("[setup] all %d providers advertising: %v", len(c.Providers), providerIDs(c.Providers))

	consoles := make(map[string]*ConsoleClient, len(c.Consumers))
	for _, cons := range c.Consumers {
		cc := NewConsoleClient(cons.ConsoleURL)
		log.Printf("[setup] checking consumer %s console at %s...", cons.ID, cons.ConsoleURL)
		_, err := cc.getState(ctx)
		if err != nil {
			return nil, fmt.Errorf("consumer %q console not reachable at %s: %w", cons.ID, cons.ConsoleURL, err)
		}
		consoles[cons.ID] = cc
		log.Printf("[setup] consumer %s: OK", cons.ID)
	}

	var eco *MockEcoClient
	if c.MockEco != nil && c.MockEco.URL != "" {
		eco = NewMockEcoClient(c.MockEco.URL)
		log.Printf("[setup] checking mock-eco at %s...", c.MockEco.URL)
		if err := eco.CheckHealthy(ctx); err != nil {
			return nil, fmt.Errorf("mock-eco not reachable at %s: %w", c.MockEco.URL, err)
		}
		log.Println("[setup] mock-eco: OK")
	}

	return &ExperimentClients{
		Identity:    id,
		CertFP:      certFP,
		Broker:      broker,
		Consoles:    consoles,
		MockEco:     eco,
		InitialNGs:  ngResp,
	}, nil
}

// ExperimentClients holds all validated, ready-to-use clients.
type ExperimentClients struct {
	Identity   Identity
	CertFP     string
	Broker     *BrokerClient                // primary (consumer-1) for shared queries
	Brokers    map[string]*BrokerClient      // per-consumer broker clients for reservations
	Consoles   map[string]*ConsoleClient
	MockEco    *MockEcoClient
	InitialNGs interface{}
}

// BrokerFor returns the BrokerClient for a specific consumer. Falls back to the
// primary Broker if no per-consumer client exists.
func (ec *ExperimentClients) BrokerFor(consumerID string) *BrokerClient {
	if bc, ok := ec.Brokers[consumerID]; ok {
		return bc
	}
	return ec.Broker
}

// SetPolicyAll sets the same policy on ALL consumers and waits for propagation.
func (ec *ExperimentClients) SetPolicyAll(ctx context.Context, policyType string, propagationWait time.Duration) error {
	for consID, cc := range ec.Consoles {
		if err := cc.SetPolicy(ctx, policyType); err != nil {
			return fmt.Errorf("set policy %q on consumer %s: %w", policyType, consID, err)
		}
		log.Printf("[policy] consumer %s → %s", consID, policyType)
	}
	if propagationWait > 0 {
		log.Printf("[policy] waiting %s for propagation...", propagationWait)
		return SleepCtx(ctx, propagationWait)
	}
	return nil
}

// SleepCtx sleeps for d or until ctx is cancelled.
func SleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// EnsureExperimentOutputDir creates the output directory for a specific test type.
func (c *ExperimentConfig) EnsureExperimentOutputDir(testType string) (string, error) {
	return EnsureOutputDir(c.Output.Dir, testType)
}

func providerIDs(providers []ProviderConfig) []string {
	ids := make([]string, len(providers))
	for i, p := range providers {
		ids[i] = p.ID
	}
	return ids
}
