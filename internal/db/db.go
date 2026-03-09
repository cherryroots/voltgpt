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
	if _, err := DB.Exec("PRAGMA foreign_keys = ON"); err != nil {
		log.Fatalf("Failed to enable SQLite foreign keys: %v", err)
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
	// Cold cutover to memory v2. Old flat-fact memory is intentionally discarded.
	for _, stmt := range []string{
		"DROP TABLE IF EXISTS vec_facts",
		"DROP TABLE IF EXISTS facts",
	} {
		if _, err := DB.Exec(stmt); err != nil {
			log.Fatalf("Failed to drop legacy memory table: %v", err)
		}
	}

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
			username TEXT NOT NULL,
			display_name TEXT NOT NULL DEFAULT '',
			preferred_name TEXT NOT NULL DEFAULT ''
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
		`CREATE TABLE IF NOT EXISTS guild_user_profiles (
			guild_id             TEXT NOT NULL,
			user_id              INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			bio                  TEXT NOT NULL DEFAULT '[]',
			interests            TEXT NOT NULL DEFAULT '[]',
			skills               TEXT NOT NULL DEFAULT '[]',
			opinions             TEXT NOT NULL DEFAULT '[]',
			relationships        TEXT NOT NULL DEFAULT '[]',
			other                TEXT NOT NULL DEFAULT '[]',
			is_dirty             INTEGER NOT NULL DEFAULT 0,
			updated_at           DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			last_full_rebuild_at DATETIME,
			PRIMARY KEY (guild_id, user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS interaction_notes (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			guild_id        TEXT NOT NULL,
			channel_id      TEXT,
			note_type       TEXT NOT NULL,
			title           TEXT NOT NULL,
			summary         TEXT NOT NULL,
			source_note_ids TEXT NOT NULL DEFAULT '[]',
			note_date       DATE NOT NULL,
			created_at      DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS note_participants (
			note_id             INTEGER NOT NULL REFERENCES interaction_notes(id) ON DELETE CASCADE,
			participant_user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			PRIMARY KEY (note_id, participant_user_id)
		)`,
		`CREATE TABLE IF NOT EXISTS channel_buffers (
			channel_id TEXT PRIMARY KEY,
			guild_id   TEXT NOT NULL,
			messages   TEXT NOT NULL,
			started_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS memory_job_runs (
			guild_id    TEXT NOT NULL,
			job_date    DATE NOT NULL,
			phase       TEXT NOT NULL,
			status      TEXT NOT NULL,
			started_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			finished_at DATETIME,
			PRIMARY KEY (guild_id, job_date, phase)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_guild_type_date
			ON interaction_notes(guild_id, note_type, note_date, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_notes_guild_channel_created
			ON interaction_notes(guild_id, channel_id, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_note_participants_user_note
			ON note_participants(participant_user_id, note_id)`,
		`CREATE INDEX IF NOT EXISTS idx_profiles_guild_dirty
			ON guild_user_profiles(guild_id, is_dirty, updated_at)`,
	}

	for _, table := range tables {
		if _, err := DB.Exec(table); err != nil {
			log.Fatalf("Failed to create table: %v", err)
		}
	}

	ensureVecNotesTable()
}

func ensureVecNotesTable() {
	const createVecNotesSQL = `CREATE VIRTUAL TABLE vec_notes USING vec0(note_id INTEGER PRIMARY KEY, embedding float[1536] distance_metric=cosine)`

	var sqlText string
	err := DB.QueryRow("SELECT sql FROM sqlite_master WHERE type='table' AND name='vec_notes'").Scan(&sqlText)
	switch err {
	case nil:
		if strings.Contains(strings.ToLower(sqlText), "float[1536]") {
			return
		}
		log.Printf("Migrating vec_notes to 1536-dimensional embeddings; clearing incompatible stored vectors")
		if _, err := DB.Exec("DROP TABLE vec_notes"); err != nil {
			log.Fatalf("Failed to drop vec_notes table: %v", err)
		}
	case sql.ErrNoRows:
	default:
		log.Fatalf("Failed to check vec_notes table: %v", err)
	}

	if _, err := DB.Exec(createVecNotesSQL); err != nil {
		log.Fatalf("Failed to create vec_notes table: %v", err)
	}
}
