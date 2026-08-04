#!/usr/bin/env bash
# Rasterise the PWA icons from their SVG sources in web-ui/public/.
#
# The PNGs are committed, so this only needs running when an SVG changes.
# Prefers rsvg-convert (Linux/CI, `brew install librsvg`); falls back to macOS's
# built-in qlmanage so a Mac with nothing installed can still regenerate them.
set -euo pipefail

cd "$(dirname "$0")/../web-ui/public"

render() { # render <src.svg> <size> <dest.png>
  local src=$1 size=$2 dest=$3
  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert -w "$size" -h "$size" "$src" -o "$dest"
  elif command -v qlmanage >/dev/null 2>&1; then
    local tmp
    tmp=$(mktemp -d)
    qlmanage -t -s "$size" -o "$tmp" "$src" >/dev/null 2>&1
    mv "$tmp/$(basename "$src").png" "$dest"
    rm -rf "$tmp"
  else
    echo "need rsvg-convert or qlmanage to rasterise $src" >&2
    exit 1
  fi
  echo "  $dest (${size}px)"
}

echo "rendering icons:"
render icon.svg 192 icon-192.png
render icon.svg 512 icon-512.png
render icon-maskable.svg 192 icon-maskable-192.png
render icon-maskable.svg 512 icon-maskable-512.png
render icon-square.svg 180 apple-touch-icon.png
