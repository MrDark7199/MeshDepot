# Microsoft Core Fonts for the Web

The eleven self-extracting archives in this directory are the original
"Core fonts for the Web" packages. They are here so the image build does not
depend on SourceForge being reachable: `ttf-mscorefonts-installer` downloads
them at build time, which turns every rebuild into a bet on a third party.

## Why the fonts are needed at all

The Firefox resolver presents itself as Chrome on Windows 10. Without these
fonts, `fc-match` resolved Verdana, Georgia, Tahoma, Trebuchet MS, Impact and
Segoe UI to WenQuanYi Zen Hei — a CJK face whose metrics look nothing like the
Windows ones. A script that measures rendered text width sees the mismatch
between the claimed platform and the real one, which is what anti-bot checks
score against.

## Licence

Redistribution is permitted **in this original, unmodified form only**. The
EULA is inside each archive. This is why the `.exe` files are committed rather
than the extracted `.ttf` files: extracting and redistributing those would not
be covered.

Do not repack, rename or modify these files.

## Provenance and verification

Downloaded from `https://downloads.sourceforge.net/corefonts/`, the same source
Debian and Ubuntu use. Every file was checked against the SHA256 list that
`ttf-mscorefonts-installer` ships in
`/usr/share/package-data-downloads/ttf-mscorefonts-installer`.

`sha256sums` holds those hashes. The image build verifies against it before
extracting, so a corrupted or swapped archive fails the build rather than
silently producing a different font set.

## Updating

These fonts have not changed since 2002 and there is nothing to update. If a
file ever has to be replaced, fetch it from the URL above, check it against the
Debian package's hash list, and regenerate `sha256sums` with `sha256sum *.exe`.
