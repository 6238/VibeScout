package main

import "database/sql"

var db *sql.DB

func initDB() {
	var err error
	db, err = sql.Open("sqlite", "./vibe_scout.db")
	if err != nil {
		panic(err)
	}

	// Pairwise Schema
	query := `
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
    );`

	db.Exec(query)

	// Scout Submissions Schema
	db.Exec(`
    CREATE TABLE IF NOT EXISTS scout_submissions (
      id INTEGER PRIMARY KEY AUTOINCREMENT,
      event_key TEXT,
      match_num INTEGER,
      scouter_id INTEGER,
      payload TEXT,
      created_at DATETIME DEFAULT CURRENT_TIMESTAMP
    );
    `)
}
