#!/usr/bin/env bash
# Assembles the loadable extension directories from shared/.
#
# The code is written once and differs between browsers in two places only: the
# manifest, and how the background is started - Firefox runs a list of scripts as
# an event page, Chrome runs one file as a service worker. Keeping two hand-made
# copies of two thousand lines to express that would be the wrong trade.
#
# Chrome and Chromium get the same directory. Chromium is the base Chrome is
# built on; for an extension there is nothing between them.
set -euo pipefail
cd "$(dirname "$0")"

for target in firefox chrome; do
    rm -rf "$target"
    mkdir -p "$target"
    cp shared/*.js shared/*.html "$target"/
    cp "manifest.$target.json" "$target/manifest.json"
done

# The service worker is Chrome's alone, the manifest key that names it is absent
# in Firefox, and an unused file in a signed add-on is a question at review time.
rm -f firefox/sw.js

echo "built:"
echo "  firefox/  -> about:debugging  ·  Load Temporary Add-on  ·  firefox/manifest.json"
echo "  chrome/   -> chrome://extensions  ·  Developer mode  ·  Load unpacked  ·  chrome/"
