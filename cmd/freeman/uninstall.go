package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// uninstallCmd is the single command that reverses `freeman install`.
// By default it unloads both LaunchAgents and removes their plists,
// but leaves ~/.freeman intact so session logs, user-authored tools,
// and any hand-edited config survive. Pass --purge to wipe that too.
var uninstallCmd = &cobra.Command{
	Use:   "uninstall",
	Short: "Stop services, remove LaunchAgent plists, optionally purge ~/.freeman",
	Long: `Reverses 'freeman install'. Unloads the voice daemon and the
menu-bar LaunchAgent, then removes both plists from
~/Library/LaunchAgents/.

By default ~/.freeman/ is preserved so session logs, user-authored
tools in ~/.freeman/tools/, and any hand-edited config.yaml survive a
reinstall. Pass --purge to delete ~/.freeman/ as well.`,
	RunE: runUninstall,
}

var uninstallPurge bool

func init() {
	uninstallCmd.Flags().BoolVar(&uninstallPurge, "purge", false,
		"Also remove ~/.freeman/ (logs, config, tools)")
}

func runUninstall(cmd *cobra.Command, args []string) error {
	// 1. Unload + remove voice daemon plist. Bootout is best-effort —
	// the service may already be stopped.
	dp, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	_ = launchctlBootout()
	if err := os.Remove(dp.plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove voice plist: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ voice daemon unloaded (%s)\n", dp.plist)

	// 2. Same for the menu bar LaunchAgent.
	mp, err := resolveMenubarPaths()
	if err != nil {
		return err
	}
	_ = menubarBootout()
	if err := os.Remove(mp.plist); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove menubar plist: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ menu bar unloaded (%s)\n", mp.plist)

	// 3. Optionally purge the whole runtime dir.
	if uninstallPurge {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		target := filepath.Join(home, ".freeman")
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("purge %s: %w", target, err)
		}
		fmt.Fprintf(os.Stderr, "  ✓ purged %s\n", target)
	} else {
		fmt.Fprintf(os.Stderr, "  · ~/.freeman preserved (use --purge to remove)\n")
	}
	fmt.Fprintf(os.Stderr, "\nUninstalled.\n")
	return nil
}
