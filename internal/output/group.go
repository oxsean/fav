package output

// Item is what a reader sees as one line of the timeline: one event, or a run of reads and searches with nothing else
// between them. ID is its first event's id; Events are indexes into the events it was made from.
type Item struct {
	ID     string   `json:"id"`
	Group  bool     `json:"group,omitempty"`
	Events []int    `json:"events"`
	IDs    []string `json:"ids"`
}

// Has tells whether id is one of the item's events: a state kept by an item's id survives an earlier page arriving and
// becoming the group's new start.
func (it Item) Has(id string) bool {
	for _, x := range it.IDs {
		if x == id {
			return true
		}
	}
	return false
}

// Results maps a call to the index of its result, among events: the renderer shows them as one, whichever arrived
// first.
func Results(events []Event) map[string]int {
	calls := map[string]bool{}
	for _, e := range events {
		if e.Call != "" {
			calls[e.Call] = true
		}
	}
	res := map[string]int{}
	for i, e := range events {
		if e.Kind == KindToolResult && calls[e.Ref] {
			res[e.Ref] = i
		}
	}
	return res
}

// Items lays events out as the timeline's items: a result whose call is among events belongs to the call; consecutive
// reads and searches under the same parent make one group. Temp events are not laid out.
func Items(events []Event) []Item {
	res := Results(events)
	var out []Item
	open := -1
	for i, e := range events {
		if j, ok := res[e.Ref]; e.Temp || e.Kind == KindToolResult && ok && j == i {
			continue
		}
		if groups(e) && open >= 0 && events[out[open].Events[0]].Parent == e.Parent {
			out[open].Group = true
			out[open].Events = append(out[open].Events, i)
			out[open].IDs = append(out[open].IDs, e.ID)
			continue
		}
		out = append(out, Item{ID: e.ID, Events: []int{i}, IDs: []string{e.ID}})
		open = -1
		if groups(e) {
			open = len(out) - 1
		}
	}
	return out
}

func groups(e Event) bool {
	return e.Kind == KindTool && e.Request == "" && (e.Family == FamilyRead || e.Family == FamilySearch)
}
