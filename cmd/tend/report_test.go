package main

import (
	"testing"
	"time"
)

func TestPeriodStart(t *testing.T) {
	wed := time.Date(2026, 9, 23, 15, 4, 0, 0, time.Local)
	if got := periodStart("today", wed); !got.Equal(time.Date(2026, 9, 23, 0, 0, 0, 0, time.Local)) {
		t.Errorf("today starts at midnight: %v", got)
	}
	if got := periodStart("week", wed); !got.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)) {
		t.Errorf("the week starts on Monday: %v", got)
	}
	sun := time.Date(2026, 9, 27, 23, 0, 0, 0, time.Local)
	if got := periodStart("week", sun); !got.Equal(time.Date(2026, 9, 21, 0, 0, 0, 0, time.Local)) {
		t.Errorf("Sunday still belongs to the week that began on Monday: %v", got)
	}
}
