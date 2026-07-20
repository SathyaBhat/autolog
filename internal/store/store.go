package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sathyabhat/autolog/internal/trips"
)

const schema = `
CREATE TABLE IF NOT EXISTS trips (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    date          TEXT NOT NULL,
    start_time    INTEGER NOT NULL,
    end_time      INTEGER NOT NULL,
    start_lat     REAL NOT NULL,
    start_lon     REAL NOT NULL,
    end_lat       REAL NOT NULL,
    end_lon       REAL NOT NULL,
    distance_km   REAL NOT NULL,
    max_speed_kmh REAL NOT NULL,
    mode          TEXT NOT NULL,
    UNIQUE(date, start_time)
);
CREATE TABLE IF NOT EXISTS state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
`

// Store persists trips and processing state to SQLite.
type Store struct {
	db *sql.DB
}

// New opens (or creates) the SQLite database at path and applies the schema.
// Use ":memory:" for tests.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// SaveTrip inserts a trip. Duplicate (date, start_time) pairs are silently ignored.
func (s *Store) SaveTrip(ctx context.Context, t trips.Trip) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT OR IGNORE INTO trips
		  (date, start_time, end_time, start_lat, start_lon, end_lat, end_lon,
		   distance_km, max_speed_kmh, mode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.Date,
		t.StartTime.Unix(),
		t.EndTime.Unix(),
		t.StartLat, t.StartLon,
		t.EndLat, t.EndLon,
		t.DistanceKm,
		t.MaxSpeedKmh,
		string(t.Mode),
	)
	return err
}

// TripExists reports whether a trip with the given date and start time is
// already stored.
func (s *Store) TripExists(ctx context.Context, date string, startTime time.Time) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM trips WHERE date = ? AND start_time = ?`,
		date, startTime.Unix(),
	).Scan(&count)
	return count > 0, err
}

// GetLastProcessedTime returns the last successfully processed timestamp, or
// a zero time.Time if never set.
func (s *Store) GetLastProcessedTime(ctx context.Context) (time.Time, error) {
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT value FROM state WHERE key = 'last_processed_time'`,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, err
	}
	t, err := time.Parse(time.RFC3339, raw)
	return t, err
}

// SetLastProcessedTime upserts the last successfully processed timestamp.
func (s *Store) SetLastProcessedTime(ctx context.Context, t time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO state (key, value) VALUES ('last_processed_time', ?)`,
		t.UTC().Format(time.RFC3339),
	)
	return err
}
