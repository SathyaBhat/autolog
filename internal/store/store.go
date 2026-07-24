package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	_ "modernc.org/sqlite"

	"github.com/sathyabhat/autolog/internal/owntracks"
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
CREATE TABLE IF NOT EXISTS trip_points (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    trip_id INTEGER NOT NULL REFERENCES trips(id) ON DELETE CASCADE,
    tst     INTEGER NOT NULL,
    lat     REAL NOT NULL,
    lon     REAL NOT NULL,
    vel     REAL NOT NULL,
    acc     REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS trip_points_trip_id ON trip_points(trip_id);
CREATE TABLE IF NOT EXISTS state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS geocode_cache (
    lat   REAL NOT NULL,
    lon   REAL NOT NULL,
    label TEXT NOT NULL,
    PRIMARY KEY (lat, lon)
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

// SaveTrip inserts a trip and its GPS points in a single transaction.
// Duplicate (date, start_time) pairs are silently ignored (no points inserted either).
func (s *Store) SaveTrip(ctx context.Context, t trips.Trip) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx, `
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
	if err != nil {
		return fmt.Errorf("insert trip: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		// INSERT OR IGNORE hit a duplicate — nothing to do.
		return tx.Commit()
	}

	tripID, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("last insert id: %w", err)
	}

	for _, p := range t.Points {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO trip_points (trip_id, tst, lat, lon, vel, acc) VALUES (?, ?, ?, ?, ?, ?)`,
			tripID, p.Tst, p.Lat, p.Lon, p.Vel, p.Acc,
		); err != nil {
			return fmt.Errorf("insert point: %w", err)
		}
	}

	return tx.Commit()
}

// GetTripPoints returns the GPS points for a trip identified by its date and
// start time, ordered by timestamp.
func (s *Store) GetTripPoints(ctx context.Context, date string, startTime time.Time) ([]owntracks.Point, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.tst, p.lat, p.lon, p.vel, p.acc
		FROM trip_points p
		JOIN trips t ON t.id = p.trip_id
		WHERE t.date = ? AND t.start_time = ?
		ORDER BY p.tst`,
		date, startTime.Unix(),
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var pts []owntracks.Point
	for rows.Next() {
		var p owntracks.Point
		if err := rows.Scan(&p.Tst, &p.Lat, &p.Lon, &p.Vel, &p.Acc); err != nil {
			return nil, err
		}
		pts = append(pts, p)
	}
	return pts, rows.Err()
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

func geoRound(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

// GetGeocode looks up a cached reverse-geocode label for lat/lon.
// Returns (label, true, nil) on hit, ("", false, nil) on miss.
func (s *Store) GetGeocode(ctx context.Context, lat, lon float64) (string, bool, error) {
	var label string
	err := s.db.QueryRowContext(ctx,
		`SELECT label FROM geocode_cache WHERE lat = ? AND lon = ?`,
		geoRound(lat), geoRound(lon),
	).Scan(&label)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return label, true, nil
}

// SaveGeocode upserts a reverse-geocode label for lat/lon.
func (s *Store) SaveGeocode(ctx context.Context, lat, lon float64, label string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO geocode_cache (lat, lon, label) VALUES (?, ?, ?)`,
		geoRound(lat), geoRound(lon), label,
	)
	return err
}
