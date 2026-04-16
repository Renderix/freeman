#!/usr/bin/env bash
# Launch pi-coding-agent's interactive TUI so you can run `/login anthropic`
# and complete the OAuth handshake that pm-sidecar.ts relies on.
#
# Why the gymnastics: the pi CLI has a `#!/usr/bin/env node` shebang, which
# on systems with Node < 20 fails because pi-tui uses the regex `v` flag.
# Forcing execution through `bun` bypasses the shebang and picks up bun's
# JS runtime instead.
#
# Usage:
#   ./scripts/pi_login.sh
#
# Inside the TUI:
#   /login
#   (select anthropic, complete browser OAuth)
#   /quit
#
# After success, ~/.pi/agent/auth.json will contain an anthropic credential
# that pm-sidecar.ts uses automatically.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SIDECAR_DIR="${REPO_ROOT}/sidecar"
PI_CLI="${SIDECAR_DIR}/node_modules/@mariozechner/pi-coding-agent/dist/cli.js"

if [[ ! -f "${PI_CLI}" ]]; then
	echo "pi CLI not found at ${PI_CLI}" >&2
	echo "run 'cd sidecar && bun install' first" >&2
	exit 1
fi

if ! command -v bun >/dev/null 2>&1; then
	echo "bun not found on PATH; install bun from https://bun.sh" >&2
	exit 1
fi

cd "${SIDECAR_DIR}"
exec bun "${PI_CLI}" "$@"
