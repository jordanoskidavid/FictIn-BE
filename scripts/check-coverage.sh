#!/usr/bin/env sh
# Usage: scripts/check-coverage.sh <coverage profile> <minimum percent>
# Fails when the total reported by `go tool cover -func` is below the minimum.
set -eu
profile=${1:?coverage profile required}
min=${2:?minimum percent required}

total=$(go tool cover -func="$profile" | awk '/^total:/ { sub("%", "", $NF); print $NF }')
if [ -z "$total" ]; then
  echo "could not read total coverage from $profile" >&2
  exit 1
fi

echo "total coverage: ${total}% (minimum: ${min}%)"
if awk -v t="$total" -v m="$min" 'BEGIN { exit !(t + 0 < m + 0) }'; then
  echo "coverage ${total}% is below the required ${min}%" >&2
  exit 1
fi
