package capture

import (
	"bufio"
	"encoding/json"
	"os"
	"time"
)

// SessionStart is the first line with a timestamp; Claude's leading status lines have none. ⚠️ Files reach hundreds of MB, read only the head.
func SessionStart(path string) (time.Time, bool) {
	f, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for n := 0; n < 50 && sc.Scan(); n++ {
		var line struct {
			Timestamp time.Time `json:"timestamp"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && !line.Timestamp.IsZero() {
			return line.Timestamp.Local(), true
		}
	}
	return time.Time{}, false
}
