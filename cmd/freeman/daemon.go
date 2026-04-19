package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/spf13/cobra"
)

// daemonLabel is the launchd label (and plist basename) for Freeman's
// per-user agent. Using a reverse-DNS-ish form is the macOS convention
// and keeps it from colliding with other user agents.
const daemonLabel = "com.freeman.agent"

// plistTemplate is the LaunchAgent plist written to
// ~/Library/LaunchAgents/<daemonLabel>.plist. It starts Freeman at
// login, restarts it on crash, and routes stdout/stderr into the
// per-install log directory so failures don't vanish silently.
const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
        <string>call</string>
    </array>
    <key>WorkingDirectory</key>
    <string>{{.WorkDir}}</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>{{.Home}}/.bun/bin:/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin</string>
        <key>HOME</key>
        <string>{{.Home}}</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <!--
      No KeepAlive: if the daemon crashes, it stays down. Better to fail
      loudly than to crash-loop silently. Bring it back with:
          freeman daemon start
      Or click the menu bar icon. "Disengage" exits 0, same effect.
    -->
    <key>StandardOutPath</key>
    <string>{{.StdoutLog}}</string>
    <key>StandardErrorPath</key>
    <string>{{.StderrLog}}</string>
</dict>
</plist>
`

type daemonPaths struct {
	home     string
	target   string // $HOME/.freeman
	binary   string
	plist    string
	stdout   string
	stderr   string
}

func resolveDaemonPaths() (daemonPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return daemonPaths{}, err
	}
	target := filepath.Join(home, ".freeman")
	return daemonPaths{
		home:   home,
		target: target,
		binary: filepath.Join(target, "freeman"),
		plist:  filepath.Join(home, "Library", "LaunchAgents", daemonLabel+".plist"),
		stdout: filepath.Join(target, "logs", "daemon.out.log"),
		stderr: filepath.Join(target, "logs", "daemon.err.log"),
	}, nil
}

var daemonCmd = &cobra.Command{
	Use:   "daemon",
	Short: "Manage Freeman as a macOS launchd user agent",
	Long: `Controls the Freeman background service.

Run 'freeman install' first to place the binary + config at ~/.freeman/.
Then 'daemon start' writes the launchd plist (if missing) and loads
the service. It runs at every login, is kept alive across crashes, and
logs to ~/.freeman/logs/daemon.{out,err}.log.

'daemon stop' pauses the service without removing the plist. 'daemon
uninstall' removes the plist entirely. The voice process also hosts the
log viewer at http://127.0.0.1:17001/, so monitoring is always live
while the daemon is running.`,
}

var daemonStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Start (or wake) the daemon",
	Long: `Starts the Freeman daemon. Idempotent:

- First invocation: writes the LaunchAgent plist and bootstraps the
  service.
- Repeat invocation when the service is loaded but stopped (e.g. after
  you said "disengage"): kickstarts it so it runs again without
  needing to unload/reload.

"Disengage" exits cleanly (code 0); the plist's KeepAlive is
conditional on non-zero exit, so the service stays stopped until you
run this command.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolveDaemonPaths()
		if err != nil {
			return err
		}
		if _, err := os.Stat(p.binary); err != nil {
			return fmt.Errorf("freeman binary not found at %s — run `freeman install` from the source repo first", p.binary)
		}
		if err := writePlistIfMissing(p); err != nil {
			return err
		}
		if launchctlIsLoaded() {
			if err := launchctlKickstart(); err != nil {
				return fmt.Errorf("launchctl kickstart: %w", err)
			}
			fmt.Printf("daemon kickstarted\n  viewer: http://127.0.0.1:17001/\n")
			return nil
		}
		if err := launchctlBootstrap(p.plist); err != nil {
			return fmt.Errorf("launchctl bootstrap: %w", err)
		}
		fmt.Printf("daemon started\n  plist:  %s\n  logs:   %s\n  viewer: http://127.0.0.1:17001/\n", p.plist, filepath.Dir(p.stdout))
		return nil
	},
}

var daemonUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop the service and remove the LaunchAgent plist",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolveDaemonPaths()
		if err != nil {
			return err
		}
		// Bootout is best-effort; ignore error if not loaded.
		_ = launchctlBootout()
		if err := os.Remove(p.plist); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Printf("daemon uninstalled (plist removed)\n")
		return nil
	},
}

// writePlistIfMissing materialises the LaunchAgent plist from the
// template. It never overwrites an existing plist — users may have
// hand-tweaked theirs. Callers that want a clean slate should
// 'daemon uninstall' first.
func writePlistIfMissing(p daemonPaths) error {
	if _, err := os.Stat(p.plist); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.plist), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(p.target, "logs"), 0o755); err != nil {
		return err
	}
	tmpl, err := template.New("plist").Parse(plistTemplate)
	if err != nil {
		return err
	}
	f, err := os.Create(p.plist)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, map[string]string{
		"Label":     daemonLabel,
		"Binary":    p.binary,
		"WorkDir":   p.target,
		"Home":      p.home,
		"StdoutLog": p.stdout,
		"StderrLog": p.stderr,
	})
}

var daemonStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the installed service (plist remains, service can be restarted)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return launchctlBootout()
	},
}

var daemonStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Report whether the service is loaded and its last exit code",
	RunE: func(cmd *cobra.Command, args []string) error {
		out, err := exec.Command("launchctl", "print", gUIDomain()+"/"+daemonLabel).CombinedOutput()
		text := string(out)
		if err != nil {
			// Not loaded: launchctl exits non-zero with a concise message.
			fmt.Printf("not loaded\n")
			if strings.TrimSpace(text) != "" {
				fmt.Printf("(%s)\n", strings.TrimSpace(text))
			}
			return nil
		}
		// Trim the output: launchctl print is verbose. Pull the lines
		// that matter for a quick status readout.
		wanted := map[string]bool{"state": true, "pid": true, "last exit code": true, "last exit reason": true}
		for _, line := range strings.Split(text, "\n") {
			l := strings.TrimSpace(line)
			for key := range wanted {
				if strings.HasPrefix(strings.ToLower(l), key+" =") {
					fmt.Println(l)
					break
				}
			}
		}
		return nil
	},
}

// launchctlBootstrap loads the plist into the user's GUI session. Falls
// back to the legacy `load` verb on older macOS where `bootstrap` isn't
// available.
func launchctlBootstrap(plistPath string) error {
	target := gUIDomain()
	if err := exec.Command("launchctl", "bootstrap", target, plistPath).Run(); err == nil {
		return nil
	}
	return exec.Command("launchctl", "load", "-w", plistPath).Run()
}

// launchctlIsLoaded reports whether the agent is currently loaded in
// the user's GUI session (regardless of whether it's actually running).
func launchctlIsLoaded() bool {
	return exec.Command("launchctl", "print", gUIDomain()+"/"+daemonLabel).Run() == nil
}

// launchctlKickstart asks launchd to (re)start the loaded service.
// Needed after a clean disengage exit — the plist stays loaded but the
// process is gone, and bootstrap would error with "already loaded".
func launchctlKickstart() error {
	target := gUIDomain() + "/" + daemonLabel
	return exec.Command("launchctl", "kickstart", "-k", target).Run()
}

// launchctlBootout unloads the service. Tries modern `bootout` then
// legacy `unload`.
func launchctlBootout() error {
	target := gUIDomain() + "/" + daemonLabel
	if err := exec.Command("launchctl", "bootout", target).Run(); err == nil {
		return nil
	}
	// Legacy: needs plist path, resolve it.
	p, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	return exec.Command("launchctl", "unload", "-w", p.plist).Run()
}

// gUIDomain returns the launchd domain specifier for the current user's
// GUI session, e.g. "gui/501". Required by bootstrap/bootout.
func gUIDomain() string {
	return fmt.Sprintf("gui/%d", os.Getuid())
}

func init() {
	daemonCmd.AddCommand(daemonStartCmd, daemonStopCmd, daemonStatusCmd, daemonUninstallCmd)
}
