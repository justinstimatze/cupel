#!/usr/bin/env bash
# Generate web/public/og-card.png — the 1200x630 social-share preview that
# appears when a cupel link is pasted into Twitter / Bluesky / LinkedIn /
# Discord / Slack. Direct ImageMagick generation (no SVG intermediate)
# with explicit font paths, because SVG→PNG via librsvg's font resolver
# was inconsistent (initial release shipped with the headline glyph
# replaced and word-spacing collapsed).
#
# Requires: ImageMagick `magick` + DejaVu fonts (Debian: fonts-dejavu).

set -euo pipefail

ROOT="$(git rev-parse --show-toplevel)"
OUT="$ROOT/web/public/og-card.png"
SERIF_BOLD=/usr/share/fonts/truetype/dejavu/DejaVuSerif-Bold.ttf
SERIF=/usr/share/fonts/truetype/dejavu/DejaVuSerif.ttf
SANS=/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf

magick -size 1200x630 xc:'#16130f' \
    -fill '#322a20' -strokewidth 2 -stroke '#322a20' -draw 'roundrectangle 60,60 1140,570 14,14' \
    -font "$SERIF_BOLD" -pointsize 148 -fill '#c9a45e' -stroke none -annotate +100+260 'cupel' \
    -font "$SERIF" -pointsize 36 -fill '#ece3d4' -annotate +100+360 'The wish-fulfillment engines stories run on.' \
    -fill '#d7c9b1' -annotate +100+410 'The dark-twin grifts that wear their face.' \
    -fill '#c9a45e' -strokewidth 3 -stroke '#c9a45e' -draw 'line 100,475 240,475' \
    -font "$SANS" -pointsize 22 -fill '#9c8f7a' -stroke none -annotate +100+530 'justinstimatze.github.io/cupel' \
    "$OUT"

echo "wrote $OUT"
