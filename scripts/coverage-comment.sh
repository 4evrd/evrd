#!/bin/sh
# Writes the coverage comment for a pull request (markdown, on stdout), and
# lists what breaks the rules in $DROPPED_FILE (one reason per line; no file
# written when nothing does).
#
#   scripts/coverage-comment.sh <pr-dir> [<base-dir>]
#
# <pr-dir> has the pull request's reports: coverage.out (the unit tests'
# profile), scylla.cover (the ScyllaDB job's, if there) and coverage.txt
# (node --test's coverage). <base-dir>, when there is one, has main's. Go
# figures come from both profiles merged: a block counts as covered if
# either run covered it.
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

# merged <dir>: "block statements covered(0/1)" per code block, from the
# dir's profiles together
merged() {
	cat "$1/coverage.out" "$1/scylla.cover" 2>/dev/null | awk '$1 != "mode:" && NF == 3 { n[$1] = $2; if ($3 > 0) hit[$1] = 1 }
		END { for (b in n) print b, n[b], (b in hit) ? 1 : 0 }'
}
# total coverage from the merged blocks (straight from the profiles: go tool
# cover would need the source of every package — main's may have ones this
# PR removed)
total() {
	merged "$1" | awk '{ n += $2; if ($3) c += $2 } END { if (n) printf "%.1f", 100 * c / n; else print "0.0" }'
}
# "package percent" per line, from the merged blocks
go_parts() {
	merged "$1" | awk '{ f = $1; sub(/:.*/, "", f); sub(/\/[^\/]*$/, "", f); sub("github.com/4evrd/evrd/", "", f)
		n[f] += $2; if ($3) c[f] += $2 }
		END { for (p in n) if (n[p]) printf "%s %.1f\n", p, 100 * c[p] / n[p] }' | sort
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
	awk -v min="$min" -v kind="$kind" -v fails="$fails" -v slack="${COVERAGE_SLACK:-1}" '
		NR == FNR { b[$1] = $2; next }
		{
			p = $1; c = $2 + 0; has = (p in b); base = b[p] + 0
			need = min; tol = slack # per part: the minimum itself gets none
			if (has && base < min) need = base; else tol = 0.05
			if (c + 0.05 >= min) status = "✅"
			# a point of slack against main: concurrent code covers a little
			# differently from run to run
			else if (c + tol < need) {
				status = "❌"
				if (!has) why = "a new " kind " needs " min "%"
				else if (base < min) why = "main has " base "%"
				else why = "the minimum is " min "%"
				print kind " " p ": " c "% (" why ")" >> fails
			} else status = sprintf("⬆️ %.1f to go", min - c)
			printf "| %s | %s | %s%% | %s |\n", p, (has ? base "%" : "new"), c, status
		}' "$3" "$2"
}

pr_total=$(total "$pr")
go_parts "$pr" >"$fails.go"
web_parts "$pr/coverage.txt" >"$fails.web"
: >"$fails.gobase"
: >"$fails.webbase"
if [ -n "$base" ] && [ -f "$base/coverage.out" ]; then
	go_parts "$base" >"$fails.gobase"
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
	echo "Go overall: **${pr_total}%** (main: $(total "$base")%)."
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
echo "_Go: the unit tests and the ScyllaDB job together (e2e isn't counted). Profiles: the run's \`coverage-go\` and \`coverage-scylla\` artifacts._"

if [ -s "$fails" ] && [ -n "${DROPPED_FILE:-}" ]; then
	cp "$fails" "$DROPPED_FILE"
fi
