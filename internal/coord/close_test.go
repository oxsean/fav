package coord

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/agent"
	"github.com/oxsean/fav/internal/node"
	"github.com/oxsean/fav/internal/remote"
	"github.com/oxsean/fav/internal/wire"
)

// A connection to this machine's node that Close does not hold (a dial under way) keeps calling while Close waits for
// the call already being answered: Close refuses the new calls instead of counting them.
func TestCloseTakesNoNewLocalCallWhileItWaits(t *testing.T) {
	home := t.TempDir()
	n := node.New(home)
	answering, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	n.Probe = func(string) agent.Check {
		once.Do(func() { close(answering) })
		<-release
		return agent.Check{}
	}
	c, err := Open(Options{Home: home, Version: "test", Node: n, Sessions: remote.NewLocal("test")})
	if err != nil {
		t.Fatal(err)
	}
	conn := c.local()
	defer conn.Close()
	go conn.Call(context.Background(), node.MAgents, node.ChecksParams{Fresh: true}, nil)
	<-answering
	closed := make(chan struct{})
	go func() { c.Close(); close(closed) }()
	const callers = 4
	refused := make(chan error, callers)
	for range callers {
		go func() {
			for {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				err := conn.Call(ctx, remote.MHello, remote.HelloParams{Proto: wire.Proto, Role: "coordinator"}, nil)
				cancel()
				if err != nil {
					refused <- err
					return
				}
			}
		}()
	}
	var errs []error
	select {
	case err := <-refused:
		errs = append(errs, err)
	case <-time.After(time.Second):
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(4 * time.Second):
		t.Fatal("Close waited out its bound after the call it waited for ended")
	}
	for len(errs) < callers {
		select {
		case err := <-refused:
			errs = append(errs, err)
		case <-time.After(2 * time.Second):
			t.Fatalf("the node answered a new call after Close: %d of %d callers refused", len(errs), callers)
		}
	}
	for _, err := range errs {
		if wire.Code(err) != wire.CodeClosed {
			t.Fatalf("a call after Close began: %v", err)
		}
	}
}
