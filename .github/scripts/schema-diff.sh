#!/usr/bin/env bash
# Reports how schema.graphql changed against a base commit, and fails on a breaking change that
# no commit in the range acknowledges.
#
#   schema-diff.sh <base-commit>
#
# A breaking change is acknowledged by a trailer in any commit message between the base and
# HEAD:
#
#   API-Break: <why, and what a client has to change>
#
# A trailer rather than a label because this repository has no merge requests to label: work
# lands as feature branches merged by hand, and the commit is the one place the decision travels
# with. Acknowledging a break is not the same as releasing a major — server and GUI share their
# major, and raising it stays a decision taken with the maintainer (see CLAUDE.md).
set -euo pipefail

base=${1:?usage: schema-diff.sh <base-commit>}
file=schema.graphql
inspector=@graphql-inspector/cli@7.0.2
summary=${GITHUB_STEP_SUMMARY:-/dev/null}

if ! git cat-file -e "${base}:${file}" 2>/dev/null; then
  echo "::notice::${base} has no ${file} yet — nothing to compare against."
  echo "No ${file} at the base commit yet; nothing to compare." >>"$summary"
  exit 0
fi

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
git show "${base}:${file}" >"$work/base.graphql"

set +e
NO_COLOR=1 FORCE_COLOR=0 npx --yes "$inspector" diff "$work/base.graphql" "$file" >"$work/out" 2>&1
status=$?
set -e
cat "$work/out"

{
  echo "## Schema changes against \`$(git rev-parse --short "$base")\`"
  echo
  echo '```'
  cat "$work/out"
  echo '```'
} >>"$summary"

if [ "$status" -eq 0 ]; then
  exit 0
fi

# graphql-inspector exits non-zero on a breaking change and on its own failure alike. Only the
# first one can be acknowledged; the second has to stay red.
if ! grep -qi 'breaking change' "$work/out"; then
  echo "::error::graphql-inspector failed without reporting a breaking change — see the log."
  exit "$status"
fi

ack=$(git log --format=%B "${base}..HEAD" | grep -i '^API-Break:' || true)
if [ -n "$ack" ]; then
  echo "::warning::Breaking schema change, acknowledged: ${ack//$'\n'/ | }"
  {
    echo
    echo "**Breaking, acknowledged:**"
    echo
    echo "$ack"
  } >>"$summary"
  exit 0
fi

echo "::error::Breaking schema change. Make it additive (add, then @deprecated, then remove in a later release), or acknowledge it with an 'API-Break: <reason>' trailer in a commit message."
exit 1
