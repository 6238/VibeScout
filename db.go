package main

import (
	"database/sql"
	"os"
	"path/filepath"
)

var db *sql.DB

func initDB() {
	var err error

	// Check for Railway volume mount path
	if mountPath := os.Getenv("RAILWAY_VOLUME_MOUNT_PATH"); mountPath != "" {
		dbPath := filepath.Join(mountPath, "vibescout.db")
		db, err = sql.Open("sqlite", dbPath)
	} else {
		// Local development
		db, err = sql.Open("sqlite", "./vibe_scout.db")
	}

	if err != nil {
		panic(err)
	}

	// Scout Submissions Schema with new fields
	db.Exec(`
    CREATE TABLE IF NOT EXISTS scout_submissions (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      event_key TEXT,
      match_num INTEGER,
      scouter_id INTEGER,
      
      -- Auto fields
      team_number TEXT,
      auto_path TEXT,
      auto_start_pos TEXT,
      auto_climb TEXT,
      
      -- Teleop fields
      teleop_climb TEXT,
      defense_pct INTEGER,
      defended_against_pct INTEGER,
      
      -- Notes
      notes TEXT,
      
      payload TEXT,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `)

	// Keep pairwise for backward compatibility
	db.Exec(`
    CREATE TABLE IF NOT EXISTS pairwise_scouting (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        event_key TEXT,
        match_num INTEGER,
        scouter_id INTEGER,
        category TEXT,
        team_a TEXT,
        team_b TEXT,
        difference INTEGER, 
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );`)

	// Defense Percentages Schema (keep for backward compatibility)
	db.Exec(`
    CREATE TABLE IF NOT EXISTS defense_percentages (
        id INTEGER PRIMARY KEY AUTOINCREMENT,
        event_key TEXT,
        match_num INTEGER,
        scouter_id INTEGER,
        team_number TEXT,
        defense_score INTEGER,
        created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `)
}
