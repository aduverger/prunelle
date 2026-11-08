package store

import (
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// NewID returns a time-ordered ID with a type prefix, such as top_01a1....
func NewID(prefix string) string {
	return prefix + "_" + strings.ReplaceAll(uuid.Must(uuid.NewV7()).String(), "-", "")
}

const timeLayout = time.RFC3339Nano

// FormatTime is the stored form of timestamps.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// ParseTime reads a stored timestamp; stored values are always written by FormatTime.
func ParseTime(s string) time.Time {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		panic(fmt.Sprintf("corrupt timestamp %q: %v", s, err))
	}
	return t
}
