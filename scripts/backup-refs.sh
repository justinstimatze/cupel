#!/usr/bin/env bash
# Back up the gitignored refs/ — local working materials that live outside
# the published repo — to a home-dir tarball.
#
# The logic lives here, in a TRACKED script, rather than buried in the untracked
# .git/hooks/pre-commit — so it survives a reclone and is runnable standalone.
#
# Cheap when current: `find -newer` stops at the first refs/ file newer than the
# backup (-print -quit), so it only re-tars when refs/ has actually changed.
# Write is atomic: tar to .tmp, then mv.
#
# Usage:  scripts/backup-refs.sh        (also invoked by .git/hooks/pre-commit)
set -euo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
BACKUP_FILE="$HOME/cupel-refs-backup.tgz"

if [[ ! -d "$REPO_ROOT/refs" ]]; then
    echo "backup-refs: no refs/ — nothing to back up"
    exit 0
fi

if [[ ! -f "$BACKUP_FILE" ]] || [[ -n "$(find "$REPO_ROOT/refs" -newer "$BACKUP_FILE" -print -quit 2>/dev/null)" ]]; then
    echo "backup-refs: refs/ has new content — refreshing $BACKUP_FILE"
    tar -czf "$BACKUP_FILE.tmp" -C "$REPO_ROOT" refs/ && mv "$BACKUP_FILE.tmp" "$BACKUP_FILE"
    echo "backup-refs: refs backup refreshed ($(du -h "$BACKUP_FILE" | cut -f1))"
else
    echo "backup-refs: $BACKUP_FILE is current — skipping"
fi
