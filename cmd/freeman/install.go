package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// installCmd assembles a self-contained runtime at ~/.freeman/:
//
//   ~/.freeman/freeman       — compiled binary (copied)
//   ~/.freeman/config.yaml   — config (copied fresh or preserved)
//   ~/.freeman/tools/        — symlink to repo's tools/
//   ~/.freeman/models/       — symlink to repo's models/
//   ~/.freeman/sidecar/      — symlink to repo's sidecar/
//   ~/.freeman/logs/         — created for session + daemon logs
//
// Heavy dirs are symlinked so upstream edits flow through without
// duplicating hundreds of MB. That means the source repo must stay
// where it is after install — moving it invalidates the symlinks.
var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Build and install Freeman into ~/.freeman/",
	Long: `Installs Freeman as a self-contained runtime under ~/.freeman/.
Must be run from the source repository (needs go.mod to build).

The binary is (re)built via 'go build' and copied. Config is copied
fresh the first time; on subsequent runs you'll be asked whether to
overwrite. Heavy asset directories (models/, sidecar/, tools/) are
symlinked so updates flow through without copying gigabytes.`,
	RunE: runInstall,
}

var installOverwriteConfig bool
var installYes bool

func init() {
	installCmd.Flags().BoolVar(&installOverwriteConfig, "overwrite-config", false,
		"Replace an existing ~/.freeman/config.yaml without prompting")
	installCmd.Flags().BoolVarP(&installYes, "yes", "y", false,
		"Answer yes to all prompts (assume defaults)")
}

func runInstall(cmd *cobra.Command, args []string) error {
	source, err := findSourceRepo()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	target := filepath.Join(home, ".freeman")

	fmt.Fprintf(os.Stderr, "→ Installing Freeman into %s (source: %s)\n", target, source)

	// 1. Build.
	fmt.Fprintf(os.Stderr, "→ Building binary (go build)…\n")
	build := exec.Command("go", "build", "-o", "freeman", "./cmd/freeman")
	build.Dir = source
	build.Stdout = os.Stderr
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("go build failed: %w — is Go installed and on PATH?", err)
	}

	if err := os.MkdirAll(target, 0o755); err != nil {
		return err
	}

	// 2. Copy binary.
	binSrc := filepath.Join(source, "freeman")
	binDst := filepath.Join(target, "freeman")
	if err := copyFileExecutable(binSrc, binDst); err != nil {
		return fmt.Errorf("copy binary: %w", err)
	}
	fmt.Fprintf(os.Stderr, "  ✓ binary → %s\n", binDst)

	// 3. Handle config.yaml.
	cfgSrc := filepath.Join(source, "config.yaml")
	cfgDst := filepath.Join(target, "config.yaml")
	if err := handleConfig(cfgSrc, cfgDst); err != nil {
		return err
	}

	// 4. Symlink heavy asset dirs. lib/ carries libonnxruntime; the
	// wakeword detector resolves it relative to the binary's directory
	// at runtime.
	for _, name := range []string{"models", "sidecar", "tools", "lib"} {
		if err := linkIfMissingOrSymlink(filepath.Join(source, name), filepath.Join(target, name)); err != nil {
			return fmt.Errorf("symlink %s: %w", name, err)
		}
	}

	// 5. Logs dir.
	if err := os.MkdirAll(filepath.Join(target, "logs"), 0o755); err != nil {
		return err
	}

	// 6. Bring up the services so "install" actually ends with Freeman
	// running. The user shouldn't need to chase a second command to
	// start the daemon or menu bar — that's the whole point of an
	// install step.
	if err := bringUpServices(); err != nil {
		return fmt.Errorf("bring services up: %w — you can retry with `freeman daemon start` and `freeman menubar start`", err)
	}

	fmt.Fprintf(os.Stderr, `
Installed and running.

  Menu bar icon:  look for 🟢 Horus at the top of your screen
  Log viewer:     http://127.0.0.1:17001/
  Config:         %[1]s/config.yaml

Wake with "Horus". Say "disengage" to stop the voice daemon — click the
menu bar icon (or run 'freeman daemon start') to bring it back.

To remove everything:
  %[1]s/freeman uninstall
`, target)
	return nil
}

// bringUpServices writes both LaunchAgent plists (if missing) and
// loads them. Idempotent: if either is already loaded we kickstart
// instead of bootstrapping twice.
func bringUpServices() error {
	dp, err := resolveDaemonPaths()
	if err != nil {
		return err
	}
	if err := writePlistIfMissing(dp); err != nil {
		return fmt.Errorf("write voice plist: %w", err)
	}
	if launchctlIsLoaded() {
		if err := launchctlKickstart(); err != nil {
			return fmt.Errorf("kickstart voice daemon: %w", err)
		}
	} else if err := launchctlBootstrap(dp.plist); err != nil {
		return fmt.Errorf("bootstrap voice daemon: %w", err)
	}

	mp, err := resolveMenubarPaths()
	if err != nil {
		return err
	}
	if err := writeMenubarPlistIfMissing(mp); err != nil {
		return fmt.Errorf("write menubar plist: %w", err)
	}
	if menubarIsLoaded() {
		if err := menubarKickstart(); err != nil {
			return fmt.Errorf("kickstart menubar: %w", err)
		}
	} else if err := menubarBootstrap(mp.plist); err != nil {
		return fmt.Errorf("bootstrap menubar: %w", err)
	}
	return nil
}

// findSourceRepo walks up from the running binary's directory looking
// for go.mod. We need an actual source checkout — `freeman install` is
// only meaningful when the caller has the code and a Go toolchain.
func findSourceRepo() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("couldn't find go.mod walking up from %s — run `freeman install` from a source checkout", filepath.Dir(exe))
		}
		dir = parent
	}
}

// handleConfig copies src→dst, prompting on overwrite unless
// --overwrite-config is set. Never clobbers a user-tweaked config
// silently.
func handleConfig(src, dst string) error {
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("copy config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  ✓ config.yaml (fresh copy) → %s\n", dst)
		return nil
	}
	if installOverwriteConfig || installYes {
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("overwrite config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  ✓ config.yaml overwritten → %s\n", dst)
		return nil
	}
	if promptYesNo(fmt.Sprintf("Overwrite existing %s with repo config?", dst), false) {
		if err := copyFile(src, dst); err != nil {
			return fmt.Errorf("overwrite config: %w", err)
		}
		fmt.Fprintf(os.Stderr, "  ✓ config.yaml overwritten → %s\n", dst)
	} else {
		fmt.Fprintf(os.Stderr, "  · config.yaml preserved\n")
	}
	return nil
}

// linkIfMissingOrSymlink creates (or refreshes) a symlink dst → src.
// If dst is a non-symlink (real directory), we leave it alone so we
// don't destroy anything the user has put there.
func linkIfMissingOrSymlink(src, dst string) error {
	if _, err := os.Stat(src); err != nil {
		fmt.Fprintf(os.Stderr, "  ! skipping %s (not present in source)\n", filepath.Base(src))
		return nil
	}
	info, err := os.Lstat(dst)
	if err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			// Refresh the symlink in case repo moved.
			if err := os.Remove(dst); err != nil {
				return err
			}
		} else {
			fmt.Fprintf(os.Stderr, "  ! %s exists and is not a symlink — leaving as-is\n", dst)
			return nil
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Symlink(src, dst); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "  ✓ %s → %s\n", filepath.Base(dst), src)
	return nil
}

func copyFile(src, dst string) error {
	return copyFileMode(src, dst, 0o644)
}

func copyFileExecutable(src, dst string) error {
	return copyFileMode(src, dst, 0o755)
}

func copyFileMode(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Create+truncate, not append — we want a full overwrite.
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return os.Chmod(dst, mode)
}

// promptYesNo prints question to stderr and reads a y/n from stdin.
// Returns def if input is empty or stdin is not a terminal (e.g. piped).
func promptYesNo(question string, def bool) bool {
	defStr := "y/N"
	if def {
		defStr = "Y/n"
	}
	fmt.Fprintf(os.Stderr, "%s [%s] ", question, defStr)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "":
		return def
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return def
	}
}
