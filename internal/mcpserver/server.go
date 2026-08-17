package mcpserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/sathyabhat/autolog/internal/store"
)

// HTTPHandler returns a Streamable HTTP MCP endpoint protected by a bearer token.
func HTTPHandler(st *store.Store, timezone, token string) (http.Handler, error) {
	server, err := New(st, timezone)
	if err != nil {
		return nil, err
	}
	handler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return server },
		&mcp.StreamableHTTPOptions{JSONResponse: true},
	)
	return bearerAuth(handler, token), nil
}

func bearerAuth(next http.Handler, token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if token == "" || !strings.EqualFold(strings.TrimPrefix(auth, "Bearer "), token) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

type Server struct {
	store    *store.Store
	location *time.Location
}

func New(st *store.Store, timezone string) (*mcp.Server, error) {
	loc, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("load timezone %q: %w", timezone, err)
	}

	s := &Server{store: st, location: loc}
	server := mcp.NewServer(
		&mcp.Implementation{Name: "autolog", Version: "v0.1.0"},
		&mcp.ServerOptions{
			Instructions: "Query recorded trips from autolog. Dates and times are returned in the configured local timezone.",
		},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "list_trips",
		Description: "List recorded trips, optionally filtered by date range, location, and transport mode.",
	}, s.listTrips)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "trip_details",
		Description: "Get the details and intermediate stops for one recorded trip.",
	}, s.tripDetails)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "trip_days",
		Description: "Group recorded trips by day and summarize the destinations visited on each day.",
	}, s.tripDays)
	return server, nil
}

type listTripsInput struct {
	FromDate string `json:"from_date,omitempty" jsonschema:"first local date to include, YYYY-MM-DD"`
	ToDate   string `json:"to_date,omitempty" jsonschema:"last local date to include, YYYY-MM-DD"`
	Location string `json:"location,omitempty" jsonschema:"case-insensitive substring of a start or end location"`
	Mode     string `json:"mode,omitempty" jsonschema:"transport mode such as car, train, walking, or cycling"`
	Limit    int    `json:"limit,omitempty" jsonschema:"maximum number of trips, default 50, maximum 500"`
}

type tripOutput struct {
	Date          string  `json:"date"`
	StartTime     string  `json:"start_time"`
	EndTime       string  `json:"end_time"`
	StartLocation string  `json:"start_location,omitempty"`
	EndLocation   string  `json:"end_location,omitempty"`
	DistanceKm    float64 `json:"distance_km"`
	MaxSpeedKmh   float64 `json:"max_speed_kmh"`
	Mode          string  `json:"mode"`
	StopCount     int     `json:"stop_count"`
}

type listTripsOutput struct {
	Trips []tripOutput `json:"trips"`
	Count int          `json:"count"`
}

func (s *Server) listTrips(ctx context.Context, _ *mcp.CallToolRequest, in listTripsInput) (*mcp.CallToolResult, listTripsOutput, error) {
	if err := validateDate(in.FromDate); err != nil {
		return nil, listTripsOutput{}, err
	}
	if err := validateDate(in.ToDate); err != nil {
		return nil, listTripsOutput{}, err
	}
	if in.FromDate != "" && in.ToDate != "" && in.FromDate > in.ToDate {
		return nil, listTripsOutput{}, fmt.Errorf("from_date must not be after to_date")
	}
	if in.Limit <= 0 {
		in.Limit = 50
	}
	if in.Limit > 500 {
		in.Limit = 500
	}

	trips, err := s.store.ListTrips(ctx, in.FromDate, in.ToDate, in.Location, strings.ToLower(in.Mode), in.Limit)
	if err != nil {
		return nil, listTripsOutput{}, err
	}
	out := listTripsOutput{Trips: make([]tripOutput, 0, len(trips)), Count: len(trips)}
	for _, trip := range trips {
		out.Trips = append(out.Trips, s.formatTrip(trip))
	}
	return nil, out, nil
}

type tripDetailsInput struct {
	Date         string `json:"date" jsonschema:"local trip date, YYYY-MM-DD"`
	StartTime    string `json:"start_time" jsonschema:"local trip start time, HH:MM"`
	IncludePoint bool   `json:"include_points,omitempty" jsonschema:"include raw GPS points when true"`
}

type stopOutput struct {
	Arrival     string  `json:"arrival"`
	Departure   string  `json:"departure"`
	DurationMin float64 `json:"duration_minutes"`
	Location    string  `json:"location,omitempty"`
	Confidence  string  `json:"confidence"`
	Evidence    string  `json:"evidence,omitempty"`
	Latitude    float64 `json:"latitude"`
	Longitude   float64 `json:"longitude"`
}

type pointOutput struct {
	Time string  `json:"time"`
	Lat  float64 `json:"latitude"`
	Lon  float64 `json:"longitude"`
	Vel  float64 `json:"speed_kmh"`
	Tag  string  `json:"tag,omitempty"`
}

type tripDetailsOutput struct {
	tripOutput
	Stops  []stopOutput  `json:"stops"`
	Points []pointOutput `json:"points,omitempty"`
}

func (s *Server) tripDetails(ctx context.Context, _ *mcp.CallToolRequest, in tripDetailsInput) (*mcp.CallToolResult, tripDetailsOutput, error) {
	if err := validateDate(in.Date); err != nil {
		return nil, tripDetailsOutput{}, err
	}
	start, err := time.ParseInLocation("15:04", in.StartTime, s.location)
	if err != nil {
		return nil, tripDetailsOutput{}, fmt.Errorf("invalid start_time %q, expected HH:MM", in.StartTime)
	}
	startDate, _ := time.ParseInLocation("2006-01-02", in.Date, s.location)
	start = time.Date(startDate.Year(), startDate.Month(), startDate.Day(), start.Hour(), start.Minute(), 0, 0, s.location)

	summary, err := s.store.GetTripSummary(ctx, in.Date, start)
	if err != nil {
		return nil, tripDetailsOutput{}, err
	}
	stops, err := s.store.GetTripStops(ctx, in.Date, summary.StartTime)
	if err != nil {
		return nil, tripDetailsOutput{}, err
	}
	out := tripDetailsOutput{tripOutput: s.formatTrip(summary), Stops: make([]stopOutput, 0, len(stops))}
	for _, stop := range stops {
		out.Stops = append(out.Stops, stopOutput{
			Arrival:     time.Unix(stop.ArrivalTst, 0).In(s.location).Format(time.RFC3339),
			Departure:   time.Unix(stop.DepartureTst, 0).In(s.location).Format(time.RFC3339),
			DurationMin: float64(stop.DepartureTst-stop.ArrivalTst) / 60,
			Location:    stop.Location,
			Confidence:  string(stop.Confidence),
			Evidence:    stop.Evidence,
			Latitude:    stop.Lat,
			Longitude:   stop.Lon,
		})
	}
	if in.IncludePoint {
		points, err := s.store.GetTripPoints(ctx, in.Date, summary.StartTime)
		if err != nil {
			return nil, tripDetailsOutput{}, err
		}
		out.Points = make([]pointOutput, 0, len(points))
		for _, point := range points {
			out.Points = append(out.Points, pointOutput{
				Time: time.Unix(point.Tst, 0).In(s.location).Format(time.RFC3339),
				Lat:  point.Lat,
				Lon:  point.Lon,
				Vel:  point.Vel,
				Tag:  point.Tag,
			})
		}
	}
	return nil, out, nil
}

type tripDaysInput struct {
	FromDate string `json:"from_date,omitempty" jsonschema:"first local date to include, YYYY-MM-DD"`
	ToDate   string `json:"to_date,omitempty" jsonschema:"last local date to include, YYYY-MM-DD"`
	Location string `json:"location,omitempty" jsonschema:"case-insensitive substring of a start or end location"`
}

type dayOutput struct {
	Date         string   `json:"date"`
	TripCount    int      `json:"trip_count"`
	Destinations []string `json:"destinations"`
	Routes       []string `json:"routes"`
	TotalKm      float64  `json:"total_distance_km"`
}

type tripDaysOutput struct {
	Days []dayOutput `json:"days"`
}

func (s *Server) tripDays(ctx context.Context, _ *mcp.CallToolRequest, in tripDaysInput) (*mcp.CallToolResult, tripDaysOutput, error) {
	if err := validateDate(in.FromDate); err != nil {
		return nil, tripDaysOutput{}, err
	}
	if err := validateDate(in.ToDate); err != nil {
		return nil, tripDaysOutput{}, err
	}
	trips, err := s.store.ListTrips(ctx, in.FromDate, in.ToDate, in.Location, "", 0)
	if err != nil {
		return nil, tripDaysOutput{}, err
	}
	byDate := make(map[string]*dayOutput)
	order := make([]string, 0)
	for _, trip := range trips {
		day := byDate[trip.Date]
		if day == nil {
			day = &dayOutput{Date: trip.Date, Destinations: []string{}, Routes: []string{}}
			byDate[trip.Date] = day
			order = append(order, trip.Date)
		}
		day.TripCount++
		day.TotalKm += trip.DistanceKm
		if trip.EndLocation != "" && !contains(day.Destinations, trip.EndLocation) {
			day.Destinations = append(day.Destinations, trip.EndLocation)
		}
		route := strings.TrimSpace(trip.StartLocation + " -> " + trip.EndLocation)
		if route != "->" && !contains(day.Routes, route) {
			day.Routes = append(day.Routes, route)
		}
	}
	// ListTrips is newest first; return chronological days for easier reading.
	for i, j := 0, len(order)-1; i < j; i, j = i+1, j-1 {
		order[i], order[j] = order[j], order[i]
	}
	out := tripDaysOutput{Days: make([]dayOutput, 0, len(order))}
	for _, date := range order {
		out.Days = append(out.Days, *byDate[date])
	}
	return nil, out, nil
}

func (s *Server) formatTrip(trip store.TripSummary) tripOutput {
	return tripOutput{
		Date:          trip.Date,
		StartTime:     trip.StartTime.In(s.location).Format(time.RFC3339),
		EndTime:       trip.EndTime.In(s.location).Format(time.RFC3339),
		StartLocation: trip.StartLocation,
		EndLocation:   trip.EndLocation,
		DistanceKm:    trip.DistanceKm,
		MaxSpeedKmh:   trip.MaxSpeedKmh,
		Mode:          string(trip.Mode),
		StopCount:     trip.StopCount,
	}
}

func validateDate(value string) error {
	if value == "" {
		return nil
	}
	if _, err := time.Parse("2006-01-02", value); err != nil {
		return fmt.Errorf("invalid date %q, expected YYYY-MM-DD", value)
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}
