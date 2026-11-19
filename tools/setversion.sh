#!/bin/sh
# Set the version in the one place the build reads it.
#
#   tools/setversion.sh 1.0.1
#
# It writes viva.toml (what viva build/package stamps into the bundle, the
# installer and the package metadata) and main.go's version constant (what
# `cetone --version` prints). Two files, one value: a program that reports a
# different version from the package it came in is a bug report waiting to happen.
set -eu

if [ $# -ne 1 ]; then
	echo "usage: tools/setversion.sh <version without the leading v>" >&2
	exit 2
fi
version=${1#v}
case "$version" in
*[!0-9.]* | "")
	echo "not a version: $1" >&2
	exit 2
	;;
esac

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

# BSD sed (macOS) wants an argument after -i where GNU sed does not, so the two
# are told apart rather than assumed: this script runs on both, because the
# release workflow builds on Linux and macOS.
if sed --version >/dev/null 2>&1; then
	edit() { sed -i "$1" "$2"; }
else
	edit() { sed -i '' "$1" "$2"; }
fi

edit "s/^version = .*/version = \"$version\"/" "$root/viva.toml"
edit "s/^const version = .*/const version = \"$version\"/" "$root/main.go"

grep -H 'version' "$root/viva.toml" | head -1
grep -H '^const version' "$root/main.go"
