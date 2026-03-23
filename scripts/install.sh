#!/usr/bin/env bash
set -euo pipefail

target="$(realpath "$(brew --prefix cupa)/bin/cupa")"
tmp="$(mktemp)"
trap 'rm -f "$tmp"' EXIT

go build -o "$tmp" .
install -m 755 "$tmp" "$target"

echo "Installed to $target"
