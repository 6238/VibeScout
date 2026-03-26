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
}
