//go:build !windows

package tui

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestThinWheelAccelerates(t *testing.T) {
	setWheelTuning(3, "normal")
	w := &wheelInput{}
	t0 := time.Unix(100, 0)
	ev := "\x1b[<65;10;5M"
	count := func(b []byte) int { return bytes.Count(b, []byte(ev)) }
	if got := count(w.thin([]byte("j"+strings.Repeat(ev, 3)+"k"), t0)); got != 3 {
		t.Fatalf("one step of events passes as one step, got %d", got)
	}
	if got := count(w.thin([]byte(strings.Repeat(ev, 300)), t0.Add(5*time.Millisecond))); got != 0 {
		t.Fatalf("inside the frame nothing passes, got %d", got)
	}
	// 300 events waited + 3 now = 101 steps requested → sqrt → 10 steps = 30 events
	if got := count(w.thin([]byte(strings.Repeat(ev, 3)), t0.Add(wheelFrame))); got != 30 {
		t.Fatalf("a fast scroll accelerates: want 30 events, got %d", got)
	}
	up := "\x1b[<64;1;1M"
	got := w.thin([]byte(strings.Repeat(ev, 9)+up), t0.Add(2*wheelFrame))
	if count(got) != 6 || bytes.Count(got, []byte(up)) != 0 {
		t.Fatalf("a direction change resets the count and the reverse run waits for the next frame: %q", got)
	}
	if plain := []byte("abc\x1b[A"); !bytes.Equal(w.thin(plain, t0), plain) {
		t.Fatal("non-wheel input must pass through unchanged")
	}
}

func TestWheelSpeedSetting(t *testing.T) {
	ev := "\x1b[<65;10;5M"
	for _, tc := range []struct {
		speed string
		want  int
	}{{"off", 3}, {"normal", 30}, {"fast", 60}} {
		setWheelTuning(3, tc.speed)
		w := &wheelInput{}
		t0 := time.Unix(100, 0)
		w.thin([]byte(strings.Repeat(ev, 3)), t0)                         // first frame drains
		w.thin([]byte(strings.Repeat(ev, 300)), t0.Add(time.Millisecond)) // inside the frame: 300 waiting
		got := bytes.Count(w.thin([]byte(strings.Repeat(ev, 3)), t0.Add(wheelFrame)), []byte(ev))
		if got != tc.want {
			t.Errorf("speed %q: 101 steps requested gave %d events, want %d", tc.speed, got, tc.want)
		}
	}
	setWheelTuning(3, "normal")
}

func TestDropCutRemovesBothHalvesOfACutSequence(t *testing.T) {
	for in, want := range map[string]string{
		"0;30Mab\x1b[<65;12":       "ab",
		"\x1b[<65;12;7M":           "\x1b[<65;12;7M",
		"\x1b":                     "\x1b",
		"q":                        "q",
		"M":                        "M",
		"\x1b[<65;12;7M\x1b[<65;1": "\x1b[<65;12;7M",
	} {
		if got := string(dropCut([]byte(in))); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}
