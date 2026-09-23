package tui

import "sync/atomic"

// Wheel tuning read by the input reader goroutine: events per step, and the fling multiplier the speed setting picks.
var wheelTuning atomic.Int64

var wheelSpeeds = []string{"off", "normal", "fast"}

func speedMult(name string) float64 {
	switch name {
	case "off":
		return 0
	case "fast":
		return 2
	}
	return 1
}

func setWheelTuning(step int, speed string) {
	wheelTuning.Store(int64(max(1, step))<<8 | int64(speedMult(speed)*10))
}

func wheelTune() (step int, mult float64) {
	v := wheelTuning.Load()
	if v == 0 {
		return 3, 1
	}
	return int(v >> 8), float64(v&0xff) / 10
}
