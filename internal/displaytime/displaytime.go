// Package displaytime renders stored instants for people without changing canonical UTC fields.
package displaytime

import "time"

const layout = "2006-01-02T15:04:05.999999999-07:00"

// Format renders an instant in the supplied location with an explicit UTC offset.
func Format(at time.Time, location *time.Location) string {
	return at.In(location).Format(layout)
}

// FromRFC3339 parses a stored instant before rendering it for display.
func FromRFC3339(recorded string, location *time.Location) (string, error) {
	at, err := time.Parse(time.RFC3339Nano, recorded)
	if err != nil {
		return "", err
	}
	return Format(at, location), nil
}
