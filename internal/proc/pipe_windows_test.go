package proc

import (
	"testing"
	"time"
)

func TestAPipeTakesMegabytesWithoutAReader(t *testing.T) {
	r, w, err := Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	done := make(chan error, 1)
	go func() {
		_, err := w.Write(make([]byte, 4<<20))
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the write waits for a reader")
	}
	w.Close()
}
