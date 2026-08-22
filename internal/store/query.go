package store

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/sathyabhat/autolog/internal/trips"
)

// TripSummary is the read model exposed to history queries.
type TripSummary struct {
	Date          string
	StartTime     time.Time
	EndTime       time.Time
	StartLocation string
	EndLocation   string
	DistanceKm    float64
	MaxSpeedKmh   float64
	Mode          trips.TransportMode
	StopCount     int
}

// ListTrips returns stored trips ordered from newest to oldest.
func (s *Store) ListTrips(ctx context.Context, fromDate, toDate, location, mode string, limit int) ([]TripSummary, error) {
	query := `
		SELECT t.date, t.start_time, t.end_time, t.start_location, t.end_location,
		       t.distance_km, t.max_speed_kmh, t.mode,
		       (SELECT COUNT(*) FROM stop_points sp WHERE sp.trip_id = t.id)
		FROM trips t
		WHERE 1 = 1`
	args := make([]any, 0, 6)

	if fromDate != "" {
		query += " AND t.date >= ?"
		args = append(args, fromDate)
	}
	if toDate != "" {
		query += " AND t.date <= ?"
		args = append(args, toDate)
	}
	if location != "" {
		query += " AND (LOWER(t.start_location) LIKE ? OR LOWER(t.end_location) LIKE ?)"
		needle := "%" + strings.ToLower(location) + "%"
		args = append(args, needle, needle)
	}
	if mode != "" {
		query += " AND t.mode = ?"
		args = append(args, mode)
	}

	query += " ORDER BY t.start_time DESC"
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list trips: %w", err)
	}
	defer rows.Close()

	var result []TripSummary
	for rows.Next() {
		var trip TripSummary
		var startUnix, endUnix int64
		var mode string
		if err := rows.Scan(
			&trip.Date,
			&startUnix,
			&endUnix,
			&trip.StartLocation,
			&trip.EndLocation,
			&trip.DistanceKm,
			&trip.MaxSpeedKmh,
			&mode,
			&trip.StopCount,
		); err != nil {
			return nil, fmt.Errorf("scan trip: %w", err)
		}
		trip.StartTime = time.Unix(startUnix, 0).UTC()
		trip.EndTime = time.Unix(endUnix, 0).UTC()
		trip.Mode = trips.TransportMode(mode)
		result = append(result, trip)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trips: %w", err)
	}
	return result, nil
}

// GetTripSummary returns one stored trip by its local date and start time.
func (s *Store) GetTripSummary(ctx context.Context, date string, startTime time.Time) (TripSummary, error) {
	var trip TripSummary
	var startUnix, endUnix int64
	var mode string
	startMinute := startTime.Truncate(time.Minute)
	err := s.db.QueryRowContext(ctx, `
		SELECT t.date, t.start_time, t.end_time, t.start_location, t.end_location,
		       t.distance_km, t.max_speed_kmh, t.mode,
		       (SELECT COUNT(*) FROM stop_points sp WHERE sp.trip_id = t.id)
		FROM trips t
		WHERE t.date = ? AND t.start_time >= ? AND t.start_time < ?`,
		date, startMinute.Unix(), startMinute.Add(time.Minute).Unix(),
	).Scan(
		&trip.Date,
		&startUnix,
		&endUnix,
		&trip.StartLocation,
		&trip.EndLocation,
		&trip.DistanceKm,
		&trip.MaxSpeedKmh,
		&mode,
		&trip.StopCount,
	)
	if err != nil {
		return TripSummary{}, fmt.Errorf("get trip: %w", err)
	}
	trip.StartTime = time.Unix(startUnix, 0).UTC()
	trip.EndTime = time.Unix(endUnix, 0).UTC()
	trip.Mode = trips.TransportMode(mode)
	return trip, nil
}
