package tui

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
	for i := 0; i < rows; i++ {
		m.mark(y0+i, x, w, act)
	}
}

// hit searches from the end: last registered is drawn on top.
func (m *Model) hit(x, y int) func(*Model) {
	for i := len(m.zones) - 1; i >= 0; i-- {
		z := m.zones[i]
		if y == z.y && x >= z.x1 && x < z.x2 {
			m.clickX = x - z.x1
			return z.act
		}
	}
	return nil
}

func (m *Model) shiftZones(from, dx, dy int) {
	for i := from; i < len(m.zones); i++ {
		m.zones[i].y += dy
		m.zones[i].x1 += dx
		m.zones[i].x2 += dx
	}
}
