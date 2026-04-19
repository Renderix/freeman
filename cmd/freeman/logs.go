package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/Renderix/freeman/internal/logs"
	"github.com/spf13/cobra"
)

var (
	logsPort   int
	logsNoOpen bool
	logsDir    string
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Start a local HTML viewer for Freeman session logs",
	Long: `Parses log files under ~/.freeman/logs/ and serves a browser UI
that groups events by turn, surfaces tool inputs and outputs, and
highlights dead-air latency.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		root := logsDir
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("resolve home: %w", err)
			}
			root = filepath.Join(home, ".freeman", "logs")
		}
		if _, err := os.Stat(root); err != nil {
			return fmt.Errorf("logs dir %s: %w", root, err)
		}
		srv := logs.NewServer(root)
		ln, url, err := srv.Listen(logsPort)
		if err != nil {
			return fmt.Errorf("listen: %w", err)
		}
		fmt.Fprintf(os.Stderr, "freeman logs: serving %s\n", url)
		if !logsNoOpen {
			// Give the server a beat to start accepting, then pop browser.
			go func() {
				time.Sleep(200 * time.Millisecond)
				openBrowser(url)
			}()
		}
		return srv.Serve(ln)
	},
}

func init() {
	logsCmd.Flags().IntVar(&logsPort, "port", 17001, "TCP port to bind (0 = auto)")
	logsCmd.Flags().BoolVar(&logsNoOpen, "no-open", false, "Do not auto-open a browser window")
	logsCmd.Flags().StringVar(&logsDir, "dir", "", "Logs directory (default ~/.freeman/logs)")
}

// openBrowser opens the URL using the platform's default handler.
// Failure is silent — the URL is already printed to stderr.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
