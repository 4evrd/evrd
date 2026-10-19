#!/bin/sh
# Writes the coverage comment for a pull request (markdown, on stdout), and
# lists what breaks the rules in $DROPPED_FILE (one reason per line; no file
# written when nothing does).
#
#   scripts/coverage-comment.sh <pr-dir> [<base-dir>]
#
# <pr-dir> has the pull request's reports: test.out (go test's output, with
# per-package "coverage: N%"), coverage.out (the profile) and coverage.txt
# (node --test's coverage). <base-dir>, when there is one, has main's.
#
# The rule, for every Go package and every web file: at least $COVERAGE_MIN
# (80) percent. Until one gets there it may not drop below main's figure, so
# it can only climb; a new one must start at the minimum.
set -eu
pr=$1
base=${2:-}
min=${COVERAGE_MIN:-80}
fails=$(mktemp)
trap 'rm -f "$fails" "$fails".*' EXIT

# total coverage straight from a profile (go tool cover would need the
# source of every package in it — main's may have ones this PR removed)
total() {
	awk 'NR > 1 && NF == 3 { if (!seen[$1]++) n += $2; if ($3 > 0 && !hit[$1]++) c += $2 }
		END { if (n) printf "%.1f", 100 * c / n; else print "0.0" }' "$1"
}
# "package percent" per line, from go test's output
go_parts() {
	awk '/coverage: [0-9]/ { p = ""; for (i = 1; i <= NF; i++) { if ($i ~ /^github\.com\/4evrd\/evrd/) p = $i; if ($i == "coverage:") c = $(i+1) }
		if (p != "") { sub("github.com/4evrd/evrd/", "", p); sub("%", "", c); print p, c } }' "$1" | sort
}
# "file percent" per line, from node --test's coverage table (lines column)
web_parts() {
	[ -f "$1" ] || return 0
	sed -E 's/^(ℹ|#) //' "$1" | awk -F'|' 'NF >= 4 && $1 ~ /\.(js|svelte)[[:space:]]*$/ { f = $1; gsub(/ /, "", f); l = $2; gsub(/ /, "", l); print f, l }' | sort
}

# judge <kind> <pr parts> <base parts>: one table row per part, with what
# main has and a status; failures go to $fails.
judge() {
	kind=$1
	awk -v min="$min" -v kind="$kind" -v fails="$fails" '
		NR == FNR { b[$1] = $2; next }
		{
			p = $1; c = $2 + 0; has = (p in b); base = b[p] + 0
			need = min; if (has && base < min) need = base
			if (c + 0.05 >= min) status = "✅"
			else if (c + 0.1 < need) {
				status = "❌"
				if (!has) why = "a new " kind " needs " min "%"
				else if (base < min) why = "main has " base "%"
				else why = "the minimum is " min "%"
				print kind " " p ": " c "% (" why ")" >> fails
			} else status = sprintf("⬆️ %.1f to go", min - c)
			printf "| %s | %s | %s%% | %s |\n", p, (has ? base "%" : "new"), c, status
		}' "$3" "$2"
}

pr_total=$(total "$pr/coverage.out")
go_parts "$pr/test.out" >"$fails.go"
web_parts "$pr/coverage.txt" >"$fails.web"
: >"$fails.gobase"
: >"$fails.webbase"
if [ -n "$base" ] && [ -f "$base/test.out" ]; then
	go_parts "$base/test.out" >"$fails.gobase"
	web_parts "$base/coverage.txt" >"$fails.webbase"
fi

gorows=$(judge package "$fails.go" "$fails.gobase")
webrows=$(judge file "$fails.web" "$fails.webbase")
under=$(printf '%s\n%s\n' "$gorows" "$webrows" | grep -c "to go\|❌" || true)
parts=$(printf '%s\n%s\n' "$gorows" "$webrows" | grep -c "^|" || true)

echo '<!-- evrd-coverage -->'
if [ -s "$fails" ]; then
	echo "### ❌ Coverage: some parts fall below what's required"
	echo
	sed 's/^/- /' "$fails"
	echo
	echo "Add tests for them — the coverage check fails until then."
elif [ "$under" -eq 0 ]; then
	echo "### ✅ Coverage: every part at ${min}% or more"
else
	echo "### ⬆️ Coverage: nothing dropped; $under of $parts parts still under ${min}%"
fi
echo
if [ -n "$base" ] && [ -f "$base/coverage.out" ]; then
	echo "Go overall: **${pr_total}%** (main: $(total "$base/coverage.out")%)."
else
	echo "Go overall: **${pr_total}%**. _No report from main to compare with yet._"
fi
echo
echo "Every Go package and web file needs at least **${min}%**. One still under it can't go lower than main has it, so it only climbs."
echo
echo "| Go package | main | this PR | |"
echo "|---|---:|---:|---|"
echo "$gorows"
if [ -n "$webrows" ]; then
	echo
	echo "| Web file (unit tests) | main | this PR | |"
	echo "|---|---:|---:|---|"
	echo "$webrows"
fi
echo
echo "_Go: unit tests (the ScyllaDB tests and e2e aren't counted). Full HTML report: the run's \`coverage-go\` artifact._"

if [ -s "$fails" ] && [ -n "${DROPPED_FILE:-}" ]; then
	cp "$fails" "$DROPPED_FILE"
fi
