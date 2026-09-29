package wire

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPushRawArrivesAsPushWould(t *testing.T) {
	opened := make(chan *Stream, 1)
	a, b := Pipe(Options{}, Options{Handler: streamer(opened)})
	defer a.Close()
	defer b.Close()
	w := a.Watch(context.Background(), "hold", nil)
	s := recv(t, opened, "the stream opens")
	params, _ := json.Marshal(map[string]any{"events": []string{"a", "é\n"}})
	if err := s.PushRaw("run.output", params, nil); err != nil {
		t.Fatal(err)
	}
	p, err := w.Next(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Events []string }
	if p.Method != "run.output" || p.Decode(&got) != nil || len(got.Events) != 2 || got.Events[1] != "é\n" {
		t.Fatalf("%s %s", p.Method, p.Params)
	}
}
