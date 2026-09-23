package tui

import (
	"slices"
	"time"
)

// Click zones are registered while rendering and looked up on mouse events; they describe the current frame.
type zone struct {
	y, x1, x2 int // row, columns [x1, x2)
	act       func(*Model)
}

func (m *Model) mark(y, x, w int, act func(*Model)) {
	if w > 0 && act != nil {
		m.zones = append(m.zones, zone{y: y, x1: x, x2: x + w, act: act})
	}
}

func (m *Model) markRows(y0, x, w, rows int, act func(*Model)) {
	for i := range rows {
		m.mark(y0+i, x, w, act)
	}
}

// hit searches from the end: last registered is drawn on top.
func (m *Model) hit(x, y int) func(*Model) {
	for _, z := range slices.Backward(m.zones) {
		if y == z.y && x >= z.x1 && x < z.x2 {
			m.clickX = x - z.x1
			return z.act
		}
	}
	return nil
}

func (m *Model) shiftZones(dx, dy int) {
	for i := range m.zones {
		m.zones[i].y += dy
		m.zones[i].x1 += dx
		m.zones[i].x2 += dx
	}
}

// doubleClick: a second click on the same thing within this is a double click.
const doubleClick = 500 * time.Millisecond

type clicks struct {
	i  int
	at time.Time
}

// double records a click on i: true when it is the second one on i within doubleClick (and the next one starts over).
func (c *clicks) double(i int) bool {
	now := time.Now()
	if c.i == i && now.Sub(c.at) < doubleClick {
		*c = clicks{}
		return true
	}
	c.i, c.at = i, now
	return false
}
