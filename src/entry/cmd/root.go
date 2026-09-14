package cmd

import (
	"fmt"
	"opentela/internal/attestation"
	"opentela/internal/common"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var cfgFile string
var rootcmd = &cobra.Command{
	Use:   "otela",
	Short: "OpenTela is a decentralized fabric for running machine learning applications.",
	Long:  ``,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		return initConfig(cmd)
	},
	Run: func(cmd *cobra.Command, args []string) {
		err := cmd.Help()
		if err != nil {
			common.Logger.Error("Could not print help", "error", err)
		}
	},
}

//nolint:gochecknoinits
func init() {
	rootcmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/opentela/cfg.yaml)")
	rootcmd.PersistentFlags().String("config-dir", "", "use this directory as the config root (overrides --config and key location)")
	if err := viper.BindPFlag("config_dir", rootcmd.PersistentFlags().Lookup("config-dir")); err != nil {
		common.Logger.Error("Could not bind config-dir flag", "error", err)
	}
	startCmd.Flags().String("wallet.account", "", "wallet account")
	startCmd.Flags().String("wallet.address", "", "base58 public key (address) of the managed wallet to run as")
	startCmd.Flags().String("account.wallet", "", "path to wallet key file")
	startCmd.Flags().String("bootstrap.addr", "", "bootstrap address")
	startCmd.Flags().StringSlice("bootstrap.source", nil, "bootstrap source (HTTP URL, dnsaddr://host, or multiaddr). Repeatable")
	startCmd.Flags().StringSlice("bootstrap.static", []string{
		"https://bootstraps.opentela.ai/v1/dnt/bootstraps",
		"http://140.238.223.116:8092/v1/dnt/bootstraps",
		"http://152.67.64.117:8092/v1/dnt/bootstraps",
	}, "static bootstrap sources (HTTP URL, dnsaddr://, or multiaddr). Repeatable")
	startCmd.Flags().String("seed", "0", "Seed")
	startCmd.Flags().String("mode", "node", "Mode (standalone, local, full)")
	startCmd.Flags().String("tcpport", "43905", "TCP Port")
	startCmd.Flags().String("udpport", "59820", "UDP Port")
	startCmd.Flags().String("subprocess", "", "Subprocess to start")
	startCmd.Flags().String("public-addr", "", "Public address if you have one (by setting this, you can be a bootstrap node)")
	startCmd.Flags().String("service.name", "", "Service name")
	startCmd.Flags().String("service.port", "", "Service port")
	startCmd.Flags().String("service.health_path", "/health", "HTTP path used to health-check the service before registration")
	startCmd.Flags().Duration("service.health_interval", 30*time.Second, "How often to re-check the local service after registration; a service that stops answering is withdrawn from the advertisement")
	startCmd.Flags().Int("service.health_failures", 3, "Consecutive failed health checks before the local service is withdrawn")
	startCmd.Flags().StringArray("label", nil, "key=value metadata to attach to this peer in the DNT (repeatable, e.g. --label launched_by=$USER)")
	startCmd.Flags().String("solana.rpc", defaultConfig.Solana.RPC, "Solana RPC endpoint")
	startCmd.Flags().String("solana.mint", defaultConfig.Solana.Mint, "SPL token mint to verify ownership")
	startCmd.Flags().Bool("solana.skip_verification", defaultConfig.Solana.SkipVerification, "Skip Solana token ownership verification (use for testing only)")
	startCmd.Flags().String("lb-policy", "random", "Load balancing policy for global service routing (random, round-robin, shortest-queue)")
	startCmd.Flags().Bool("cleanslate", true, "Clean slate")
	startCmd.Flags().String("role", "worker", "Node role (worker, head, relay)")
	startCmd.Flags().Bool("admin.enabled", false, "Enable localhost-only admin HTTP route (for benchmarking)")
	startCmd.Flags().String("admin.port", "8093", "Port for the admin HTTP route (bound to 127.0.0.1)")
	startCmd.Flags().String("admin.bind", "127.0.0.1", "Bind address for admin HTTP route (default 127.0.0.1; set 0.0.0.0 in tests)")
	_ = viper.BindPFlag("admin.enabled", startCmd.Flags().Lookup("admin.enabled"))
	_ = viper.BindPFlag("admin.port", startCmd.Flags().Lookup("admin.port"))
	_ = viper.BindPFlag("admin.bind", startCmd.Flags().Lookup("admin.bind"))
	startCmd.Flags().Bool("analytics.enabled", false, "Enable PostHog performance analytics (head node)")
	startCmd.Flags().String("analytics.posthog_api_key", "", "PostHog project API key")
	startCmd.Flags().String("analytics.posthog_host", "https://us.i.posthog.com", "PostHog ingestion host (cloud US/EU or self-hosted)")
	_ = viper.BindPFlag("analytics.enabled", startCmd.Flags().Lookup("analytics.enabled"))
	_ = viper.BindPFlag("analytics.posthog_api_key", startCmd.Flags().Lookup("analytics.posthog_api_key"))
	_ = viper.BindPFlag("analytics.posthog_host", startCmd.Flags().Lookup("analytics.posthog_host"))
	rootcmd.AddCommand(initCmd)
	rootcmd.AddCommand(startCmd)
	rootcmd.AddCommand(versionCmd)
	rootcmd.AddCommand(updateCmd)
	rootcmd.AddCommand(balanceCmd)
	rootcmd.AddCommand(walletCmd)
	rootcmd.AddCommand(peerIDCmd)
	rootcmd.AddCommand(probeCmd)
}

// configFilePath returns the canonical path for the OpenTela config file.
// It first checks for the new ~/.config/opentela/cfg.yaml location; if that
// does not exist but the legacy ~/.config/ocf/cfg.yaml does, the legacy path
// is returned so the existing installation keeps working until the user
// migrates.
func configFilePath(home string) string {
	newPath := path.Join(home, ".config", configDirName, "cfg.yaml")
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}

	legacyPath := path.Join(home, ".config", legacyConfigDirName, "cfg.yaml")
	if _, err := os.Stat(legacyPath); err == nil {
		return legacyPath
	}

	// Neither exists yet — use the new location.
	return newPath
}

func initConfig(cmd *cobra.Command) error {
	var home string
	var err error
	if cd := viper.GetString("config_dir"); cd != "" && cfgFile == "" {
		cfgFile = filepath.Join(cd, "cfg.yaml")
	}
	viper.SetEnvPrefix("of")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	// Logging: "stderr" (zap default) or "stdout" (K8s-style collection)
	viper.SetDefault("log.output", "stderr")

	viper.SetDefault("crdt.tombstone_retention", "24h")
	viper.SetDefault("crdt.tombstone_compaction_interval", "1h")
	viper.SetDefault("crdt.tombstone_compaction_batch", 512)

	// Add billing configuration defaults (opt-in, disabled by default)
	viper.SetDefault("billing.enabled", false)
	viper.SetDefault("billing.value_threshold", 10000000) // lamports
	viper.SetDefault("billing.max_interval_minutes", 60)
	viper.SetDefault("billing.dispute_threshold_pct", 10)

	// Analytics (PostHog) — opt-in, disabled by default
	viper.SetDefault("analytics.enabled", false)
	viper.SetDefault("analytics.posthog_api_key", "")
	viper.SetDefault("analytics.posthog_host", "https://us.i.posthog.com")
	viper.SetDefault("analytics.flush_interval_seconds", 60)
	viper.SetDefault("analytics.parse_usage_body", true)
	viper.SetDefault("analytics.max_latency_samples", 512)

	viper.SetDefault("role", "worker")
	viper.SetDefault("security.require_signed_binary", true)
	// Trust anchors for build attestation. Defaults to the upstream release
	// key; operators signing their own builds append or replace it here
	// (OF_SECURITY_BUILD_PUBKEYS) instead of patching the attestation package.
	viper.SetDefault(attestation.PubKeysConfigKey, []string{attestation.DefaultPubKeyHex})
	viper.SetDefault("security.auth_url", "")
	viper.SetDefault("security.control_plane.url", "")
	viper.SetDefault("security.control_plane.token", "")
	viper.SetDefault("security.control_plane.timeout", "5s")
	viper.SetDefault("security.control_plane.cache_ttl", "60s")
	viper.SetDefault("security.control_plane.stale_if_error", "2m")

	// Metrics aggregation configuration (opt-in, disabled by default)
	viper.SetDefault("metrics.aggregation_enabled", false)
	viper.SetDefault("metrics.scrape_interval_seconds", 30)
	viper.SetDefault("metrics.scrape_timeout_seconds", 5)
	viper.SetDefault("metrics.worker_metrics_path", "/metrics")
	viper.SetDefault("metrics.max_concurrent_scrapes", 10)

	// SWIM membership protocol parameters
	viper.SetDefault("swim.probe_interval", "500ms")
	viper.SetDefault("swim.probe_timeout", "500ms")
	viper.SetDefault("swim.indirect_probe_timeout", "1s")
	viper.SetDefault("swim.indirect_probes", 3)
	viper.SetDefault("swim.suspect_timeout", "5s")
	viper.SetDefault("swim.retransmit_mult", 3)
	viper.SetDefault("swim.metadata_max_bytes", 256)

	// Production logging mode: reduces log volume via sampling (opt-in)
	viper.SetDefault("production_logging", false)

	// Server-Timing per-request stage instrumentation (opt-in, disabled by default
	// because emitting headers does ~6 time.Now() calls per request).
	viper.SetDefault("observability.timing_headers", false)

	// Localhost-only admin HTTP route used by contrib/benchmark_convergence
	// to inject CRDT writes from the orchestrator. Off by default; bound to
	// 127.0.0.1 only. The bind address can be overridden via admin.bind for
	// test environments (e.g. OF_ADMIN_BIND=0.0.0.0 in Docker integration tests).
	viper.SetDefault("admin.enabled", false)
	viper.SetDefault("admin.port", "8093")
	viper.SetDefault("admin.bind", "127.0.0.1")

	// Scalability feature flags (all default to false for safe rollout)
	viper.SetDefault("scalability.swim_enabled", false)
	viper.SetDefault("scalability.crdt_tuned", false)
	viper.SetDefault("scalability.weighted_routing", false)
	viper.SetDefault("scalability.admission_control", false)
	viper.SetDefault("scalability.expected_workers", 0) // 0 = auto (disabled)

	// Request retry/re-route on transport failure (enabled by default)
	viper.SetDefault("routing.retry_enabled", true)
	viper.SetDefault("routing.max_retries", 3)
	viper.SetDefault("routing.max_response_buffer_bytes", 64*1024*1024) // 64MB
	// Price-aware routing: exponential selection-weight decay over the billing
	// gate's cheapest-first X-Otela-Allowed-Peers order. 0 disables (uniform
	// legacy policies); e.g. 0.5 makes the cheapest affordable peer roughly
	// twice as likely as the next. Env: OF_ROUTING_PRICE_WEIGHT_DECAY.
	viper.SetDefault("routing.price_weight_decay", 0.0)

	// CRDT tuned values (used when scalability.crdt_tuned=true)
	viper.SetDefault("crdt.tuned_gossipsub_d", 10)
	viper.SetDefault("crdt.tuned_gossipsub_dlo", 4)
	viper.SetDefault("crdt.tuned_gossipsub_dhi", 16)
	viper.SetDefault("crdt.tuned_rebroadcast_interval", "60s")
	viper.SetDefault("crdt.tuned_workers", 16)

	// Don't forget to read config either from cfgFile or from home directory!
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
		// print out the config file
		common.Logger.Debug("Using config file: ", viper.ConfigFileUsed())
	} else {
		// Find home directory.
		home, err = homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		viper.SetConfigFile(configFilePath(home))
	}
	if err = viper.ReadInConfig(); err != nil {
		viper.SetDefault("path", defaultConfig.Path)
		viper.SetDefault("port", defaultConfig.Port)
		viper.SetDefault("name", defaultConfig.Name)
		viper.SetDefault("p2p", defaultConfig.P2p)
		viper.SetDefault("tcpport", defaultConfig.TCPPort)
		viper.SetDefault("udpport", defaultConfig.UDPPort)
		viper.SetDefault("vacuum.interval", defaultConfig.Vacuum.Interval)
		viper.SetDefault("queue.port", defaultConfig.Queue.Port)
		viper.SetDefault("account.wallet", defaultConfig.Account.Wallet)
		viper.SetDefault("wallet.account", "")
		viper.SetDefault("solana.rpc", defaultConfig.Solana.RPC)
		viper.SetDefault("solana.mint", defaultConfig.Solana.Mint)
		viper.SetDefault("solana.skip_verification", defaultConfig.Solana.SkipVerification)
		// Seed the config at whatever path viper was told to use (either via
		// --config / cfgFile, or the default $HOME/.config/opentela path).
		// This ensures `otela init --config-dir <dir>` writes the seed under
		// <dir>/cfg.yaml rather than recomputing the path from $HOME.
		configPath := viper.ConfigFileUsed()
		if configPath == "" {
			// Fallback for safety; in practice SetConfigFile was called above
			// in both branches, so this should not be reached.
			configPath = path.Join(home, ".config", configDirName, "cfg.yaml")
		}
		err = os.MkdirAll(path.Dir(configPath), os.ModePerm)
		if err != nil {
			common.Logger.Error("Could not create config directory", "error", err)
			os.Exit(1)
		}

		if err = viper.SafeWriteConfigAs(configPath); err != nil {
			if os.IsNotExist(err) {
				err = viper.WriteConfigAs(configPath)
				if err != nil {
					common.Logger.Warn("Cannot write config file", "error", err)
				}
			}
		}
	}
	// Bind each Cobra Flag to its associated Viper Key
	cmd.Flags().VisitAll(func(flag *pflag.Flag) {
		if flag.Changed || !viper.IsSet(flag.Name) {
			switch flag.Value.Type() {
			case "bool":
				value, err := strconv.ParseBool(flag.Value.String())
				if err != nil {
					viper.Set(flag.Name, flag.Value)
				} else {
					viper.Set(flag.Name, value)
				}
			case "int":
				value, err := strconv.ParseInt(flag.Value.String(), 0, 64)
				if err != nil {
					viper.Set(flag.Name, flag.Value)
				} else {
					viper.Set(flag.Name, value)
				}
			case "stringSlice", "stringArray":
				if sliceValue, ok := flag.Value.(pflag.SliceValue); ok {
					viper.Set(flag.Name, sliceValue.GetSlice())
				} else {
					viper.Set(flag.Name, strings.Split(flag.Value.String(), ","))
				}
			default:
				viper.Set(flag.Name, flag.Value)
			}
		}
	})
	common.InitLogger()
	return nil
}

func Execute() {
	if err := rootcmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
