package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/Blink-Build-Studios/little-tyke/cmd/little-tyke/cmd/serve"
	"github.com/Blink-Build-Studios/little-tyke/cmd/little-tyke/cmd/version"
)

var RootCmd = &cobra.Command{
	Use:   "little-tyke",
	Short: "Self-hosted Gemma 4 with OpenAI-compatible API",
	Long:  "little-tyke runs Gemma 4 locally via Ollama and exposes an OpenAI-compatible chat completions API.",
}

func init() {
	cobra.OnInitialize(initConfig)

	pflags := RootCmd.PersistentFlags()

	pflags.String("log-level", "info", "log level (trace, debug, info, warn, error, fatal)")
	_ = viper.BindPFlag("log_level", pflags.Lookup("log-level"))

	pflags.String("config", "", "config file path")
	_ = viper.BindPFlag("config", pflags.Lookup("config"))

	RootCmd.AddCommand(serve.Cmd)
	RootCmd.AddCommand(versionCmd)
}

func initConfig() {
	if cfgFile := viper.GetString("config"); cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		viper.SetConfigName("little-tyke")
		viper.SetConfigType("yaml")
		viper.AddConfigPath(".")
		viper.AddConfigPath("/etc/little-tyke")
	}

	viper.SetEnvPrefix("LITTLE_TYKE")
	viper.AutomaticEnv()

	if err := viper.ReadInConfig(); err != nil {
		if viper.GetString("config") != "" {
			fmt.Printf("Error reading config file: %s\n", err)
		}
	}
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("little-tyke %s (commit: %s)\n", version.Version, version.GitCommit)
	},
}
