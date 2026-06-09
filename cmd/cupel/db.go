package main

// db.go — SQLite-backed structural index for the cupel catalog. The migration
// is one-shot via `cupel db-sync` (which reads each works/*.md, theory/*.md,
// theory/cluster-catalog.md, and theory/glossary.md, populates the DB). Once
// the renderer is rewired (Phase A part 2), the DB becomes the source of truth
// for structural data and the MD files carry only prose bodies.
//
// Schema is intentionally narrow this round — works + cross-refs + theory docs.
// Engines + clusters + glossary stay parsed from markdown until Phase B.

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"
)

const dbSchemaSQL = `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS works (
	slug         TEXT PRIMARY KEY,
	work         TEXT NOT NULL,
	author       TEXT NOT NULL,
	year         TEXT NOT NULL,
	medium       TEXT NOT NULL,
	backing      TEXT NOT NULL,
	source       TEXT,
	layer        TEXT,
	verified     INTEGER,
	author_note  TEXT,
	translator   TEXT,
	body         TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS work_engines (
	work_slug    TEXT NOT NULL REFERENCES works(slug) ON DELETE CASCADE,
	engine_name  TEXT NOT NULL,
	ordinal      INTEGER NOT NULL,
	PRIMARY KEY (work_slug, ordinal)
) STRICT;
CREATE INDEX IF NOT EXISTS work_engines_engine ON work_engines(engine_name);

CREATE TABLE IF NOT EXISTS related_refs (
	from_slug    TEXT NOT NULL REFERENCES works(slug) ON DELETE CASCADE,
	kind         TEXT NOT NULL CHECK (kind IN ('work', 'theory', 'pending')),
	to_slug      TEXT NOT NULL,
	gloss        TEXT NOT NULL,
	ordinal      INTEGER NOT NULL,
	PRIMARY KEY (from_slug, ordinal)
) STRICT;
CREATE INDEX IF NOT EXISTS related_refs_to ON related_refs(to_slug, kind);

CREATE TABLE IF NOT EXISTS theory_docs (
	slug         TEXT PRIMARY KEY,
	title        TEXT NOT NULL,
	body         TEXT NOT NULL
) STRICT;
`

// openDB opens (or creates) the SQLite database at path and ensures the schema
// has been applied. Subsequent calls are idempotent because every CREATE uses
// IF NOT EXISTS.
func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	if _, err := db.Exec(dbSchemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema in %s: %w", path, err)
	}
	return db, nil
}
