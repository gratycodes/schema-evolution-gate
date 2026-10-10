#!/usr/bin/env bash
# Runs schema-evolution-gate for the composite GitHub Action: writes the job
# summary and outputs, creates or updates exactly one PR comment (found by its
# marker), then exits non-zero on breaking changes so the comment is always
# posted before the step fails.
set -euo pipefail

marker='<!-- schema-evolution-gate -->'
md="$RUNNER_TEMP/schema-evolution-gate.md"
json="$RUNNER_TEMP/schema-evolution-gate.json"

args=(check --config "$SEG_CONFIG" --markdown-out "$md" --json-out "$json")
[[ -n "${SEG_BASE:-}" ]] && args+=(--base "$SEG_BASE")
if [[ -n "${SEG_EXTRA:-}" ]]; then
  read -r -a extra <<<"$SEG_EXTRA"
  args+=("${extra[@]}")
fi

set +e
"$SEG_BIN" "${args[@]}"
code=$?
set -e
if [[ $code -ne 0 && $code -ne 1 ]]; then
  echo "::error::schema-evolution-gate could not run (exit $code); see the log above"
  exit "$code"
fi

cat "$md" >>"$GITHUB_STEP_SUMMARY"
{
  echo "breaking=$(jq '.breaking' "$json")"
  echo "failed-topics=$(jq '.failedTopics' "$json")"
  echo "report-json=$json"
  echo "report-markdown=$md"
} >>"$GITHUB_OUTPUT"

pr=$(jq -r '.pull_request.number // empty' "$GITHUB_EVENT_PATH")
if [[ "$SEG_COMMENT" == "true" && -n "$pr" ]]; then
  api="repos/$GITHUB_REPOSITORY/issues"
  existing=$(gh api --paginate "$api/$pr/comments" \
    --jq ".[] | select(.body | startswith(\"$marker\")) | .id" | head -n1 || true)
  if [[ -n "$existing" ]]; then
    gh api -X PATCH "$api/comments/$existing" -F "body=@$md" >/dev/null &&
      echo "Updated PR comment $existing" ||
      echo "::warning::could not update the PR comment (fork PRs get a read-only token)"
  else
    gh api -X POST "$api/$pr/comments" -F "body=@$md" >/dev/null &&
      echo "Posted PR comment" ||
      echo "::warning::could not post a PR comment (fork PRs get a read-only token)"
  fi
fi

if [[ $code -eq 1 ]]; then
  msg="breaking schema changes: $(jq '.breaking' "$json") breaking, $(jq '.failedTopics' "$json") topic(s) failing"
  if [[ "$SEG_FAIL" == "true" ]]; then
    echo "::error::$msg"
    exit 1
  fi
  echo "::warning::$msg (fail-on-breaking is off)"
fi
exit 0
