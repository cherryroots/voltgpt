// Package db manages the SQLite database for persistent storage.
package db

import (
	"database/sql"
	"log"
	"strings"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB

func Open(path string) {
	sqlite_vec.Auto()
	var err error
	DB, err = sql.Open("sqlite3", path+"?_journal_mode=WAL")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	if err := DB.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	createTables()
}

func Close() {
	if DB != nil {
		DB.Close()
	}
}

func createTables() {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS image_hashes (
			hash TEXT PRIMARY KEY,
			message_json TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS game_state (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			data TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			discord_id TEXT NOT NULL UNIQUE,
			username TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS facts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id INTEGER NOT NULL REFERENCES users(id),
			original_message_id TEXT NOT NULL,
			fact_text TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			is_active INTEGER DEFAULT 1,
			reinforcement_count INTEGER DEFAULT 0
		)`,
	}

	for _, table := range tables {
		if _, err := DB.Exec(table); err != nil {
			log.Fatalf("Failed to create table: %v", err)
		}
	}

	// Migrate: add display_name and preferred_name to users table
	migrations := []string{
		"ALTER TABLE users ADD COLUMN display_name TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE users ADD COLUMN preferred_name TEXT NOT NULL DEFAULT ''",
	}
	for _, m := range migrations {
		DB.Exec(m) // ignore "duplicate column name" errors
	}

	var vecFactsSQL string
	err := DB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='vec_facts'").Scan(&vecFactsSQL)
	if err == sql.ErrNoRows {
		_, err = DB.Exec(`CREATE VIRTUAL TABLE vec_facts USING vec0(fact_id INTEGER PRIMARY KEY, embedding float[768] distance_metric=cosine)`)
		if err != nil {
			log.Fatalf("Failed to create vec_facts table: %v", err)
		}
	} else if err == nil && !strings.Contains(vecFactsSQL, "cosine") {
		// Existing table uses L2 distance — drop and recreate with cosine.
		// Embeddings will be re-inserted by the memory package at startup.
		if _, err = DB.Exec("DROP TABLE vec_facts"); err != nil {
			log.Fatalf("Failed to drop vec_facts for migration: %v", err)
		}
		if _, err = DB.Exec(`CREATE VIRTUAL TABLE vec_facts USING vec0(fact_id INTEGER PRIMARY KEY, embedding float[768] distance_metric=cosine)`); err != nil {
			log.Fatalf("Failed to recreate vec_facts with cosine metric: %v", err)
		}
		log.Println("db: migrated vec_facts to cosine distance metric; embeddings will be re-inserted at startup")
	} else if err != nil {
		log.Fatalf("Failed to check vec_facts schema: %v", err)
	}
}
