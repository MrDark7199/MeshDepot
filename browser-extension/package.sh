#!/usr/bin/env bash
# Builds the two store packages from the assembled directories.
#
# Python does the zipping rather than the zip command, which is not installed
# everywhere; the archive has to have manifest.json at its root, which is why
# the directory is entered instead of being named on the command line.
set -euo pipefail
cd "$(dirname "$0")"

./build.sh >/dev/null
mkdir -p dist

for target in firefox chrome; do
    archive="dist/meshdepot-importer-$target.zip"
    rm -f "$archive"
    python3 -c "
import shutil, sys
shutil.make_archive(sys.argv[1], 'zip', sys.argv[2])" "dist/meshdepot-importer-$target" "$target"
    echo "  $archive  ($(du -h "$archive" | cut -f1))"
done

echo "upload:"
echo "  firefox -> addons.mozilla.org  ·  Developer Hub  ·  Submit a New Add-on"
echo "  chrome  -> chrome.google.com/webstore/devconsole  ·  New Item"
