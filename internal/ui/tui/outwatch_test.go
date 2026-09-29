package tui

import (
	"encoding/json"
	"testing"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/output"
	"github.com/oxsean/fav/internal/wire"
)

func outputPush(evs ...output.Event) wire.Push {
	b, _ := json.Marshal(coord.OutputPush{Events: evs})
	return wire.Push{Method: coord.PushOutput, Params: b}
}

func TestATempEventWithoutTextTakesAwayTheOneWithItsKey(t *testing.T) {
	f := &outFeed{}
	f.take(outputPush(output.Event{ID: "f:0:0", Kind: output.KindSay, Text: "a"}, output.Event{Kind: output.KindSay, Temp: true, Key: "p:m:0", Text: "wri"}))
	f.take(outputPush(output.Event{Kind: output.KindSay, Temp: true, Key: "p:m:0", Text: "writing"}))
	if len(f.events) != 2 || f.events[1].Text != "writing" {
		t.Fatalf("replaced in place: %+v", f.events)
	}
	f.take(outputPush(output.Event{Kind: output.KindSay, Temp: true, Key: "p:m:0"}))
	if len(f.events) != 1 || f.events[0].Text != "a" {
		t.Fatalf("taken away: %+v", f.events)
	}
	f.take(outputPush(output.Event{Kind: output.KindSay, Temp: true, Key: "p:x"}))
	if len(f.events) != 1 {
		t.Fatalf("one never shown is not added: %+v", f.events)
	}
}
