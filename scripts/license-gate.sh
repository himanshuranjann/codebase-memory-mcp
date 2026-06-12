#!/usr/bin/env bash
set -euo pipefail

# CI license-compliance gate (runs in the security workflow for both dry
# runs and releases). Two layers:
#   1) Structural: every vendored component directory must physically carry
#      a license file.
#   2) ScanCode Toolkit license detection over every vendored license text
#      and all first-party sources; ONE detection outside
#      scripts/license-policy.json fails the gate (see license-gate-check.py).
#
# Grammar parser bodies (generated parser.c, no headers) are excluded from
# the ScanCode pass for runtime; their per-directory license files ARE
# scanned, and layer 1 guarantees each grammar carries one.
#
# Requires: scancode (pipx install scancode-toolkit), python3.

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "=== License gate 1/2: structural coverage ==="
MISS=0
for d in vendored/*/ \
    internal/cbm/vendored/lz4 internal/cbm/vendored/simplecpp \
    internal/cbm/vendored/ts_runtime internal/cbm/vendored/verstable \
    internal/cbm/vendored/wyhash internal/cbm/vendored/zstd \
    internal/cbm/vendored/common internal/cbm/vendored/common/tree_sitter \
    internal/cbm/vendored/grammars/*/; do
    [ -d "$d" ] || continue
    if ! ls "$d" | grep -qiE '^(LICENSE|LICENCE|COPYING|UNLICENSE|NOTICE)'; then
        echo "BLOCKED: no license file in $d"
        MISS=1
    fi
done
if [ $MISS -ne 0 ]; then
    echo "=== LICENSE GATE FAILED (structural) ==="
    exit 1
fi
echo "OK: every vendored component directory carries a license file"

echo "=== License gate 2/2: ScanCode detection ==="
if ! command -v scancode &>/dev/null; then
    echo "FAIL: scancode not installed (pipx install scancode-toolkit)"
    exit 1
fi

STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

# Inputs: all vendored license/notice texts + all first-party sources.
find vendored internal/cbm/vendored -type f \
    \( -iname 'LICENSE*' -o -iname 'COPYING*' -o -iname 'NOTICE*' -o -iname 'UNLICENSE*' \) \
    > "$STAGE/files.txt"
find src pkg scripts -type f \
    \( -name '*.c' -o -name '*.h' -o -name '*.sh' -o -name '*.js' \
    -o -name '*.py' -o -name '*.rb' -o -name '*.toml' -o -name '*.json' \) \
    >> "$STAGE/files.txt"

mkdir -p "$STAGE/tree"
tar cf - -T "$STAGE/files.txt" | tar xf - -C "$STAGE/tree"

scancode --license --quiet --processes 2 --json-pp "$STAGE/scan.json" "$STAGE/tree" \
    > "$STAGE/scancode.log" 2>&1 || {
    echo "FAIL: scancode run failed:"
    tail -20 "$STAGE/scancode.log"
    exit 1
}

python3 scripts/license-gate-check.py "$STAGE/scan.json" scripts/license-policy.json
echo "=== License gate passed ==="
