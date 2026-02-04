package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Renderix/freeman/internal/api"
	"github.com/Renderix/freeman/internal/config"
	"github.com/Renderix/freeman/internal/engine"
	"github.com/spf13/cobra"
)

var (
	configFile string
)

func main() {
	var rootCmd = &cobra.Command{
		Use:   "freeman",
		Short: "Freeman TTS - High performance real-time TTS server",
	}

	var startCmd = &cobra.Command{
		Use:   "start",
		Short: "Start the TTS server",
		Run: func(cmd *cobra.Command, args []string) {
			conf := config.LoadConfig(configFile)

			// Model paths from config
			modelPath := filepath.Join(conf.Model.Dir, conf.Model.ModelFile)
			voicesPath := filepath.Join(conf.Model.Dir, conf.Model.VoicesFile)
			tokensPath := filepath.Join(conf.Model.Dir, conf.Model.TokensFile)
			dataDir := filepath.Join(conf.Model.Dir, conf.Model.DataDir)

			fmt.Printf("🚀 Initializing Kokoro TTS engine from %s...\n", conf.Model.Dir)

			ttsEngine, err := engine.NewTTSEngine(modelPath, voicesPath, tokensPath, dataDir)
			if err != nil {
				fmt.Printf("❌ Failed to initialize TTS engine: %v\n", err)
				os.Exit(1)
			}

			server := api.NewServer(ttsEngine, conf)
			fmt.Printf("✅ Freeman engine ready! Listening on port %d\n", conf.Server.Port)

			if err := server.Start(conf.Server.Port); err != nil {
				fmt.Printf("❌ Server error: %v\n", err)
				os.Exit(1)
			}
		},
	}

	rootCmd.PersistentFlags().StringVar(&configFile, "config", "config.yaml", "Path to config file")

	var versionCmd = &cobra.Command{
		Use:   "version",
		Short: "Show version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Println("Freeman TTS v0.1.0 (Go Edition)")
		},
	}

	rootCmd.AddCommand(startCmd, versionCmd)

	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
