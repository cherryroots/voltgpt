// Package db manages the SQLite database for persistent storage.
package db

import (
	"database/sql"
	"fmt"
	"log"
	"strings"
	"sync/atomic"

	sqlite_vec "github.com/asg017/sqlite-vec-go-bindings/cgo"
	_ "github.com/mattn/go-sqlite3"
)

var DB *sql.DB
var memoryDBCounter atomic.Uint64

func Open(path string) {
	sqlite_vec.Auto()

	if DB != nil {
		if err := DB.Close(); err != nil {
			log.Printf("Failed to close existing database before reopen: %v", err)
		}
		DB = nil
	}

	dsn := path + "?_journal_mode=WAL"
	if path == ":memory:" {
		// Give each test run its own named in-memory database while still
		// keeping a shared cache within that single sql.DB handle.
		dsn = fmt.Sprintf("file:voltgpt-memory-%d?mode=memory&cache=shared", memoryDBCounter.Add(1))
	}
	var err error
	DB, err = sql.Open("sqlite3", dsn)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}
	if path == ":memory:" {
		// SQLite in-memory databases are per-connection. Tests use ":memory:",
		// so keep a single shared connection to avoid "no such table" races.
		DB.SetMaxOpenConns(1)
		DB.SetMaxIdleConns(1)
		DB.SetConnMaxLifetime(0)
	}

	if err := DB.Ping(); err != nil {
		log.Fatalf("Failed to ping database %s: %v", path, err)
	}

	createTables()
}

func Close() {
	if DB != nil {
		DB.Close()
		DB = nil
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
		`CREATE TABLE IF NOT EXISTS reminders (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	user_id    TEXT    NOT NULL,
	channel_id TEXT    NOT NULL,
	guild_id   TEXT    NOT NULL,
	message    TEXT    NOT NULL,
	images     TEXT,
	fire_at    INTEGER NOT NULL,
	created_at INTEGER NOT NULL DEFAULT (unixepoch())
)`,
		`CREATE TABLE IF NOT EXISTS response_ids (
			discord_message_id TEXT PRIMARY KEY,
			openai_response_id TEXT NOT NULL
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

	ensureVecFactsTable()
}

func ensureVecFactsTable() {
	const createVecFactsSQL = `CREATE VIRTUAL TABLE vec_facts USING vec0(fact_id INTEGER PRIMARY KEY, embedding float[1536] distance_metric=cosine)`

	var sqlText string
	err := DB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='vec_facts'").Scan(&sqlText)
	switch err {
	case nil:
		if strings.Contains(strings.ToLower(sqlText), "float[1536]") {
			return
		}
		log.Printf("Migrating vec_facts to 1536-dimensional embeddings; clearing incompatible stored vectors")
		if _, err := DB.Exec("DROP TABLE vec_facts"); err != nil {
			log.Fatalf("Failed to drop vec_facts table: %v", err)
		}
	case sql.ErrNoRows:
	default:
		log.Fatalf("Failed to check vec_facts table: %v", err)
	}

	if _, err := DB.Exec(createVecFactsSQL); err != nil {
		log.Fatalf("Failed to create vec_facts table: %v", err)
	}
}
