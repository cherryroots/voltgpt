// Package reminder manages scheduled Discord reminders backed by SQLite.
package reminder

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

var (
	database *sql.DB
	session  *discordgo.Session
	mu       sync.Mutex
	timers   = map[int64]*time.Timer{}
)

// Image is an attached image stored with a reminder.
type Image struct {
	Filename string `json:"filename"`
	Data     string `json:"data"` // base64-encoded bytes
}

// Reminder is a scheduled reminder row loaded from SQLite.
type Reminder struct {
	ID        int64
	UserID    string
	ChannelID string
	GuildID   string
	Message   string
	Images    []Image
	FireAt    int64 // Unix timestamp (seconds)
}

// Init loads all pending reminders from SQLite and schedules them.
// Must be called from main() after dg.Open(), because it needs the session.
func Init(db *sql.DB, s *discordgo.Session) {
	database = db
	session = s
	loadAndSchedule()
}

func loadAndSchedule() {
	rows, err := database.Query(
		"SELECT id, user_id, channel_id, guild_id, message, images, fire_at FROM reminders",
	)
	if err != nil {
		log.Printf("reminder: failed to load pending reminders: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var r Reminder
		var imagesJSON sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.ChannelID, &r.GuildID, &r.Message, &imagesJSON, &r.FireAt); err != nil {
			log.Printf("reminder: scan error: %v", err)
			continue
		}
		if imagesJSON.Valid && imagesJSON.String != "" {
			if err := json.Unmarshal([]byte(imagesJSON.String), &r.Images); err != nil {
				log.Printf("reminder: unmarshal images for id %d: %v", r.ID, err)
			}
		}
		schedule(r)
	}
	if err := rows.Err(); err != nil {
		log.Printf("reminder: rows error loading reminders: %v", err)
	}
}

func schedule(r Reminder) {
	delay := time.Until(time.Unix(r.FireAt, 0))
	delay = max(delay, 0)
	mu.Lock()
	t := time.AfterFunc(delay, func() { fire(r) })
	timers[r.ID] = t
	mu.Unlock()
}

func fire(r Reminder) {
	mu.Lock()
	delete(timers, r.ID)
	mu.Unlock()

	msg := fmt.Sprintf("⏰ <@%s> Reminder: %s", r.UserID, r.Message)

	var sendErr error
	if len(r.Images) == 0 {
		_, sendErr = session.ChannelMessageSend(r.ChannelID, msg)
	} else {
		files := make([]*discordgo.File, 0, len(r.Images))
		for _, img := range r.Images {
			data, err := base64.StdEncoding.DecodeString(img.Data)
			if err != nil {
				log.Printf("reminder: decode image %q: %v", img.Filename, err)
				continue
			}
			files = append(files, &discordgo.File{
				Name:   img.Filename,
				Reader: bytes.NewReader(data),
			})
		}
		_, sendErr = session.ChannelMessageSendComplex(r.ChannelID, &discordgo.MessageSend{
			Content: msg,
			Files:   files,
		})
	}
	if sendErr != nil {
		log.Printf("reminder: failed to send reminder %d: %v", r.ID, sendErr)
	}

	if _, err := database.Exec("DELETE FROM reminders WHERE id = ?", r.ID); err != nil {
		log.Printf("reminder: failed to delete reminder %d after firing: %v", r.ID, err)
	}
}

// Add inserts a new reminder into SQLite and schedules its timer.
func Add(userID, channelID, guildID, message string, images []Image, fireAt time.Time) error {
	var imagesJSON sql.NullString
	if len(images) > 0 {
		b, err := json.Marshal(images)
		if err != nil {
			return fmt.Errorf("marshal images: %w", err)
		}
		imagesJSON = sql.NullString{String: string(b), Valid: true}
	}

	result, err := database.Exec(
		"INSERT INTO reminders (user_id, channel_id, guild_id, message, images, fire_at) VALUES (?, ?, ?, ?, ?, ?)",
		userID, channelID, guildID, message, imagesJSON, fireAt.Unix(),
	)
	if err != nil {
		return fmt.Errorf("insert reminder: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	schedule(Reminder{
		ID:        id,
		UserID:    userID,
		ChannelID: channelID,
		GuildID:   guildID,
		Message:   message,
		Images:    images,
		FireAt:    fireAt.Unix(),
	})
	return nil
}

// Delete cancels and removes a reminder by ID. Returns true if a row was deleted.
func Delete(id int64) bool {
	mu.Lock()
	if t, ok := timers[id]; ok {
		t.Stop()
		delete(timers, id)
	}
	mu.Unlock()

	res, err := database.Exec("DELETE FROM reminders WHERE id = ?", id)
	if err != nil {
		log.Printf("reminder: delete %d: %v", id, err)
		return false
	}
	n, _ := res.RowsAffected()
	return n > 0
}

// GetUserReminders returns all future reminders for userID, ordered by fire time.
func GetUserReminders(userID string) ([]Reminder, error) {
	rows, err := database.Query(
		"SELECT id, user_id, channel_id, guild_id, message, images, fire_at FROM reminders WHERE user_id = ? AND fire_at > ? ORDER BY fire_at ASC",
		userID, time.Now().Unix(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var reminders []Reminder
	for rows.Next() {
		var r Reminder
		var imagesJSON sql.NullString
		if err := rows.Scan(&r.ID, &r.UserID, &r.ChannelID, &r.GuildID, &r.Message, &imagesJSON, &r.FireAt); err != nil {
			log.Printf("reminder: scan: %v", err)
			continue
		}
		if imagesJSON.Valid && imagesJSON.String != "" {
			if err := json.Unmarshal([]byte(imagesJSON.String), &r.Images); err != nil {
				log.Printf("reminder: unmarshal images for id %d: %v", r.ID, err)
			}
		}
		reminders = append(reminders, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reminder: rows: %w", err)
	}
	return reminders, nil
}

// TotalActive returns the count of in-memory scheduled timers.
func TotalActive() int {
	mu.Lock()
	defer mu.Unlock()
	return len(timers)
}
