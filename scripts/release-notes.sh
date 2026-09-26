#!/bin/sh
# Prints the release notes of one version for GitHub: the banner, the
# version's summary in English and Russian and its changes, taken from
# CHANGELOG.md and CHANGELOG.ru.md. goreleaser appends the install guide
# from .github/release-install.md.
# Usage: scripts/release-notes.sh 0.3.0 [previous tag]
set -eu

VERSION=${1#v}
REPO=$(cd "$(dirname "$0")/.." && pwd)
URL=https://github.com/ArthurKrantsevich/tgsync

# section FILE prints the body of "## [VERSION]" up to the next "## [".
section() {
	awk -v v="## [$VERSION]" '
		index($0, v) == 1 { on = 1; next }
		on && /^## \[/ { exit }
		on { print }
	' "$1"
}

# summary prints the first paragraph of a section; changes prints the rest.
summary() { section "$1" | awk 'NF { on = 1 } on && !NF { exit } on { print }'; }
changes() { section "$1" | awk 'NF { seen = 1 } seen && !NF && !gap { gap = 1; next } gap { print }'; }

EN=$(summary "$REPO/CHANGELOG.md")
RU=$(summary "$REPO/CHANGELOG.ru.md")
if [ -z "$EN" ] || [ -z "$RU" ]; then
	echo "release-notes: no [$VERSION] section in CHANGELOG.md or CHANGELOG.ru.md" >&2
	exit 1
fi
PREV=${2:-$(git -C "$REPO" describe --tags --abbrev=0 "v$VERSION^" 2>/dev/null || true)}
DATE=$(awk -v v="## [$VERSION] - " 'index($0, v) == 1 { print substr($0, length(v) + 1); exit }' "$REPO/CHANGELOG.md")
ANCHOR=$(echo "$VERSION" | tr -d .)---$DATE

cat <<EOF
<p align="center"><img src="https://raw.githubusercontent.com/ArthurKrantsevich/tgsync/main/docs/assets/banner.svg" width="100%"></p>

**tgsync $VERSION** — $EN

**tgsync $VERSION** — $RU

## What is new in $VERSION
EOF
changes "$REPO/CHANGELOG.md"
echo
printf 'По-русски: [CHANGELOG.ru.md](%s/blob/main/CHANGELOG.ru.md#%s)' "$URL" "$ANCHOR"
if [ -n "$PREV" ]; then
	printf ' · Full diff: [%s...v%s](%s/compare/%s...v%s)' "$PREV" "$VERSION" "$URL" "$PREV" "$VERSION"
fi
printf '\n\n---\n\n'
