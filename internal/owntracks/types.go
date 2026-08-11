package owntracks

// Point is a single location fix from OwnTracks Recorder.
// Tst is the UNIX timestamp of the fix.
// Vel is the reported velocity in km/h (-1 if unknown).
// Acc is the GPS accuracy radius in metres.
type Point struct {
	Tst              int64    `json:"tst"`
	Lat              float64  `json:"lat"`
	Lon              float64  `json:"lon"`
	Vel              float64  `json:"vel"`
	Acc              float64  `json:"acc"`
	Cog              float64  `json:"cog"`
	Tag              string   `json:"tag,omitempty"`
	MotionActivities []string `json:"motionactivities,omitempty"`
}

// response is the top-level JSON envelope returned by /api/0/locations.
type response struct {
	Data []Point `json:"data"`
}
