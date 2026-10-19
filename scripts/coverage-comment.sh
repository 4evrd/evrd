#!/bin/sh
# Writes the coverage comment for a pull request (markdown, on stdout).
#
#   scripts/coverage-comment.sh <pr-dir> [<base-dir>]
#
# <pr-dir> has the pull request's reports: test.out (go test's output, with
# per-package "coverage: N%"), coverage.out (the profile) and coverage.txt
# (node --test's coverage). <base-dir>, when there is one, has main's
# test.out and coverage.out, to compare against.
set -eu
pr=$1
base=${2:-}

# total coverage straight from a profile (go tool cover would need the
# source of every package in it — main's may have ones this PR removed)
total() {
	awk 'NR > 1 && NF == 3 { if (!seen[$1]++) n += $2; if ($3 > 0 && !hit[$1]++) c += $2 }
		END { if (n) printf "%.1f", 100 * c / n; else print "0.0" }' "$1"
}
# "package percent" per line, from go test's output
packages() {
	awk '/coverage: [0-9]/ { p = ""; for (i = 1; i <= NF; i++) { if ($i ~ /^github\.com\/4evrd\/evrd/) p = $i; if ($i == "coverage:") c = $(i+1) }
		if (p != "") { sub("github.com/4evrd/evrd/", "", p); sub("%", "", c); print p, c } }' "$1" | sort
}
delta() { awk -v a="$1" -v b="$2" 'BEGIN { d = a - b; if (d > 0.05) printf "+%.1f", d; else if (d < -0.05) printf "%.1f", d; else printf "±0" }'; }

echo '<!-- evrd-coverage -->'
pr_total=$(total "$pr/coverage.out")
if [ -n "$base" ] && [ -f "$base/coverage.out" ]; then
	base_total=$(total "$base/coverage.out")
	d=$(delta "$pr_total" "$base_total")
	icon="🟰"
	case $d in +*) icon="📈" ;; -*) icon="📉" ;; esac
	echo "### $icon Go coverage: **${pr_total}%** ($d vs main's ${base_total}%)"
else
	echo "### Go coverage: **${pr_total}%**"
fi
echo

if [ -n "$base" ] && [ -f "$base/test.out" ]; then
	packages "$base/test.out" >/tmp/cov.base.$$
	packages "$pr/test.out" >/tmp/cov.pr.$$
	# packages whose coverage changed, and new ones
	changes=$(awk 'NR == FNR { b[$1] = $2; next }
		{ if (!($1 in b)) printf "| %s | — | %s%% | new |\n", $1, $2
		  else { d = $2 - b[$1]; if (d > 0.05 || d < -0.05) printf "| %s | %s%% | %s%% | %+.1f |\n", $1, b[$1], $2, d } }' /tmp/cov.base.$$ /tmp/cov.pr.$$)
	rm -f /tmp/cov.base.$$ /tmp/cov.pr.$$
	if [ -n "$changes" ]; then
		echo "| Package | main | this PR | Δ |"
		echo "|---|---:|---:|---:|"
		echo "$changes"
	else
		echo "No package's coverage changed."
	fi
else
	echo "| Package | Coverage |"
	echo "|---|---:|"
	packages "$pr/test.out" | awk '{ printf "| %s | %s%% |\n", $1, $2 }'
	echo
	echo "_No coverage from main to compare with yet._"
fi
echo

if [ -f "$pr/coverage.txt" ]; then
	web=$(grep -E "all files" "$pr/coverage.txt" | head -1 | awk -F'|' '{ gsub(/ /, "", $2); print $2 }')
	echo "**Web unit tests:** ${web:-?}% of lines (\`web/src/lib\`)."
	echo
fi

echo "<details><summary>All packages</summary>"
echo
echo "| Package | Coverage |"
echo "|---|---:|"
packages "$pr/test.out" | awk '{ printf "| %s | %s%% |\n", $1, $2 }'
echo
echo "</details>"
echo
echo "_Go unit tests only (the ScyllaDB tests and e2e aren't counted). The full HTML report is the run's \`coverage-go\` artifact._"
