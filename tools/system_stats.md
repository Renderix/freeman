---
name: system_stats
description: Get the user's current system resource usage — CPU load, memory pressure, and root disk usage. Use when the user asks about system performance, CPU, memory, disk, or whether the machine is busy.
runtime: shell
timeout_ms: 4000
parameters:
  type: object
  properties: {}
  required: []
---
set -euo pipefail
echo "=== CPU ==="
top -l 1 -n 0 | awk '/CPU usage/{print; exit}'
echo
echo "=== Memory ==="
top -l 1 -n 0 | awk '/PhysMem/{print; exit}'
echo
echo "=== Load averages ==="
uptime | sed 's/.*load averages: //'
echo
echo "=== Disk (root) ==="
df -h / | awk 'NR==2 {printf "used=%s free=%s cap=%s\n", $3, $4, $5}'
