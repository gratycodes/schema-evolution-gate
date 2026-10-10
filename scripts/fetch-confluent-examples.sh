#!/usr/bin/env bash
# Download Confluent Schema Registry's JSON Schema diff examples, pinned to a
# commit, into testdata/confluent/ for the cross-check test. They are not
# committed here because they are under the Confluent Community License.
set -euo pipefail
REF="${CONFLUENT_REF:-9d2a64f4857c0302ad1e3b81545353e77cd9115b}"
BASE="https://raw.githubusercontent.com/confluentinc/schema-registry/${REF}/json-schema-provider/src/test/resources"
DEST="$(cd "$(dirname "$0")/.." && pwd)/testdata/confluent"
mkdir -p "$DEST"
for f in diff-schema-examples.json diff-combined-schema-examples.json; do
  curl -fsSL --retry 3 "$BASE/$f" -o "$DEST/$f"
done
echo "downloaded Confluent examples @ ${REF:0:12} to $DEST"
