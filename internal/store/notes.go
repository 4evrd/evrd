package store

import (
	"context"
	"time"

	"gorm.io/gorm/clause"
)

// UserNote is one of a user's notes (ZNC's notes module): a short text
// under a key, kept for the user and shown in the web app and to IRC
// clients (/msg *notes).
type UserNote struct {
	UserID    int64  `gorm:"primaryKey;autoIncrement:false"`
	Key       string `gorm:"column:note_key;primaryKey;size:64"`
	Text      string `gorm:"not null"`
	UpdatedAt time.Time
}

// NoteRepositoryImpl keeps users' notes.
type NoteRepositoryImpl interface {
	// Notes lists a user's notes by key.
	Notes(ctx context.Context, userID int64) ([]UserNote, error)
	// PutNote adds a note, or replaces the one with its key.
	PutNote(ctx context.Context, n UserNote) error
	// DeleteNote drops a note, reporting whether there was one.
	DeleteNote(ctx context.Context, userID int64, key string) (bool, error)
}

// NoteRepository is the NoteRepositoryImpl on the SQL database.
type NoteRepository struct{ db *DB }

// NewNoteRepository makes a NoteRepository on db.
func NewNoteRepository(db *DB) NoteRepositoryImpl { return &NoteRepository{db: db} }

func (r *NoteRepository) Notes(ctx context.Context, userID int64) ([]UserNote, error) {
	var out []UserNote
	return out, r.db.DB.WithContext(ctx).Where("user_id = ?", userID).Order("note_key").Find(&out).Error
}

func (r *NoteRepository) PutNote(ctx context.Context, n UserNote) error {
	if n.UpdatedAt.IsZero() {
		n.UpdatedAt = time.Now()
	}
	return r.db.DB.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "user_id"}, {Name: "note_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"text", "updated_at"}),
	}).Create(&n).Error
}

func (r *NoteRepository) DeleteNote(ctx context.Context, userID int64, key string) (bool, error) {
	res := r.db.DB.WithContext(ctx).Where("user_id = ? AND note_key = ?", userID, key).Delete(&UserNote{})
	return res.RowsAffected > 0, res.Error
}
