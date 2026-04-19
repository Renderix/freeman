package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"text/template"
	"time"

	"github.com/getlantern/systray"
	"github.com/spf13/cobra"
)

// menubarLabel is the launchd label for the menu-bar LaunchAgent.
// Separate from the voice daemon so it can be started/stopped
// independently — the menu bar should stay up even when the voice
// daemon is disengaged, so you can bring the voice daemon back.
const menubarLabel = "com.freeman.menubar"

const menubarPlistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>{{.Label}}</string>
    <key>ProgramArguments</key>
    <array>
        <string>{{.Binary}}</string>
        <string>menubar</string>
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
      No KeepAlive: if the menu bar crashes or you quit it, it stays
      down. Bring it back with:
          freeman menubar start
      Or reinstall everything cleanly with: freeman install
    -->
    <key>StandardOutPath</key>
    <string>{{.StdoutLog}}</string>
    <key>StandardErrorPath</key>
    <string>{{.StderrLog}}</string>
</dict>
</plist>
`

type menubarPaths struct {
	home    string
	target  string
	binary  string
	plist   string
	stdout  string
	stderr  string
}

func resolveMenubarPaths() (menubarPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return menubarPaths{}, err
	}
	target := filepath.Join(home, ".freeman")
	return menubarPaths{
		home:   home,
		target: target,
		binary: filepath.Join(target, "freeman"),
		plist:  filepath.Join(home, "Library", "LaunchAgents", menubarLabel+".plist"),
		stdout: filepath.Join(target, "logs", "menubar.out.log"),
		stderr: filepath.Join(target, "logs", "menubar.err.log"),
	}, nil
}

// menubarCmd is the entry point invoked by launchd (or directly by the
// user). It blocks for the lifetime of the menu bar icon; systray.Run
// holds the macOS main thread.
var menubarCmd = &cobra.Command{
	Use:   "menubar",
	Short: "Run the Freeman menu bar icon (blocks)",
	Long: `Displays a status icon in the macOS menu bar that shows
whether the Freeman voice daemon is running, and lets you
enable/disable it with a click.

Typically managed by launchd via 'freeman menubar start'. You can also
run it directly in a foreground terminal to try it without installing
a LaunchAgent.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		systray.Run(onMenubarReady, onMenubarExit)
		return nil
	},
}

var menubarStartCmd = &cobra.Command{
	Use:   "start",
	Short: "Install the menu bar LaunchAgent and load it",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolveMenubarPaths()
		if err != nil {
			return err
		}
		if _, err := os.Stat(p.binary); err != nil {
			return fmt.Errorf("freeman binary not found at %s — run `freeman install` first", p.binary)
		}
		if err := writeMenubarPlistIfMissing(p); err != nil {
			return err
		}
		if menubarIsLoaded() {
			if err := menubarKickstart(); err != nil {
				return fmt.Errorf("kickstart: %w", err)
			}
			fmt.Printf("menubar restarted\n")
			return nil
		}
		if err := menubarBootstrap(p.plist); err != nil {
			return fmt.Errorf("bootstrap: %w", err)
		}
		fmt.Printf("menubar started\n  plist: %s\n", p.plist)
		return nil
	},
}

var menubarStopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Unload the menu bar LaunchAgent (plist remains)",
	RunE: func(cmd *cobra.Command, args []string) error {
		return menubarBootout()
	},
}

var menubarUninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Unload the menu bar and remove its LaunchAgent plist",
	RunE: func(cmd *cobra.Command, args []string) error {
		p, err := resolveMenubarPaths()
		if err != nil {
			return err
		}
		_ = menubarBootout()
		if err := os.Remove(p.plist); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		fmt.Printf("menubar uninstalled\n")
		return nil
	},
}

func writeMenubarPlistIfMissing(p menubarPaths) error {
	if _, err := os.Stat(p.plist); err == nil {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(p.plist), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(p.target, "logs"), 0o755); err != nil {
		return err
	}
	tmpl, err := template.New("menubar-plist").Parse(menubarPlistTemplate)
	if err != nil {
		return err
	}
	f, err := os.Create(p.plist)
	if err != nil {
		return err
	}
	defer f.Close()
	return tmpl.Execute(f, map[string]string{
		"Label":     menubarLabel,
		"Binary":    p.binary,
		"WorkDir":   p.target,
		"Home":      p.home,
		"StdoutLog": p.stdout,
		"StderrLog": p.stderr,
	})
}

// menubarBootstrap / Bootout / Kickstart / IsLoaded mirror the voice
// daemon's launchctl helpers but target the menubar label.

func menubarBootstrap(plistPath string) error {
	if err := exec.Command("launchctl", "bootstrap", gUIDomain(), plistPath).Run(); err == nil {
		return nil
	}
	return exec.Command("launchctl", "load", "-w", plistPath).Run()
}

func menubarBootout() error {
	if err := exec.Command("launchctl", "bootout", gUIDomain()+"/"+menubarLabel).Run(); err == nil {
		return nil
	}
	p, err := resolveMenubarPaths()
	if err != nil {
		return err
	}
	return exec.Command("launchctl", "unload", "-w", p.plist).Run()
}

func menubarIsLoaded() bool {
	return exec.Command("launchctl", "print", gUIDomain()+"/"+menubarLabel).Run() == nil
}

func menubarKickstart() error {
	return exec.Command("launchctl", "kickstart", "-k", gUIDomain()+"/"+menubarLabel).Run()
}

// ─── UI ─────────────────────────────────────────────────────────────────────

// Items kept at package scope so the polling goroutine can toggle
// their visibility / labels from outside the main click loop.
var (
	itemEnable     *systray.MenuItem
	itemDisable    *systray.MenuItem
	itemOpenViewer *systray.MenuItem
	itemQuit       *systray.MenuItem

	// lastRunning holds the most recent daemon-running state so we only
	// update the systray title / item visibility when it changes.
	lastRunning atomic.Bool
)

func onMenubarReady() {
	systray.SetTitle("🔴 Horus")
	systray.SetTooltip("Freeman voice daemon status")

	itemEnable = systray.AddMenuItem("Enable", "Start the voice daemon")
	itemDisable = systray.AddMenuItem("Disable", "Stop the voice daemon (say 'disengage' or click here)")
	systray.AddSeparator()
	itemOpenViewer = systray.AddMenuItem("Open Log Viewer", "http://127.0.0.1:17001/")
	systray.AddSeparator()
	itemQuit = systray.AddMenuItem("Quit Freeman", "Stop the voice daemon and exit the menu bar. Run `freeman install` or `freeman menubar start` to bring it back.")

	itemDisable.Hide()

	go menubarStatePoller()
	go menubarClickLoop()
}

func onMenubarExit() {
	// systray exit — nothing to clean up. launchd will relaunch us if
	// KeepAlive is set, which is the normal "user quit" path.
}

// menubarStatePoller updates the title + enable/disable visibility
// every 2 seconds by asking launchctl whether the voice daemon is
// currently running.
func menubarStatePoller() {
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	applyState(voiceDaemonRunning())
	for range tick.C {
		applyState(voiceDaemonRunning())
	}
}

func applyState(running bool) {
	prev := lastRunning.Swap(running)
	if prev == running {
		return
	}
	if running {
		systray.SetTitle("🟢 Horus")
		itemEnable.Hide()
		itemDisable.Show()
	} else {
		systray.SetTitle("🔴 Horus")
		itemEnable.Show()
		itemDisable.Hide()
	}
}

func menubarClickLoop() {
	for {
		select {
		case <-itemEnable.ClickedCh:
			if err := voiceDaemonEnable(); err != nil {
				// No UI for errors yet; log to stderr (captured by launchd).
				fmt.Fprintf(os.Stderr, "menubar: enable failed: %v\n", err)
			}
			applyState(voiceDaemonRunning())
		case <-itemDisable.ClickedCh:
			if err := voiceDaemonDisable(); err != nil {
				fmt.Fprintf(os.Stderr, "menubar: disable failed: %v\n", err)
			}
			applyState(voiceDaemonRunning())
		case <-itemOpenViewer.ClickedCh:
			_ = exec.Command("open", "http://127.0.0.1:17001/").Start()
		case <-itemQuit.ClickedCh:
			// Quit turns the whole assistant off: send SIGTERM to the
			// voice daemon (same clean exit as "disengage"), then exit
			// the menu bar. Both plists have SuccessfulExit=false
			// KeepAlive, so launchd leaves both services stopped.
			_ = voiceDaemonDisable()
			systray.Quit()
			return
		}
	}
}

// voiceDaemonRunning reports whether the voice daemon (not just the
// plist) is actually running. Parses `launchctl print` output, which
// includes a `state = running` line while the service is live.
func voiceDaemonRunning() bool {
	out, err := exec.Command("launchctl", "print", gUIDomain()+"/"+daemonLabel).Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "state = running")
}

// voiceDaemonEnable starts the voice daemon. If its plist isn't
// installed yet, this shells out to `freeman daemon start` (via the
// current binary) so the full bootstrap path runs, including plist
// creation.
func voiceDaemonEnable() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// `freeman daemon start` is idempotent: kickstarts if loaded,
	// bootstraps if not.
	return exec.Command(exe, "daemon", "start").Run()
}

// voiceDaemonDisable sends SIGTERM to the daemon. The voice process
// traps SIGTERM via signal.NotifyContext and performs the same
// graceful shutdown as saying "disengage" — exit 0, launchd leaves it
// stopped.
func voiceDaemonDisable() error {
	return exec.Command("launchctl", "kill", "TERM", gUIDomain()+"/"+daemonLabel).Run()
}

func init() {
	menubarCmd.AddCommand(menubarStartCmd, menubarStopCmd, menubarUninstallCmd)
}
