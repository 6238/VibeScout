package main

import (
	"database/sql"
	"os"
	"path/filepath"
)

var db *sql.DB

func initDB() {
	var err error

	if mountPath := os.Getenv("RAILWAY_VOLUME_MOUNT_PATH"); mountPath != "" {
		// Only use the Railway path if the directory actually exists
		if info, statErr := os.Stat(mountPath); statErr == nil && info.IsDir() {
			db, err = sql.Open("sqlite", filepath.Join(mountPath, "vibescout.db"))
		} else {
			db, err = sql.Open("sqlite", "./vibe_scout.db")
		}
	} else {
		db, err = sql.Open("sqlite", "./vibe_scout.db")
	}

	if err != nil {
		panic(err)
	}

	db.Exec(`
    CREATE TABLE IF NOT EXISTS scout_submissions (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      event_key TEXT,
      match_num INTEGER,
      scouter_id INTEGER,
      team_number TEXT,
      notes TEXT,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`)

	db.Exec(`
    CREATE TABLE IF NOT EXISTS analysis_cache (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      event_key TEXT NOT NULL,
      team_number TEXT NOT NULL,
      analysis TEXT NOT NULL,
      notes_hash TEXT NOT NULL,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      UNIQUE(event_key, team_number)
    );`)

	db.Exec(`
    CREATE TABLE IF NOT EXISTS match_plan_cache (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      event_key TEXT NOT NULL,
      team_number TEXT NOT NULL,
      match_num INTEGER NOT NULL,
      strategy TEXT NOT NULL,
      notes_hash TEXT NOT NULL,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
      UNIQUE(event_key, team_number, match_num)
    );`)

	db.Exec(`
    CREATE TABLE IF NOT EXISTS pit_scouting (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      team_number TEXT NOT NULL,
      summary TEXT NOT NULL,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`)

	// Structured pit info (archetype, role, ...) extracted from the pit summary,
	// cached until the summary changes.
	db.Exec(`
    CREATE TABLE IF NOT EXISTS pit_profile_cache (
      team_number TEXT PRIMARY KEY,
      profile TEXT NOT NULL,
      notes_hash TEXT NOT NULL,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`)

	// Idempotent migration: add ai_generated flag if it doesn't exist yet
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN ai_generated INTEGER DEFAULT 0`)

	// Idempotent migration: scouters are identified by name (scouter_id is legacy)
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN scouter_name TEXT DEFAULT ''`)

	// Idempotent migration: structured match checklist. has_checklist marks rows
	// saved from the checklist UI, so older rows' defaults aren't read as "no".
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN has_checklist INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN broke INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN played_defense INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN was_defended INTEGER DEFAULT 0`)
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN auto_type TEXT DEFAULT ''`)

	// Idempotent migration: a client-generated id for retry safety. A scout's
	// device saves a submission locally and keeps retrying it until the server
	// confirms — possibly several times, if a slow connection drops the
	// response without dropping the request. Every retry of the same attempt
	// carries the same submission_id, so retries update the same row instead
	// of inserting a duplicate. Old rows and any caller that doesn't set one
	// (e.g. the AI video-fill admin tool) keep '' and are exempt from the
	// uniqueness constraint below, preserving today's plain-insert behavior.
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN submission_id TEXT DEFAULT ''`)
	db.Exec(`
    CREATE UNIQUE INDEX IF NOT EXISTS idx_scout_submission_dedupe
      ON scout_submissions(submission_id, team_number)
      WHERE submission_id != '';`)

	// Idempotent migration: whether this note came from field scouting's
	// one-robot mode (focused on a single robot) or the multi-robot mode
	// (splitting attention across a whole alliance). A one-robot note is
	// treated as the more reliable account when two scouts covered the same
	// match — see combineTeamNotes.
	db.Exec(`ALTER TABLE scout_submissions ADD COLUMN single_team INTEGER DEFAULT 0`)

	// One AI-generated row per (event, match, team): retrying the video-fill
	// admin tool for a team it already filled in (a page reload, a double
	// click) updates that row instead of inserting a duplicate.
	db.Exec(`
    CREATE UNIQUE INDEX IF NOT EXISTS idx_scout_ai_fill_dedupe
      ON scout_submissions(event_key, match_num, team_number)
      WHERE ai_generated = 1;`)
}
