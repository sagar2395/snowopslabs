# SPDX-License-Identifier: Apache-2.0
"""Fail when a gosec rule exclusion has no comment explaining it.

An exclusion with no reason is a silenced finding nobody triaged. One comment
may cover several entries — G301/G302/G306 share a reason — so an entry counts
as justified when the comment block above its group names that rule id.
"""

import sys

lines = open(sys.argv[1]).read().split("\n")
start = next(i for i, l in enumerate(lines) if l.strip() == "excludes:")

reason, unjustified = "", []
for line in lines[start + 1:]:
    stripped = line.strip()
    if not stripped:
        continue
    if stripped.startswith("#"):
        reason += " " + stripped.lstrip("# ")
        continue
    if not stripped.startswith("- G"):
        break
    rule = stripped[2:]
    if rule not in reason:
        unjustified.append(rule)

for rule in unjustified:
    print(f"gosec exclusion {rule} has no comment naming it", file=sys.stderr)
sys.exit(1 if unjustified else 0)
