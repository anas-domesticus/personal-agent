#!/usr/bin/env bash
# Runs govulncheck and fails on any vulnerability our code reaches, except
# those listed in .govulncheck-ignore (one ID per line, then a reason).
# Run by `task vuln` and `task check`.
set -euo pipefail
cd "$(dirname "$0")/.."

ignored=$(grep -oE '^GO-[0-9]+-[0-9]+' .govulncheck-ignore 2>/dev/null || true)
# A finding whose trace reaches a function is one our code calls.
found=$(govulncheck -format json ./... |
	jq -r 'select(.finding != null and .finding.trace[0].function != null) | .finding.osv' |
	sort -u)

fail=0
for id in $found; do
	if grep -qx "$id" <<<"$ignored"; then
		echo "ignored (see .govulncheck-ignore): $id"
	else
		echo "vulnerable: $id  https://pkg.go.dev/vuln/$id"
		fail=1
	fi
done
for id in $ignored; do
	grep -qx "$id" <<<"$found" || echo "stale ignore, remove it: $id"
done
[[ $fail -eq 0 ]] || echo "Fix by upgrading (govulncheck ./... for traces), or add the ID with a reason to .govulncheck-ignore."
exit $fail
