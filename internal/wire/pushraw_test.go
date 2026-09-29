package wire

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestPushRawArrivesAsPushWould(t *testing.T) {
	opened := make(chan *Stream, 1)
	a, b := Pipe(Options{}, Options{Handler: streamer(opened)})
	defer a.Close()
	defer b.Close()
	w := a.Watch(context.Background(), "hold", nil)
	s := <-opened
	params, _ := json.Marshal(map[string]any{"events": []string{"a", "é\n"}})
	if err := s.PushRaw("run.output", params, nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p, err := w.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var got struct{ Events []string }
	if p.Method != "run.output" || p.Decode(&got) != nil || len(got.Events) != 2 || got.Events[1] != "é\n" {
		t.Fatalf("%s %s", p.Method, p.Params)
	}
}
