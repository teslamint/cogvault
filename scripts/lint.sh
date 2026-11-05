#!/bin/sh
# Shared local/CI lint: pinned gosec over a fixed rule set. `make lint` and the
# CI lint step both run this script, so they cannot drift apart.
#
# Rules: G201/G202 SQL built by formatting/concatenation, G301 directory modes,
# G302 file open/chmod modes, G306 WriteFile modes. A justified exception is a
# narrow `#nosec Gxxx -- reason` on the flagged line.
#
# Exit codes: 0 clean, 1 gosec findings, 2 gosec could not be installed
# (module proxy/network), so a network failure is never read as a finding.
set -eu

GOSEC_VERSION=v2.30.0
GOSEC_RULES=G201,G202,G301,G302,G306

cd "$(dirname "$0")/.."
bin=$(mktemp -d)
trap 'rm -rf "$bin"' EXIT HUP INT TERM

if ! GOBIN="$bin" go install "github.com/securego/gosec/v2/cmd/gosec@${GOSEC_VERSION}"; then
	echo "lint: could not install gosec ${GOSEC_VERSION} (module proxy or network?); no scan ran" >&2
	exit 2
fi

"$bin/gosec" -quiet -include="${GOSEC_RULES}" ./...
