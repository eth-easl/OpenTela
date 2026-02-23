package cmd

import (
	"fmt"
	"opentela/internal/common"
	"os"
	"path"
	"strconv"
	"strings"

	homedir "github.com/mitchellh/go-homedir"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

var cfgFile string
var rootcmd = &cobra.Command{
	Use:   "ocf",
	Short: "OpenFabric is a decentralized fabric for running machine learning applications.",
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
	rootcmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.config/ocf/cfg.yaml)")
	startCmd.Flags().String("wallet.account", "", "wallet account")
	startCmd.Flags().String("account.wallet", "", "path to wallet key file")
	startCmd.Flags().String("bootstrap.addr", "http://152.67.71.5:8092/v1/dnt/bootstraps", "bootstrap address")
	startCmd.Flags().StringSlice("bootstrap.source", nil, "bootstrap source (HTTP URL, dnsaddr://host, or multiaddr). Repeatable")
	startCmd.Flags().StringSlice("bootstrap.static", nil, "static bootstrap multiaddr (repeatable)")
	startCmd.Flags().String("seed", "0", "Seed")
	startCmd.Flags().String("mode", "node", "Mode (standalone, local, full)")
	startCmd.Flags().String("tcpport", "43905", "TCP Port")
	startCmd.Flags().String("udpport", "59820", "UDP Port")
	startCmd.Flags().String("subprocess", "", "Subprocess to start")
	startCmd.Flags().String("public-addr", "", "Public address if you have one (by setting this, you can be a bootstrap node)")
	startCmd.Flags().String("service.name", "", "Service name")
	startCmd.Flags().String("service.port", "", "Service port")
	startCmd.Flags().String("solana.rpc", defaultConfig.Solana.RPC, "Solana RPC endpoint")
	startCmd.Flags().String("solana.mint", defaultConfig.Solana.Mint, "SPL token mint to verify ownership")
	startCmd.Flags().Bool("solana.skip_verification", defaultConfig.Solana.SkipVerification, "Skip Solana token ownership verification (use for testing only)")
	startCmd.Flags().Bool("cleanslate", true, "Clean slate")
	rootcmd.AddCommand(initCmd)
	rootcmd.AddCommand(startCmd)
	rootcmd.AddCommand(versionCmd)
	rootcmd.AddCommand(updateCmd)
}

func initConfig(cmd *cobra.Command) error {
	var home string
	var err error
	viper.SetEnvPrefix("of")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()
	viper.SetDefault("crdt.tombstone_retention", "24h")
	viper.SetDefault("crdt.tombstone_compaction_interval", "1h")
	viper.SetDefault("crdt.tombstone_compaction_batch", 512)
	// Don't forget to read config either from cfgFile or from home directory!
	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
		// print out the config file
		common.Logger.Info("Using config file: ", viper.ConfigFileUsed())
	} else {
		// Find home directory.
		home, err = homedir.Dir()
		if err != nil {
			fmt.Println(err)
			os.Exit(1)
		}
		viper.SetConfigFile(path.Join(home, ".config", "ocf", "cfg.yaml"))
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
		configPath := path.Join(home, ".config", "ocf", "cfg.yaml")
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
