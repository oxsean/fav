// Command fixture writes the synthetic test dataset (internal/fixture) and launchers that run tend against it, with
// herdr, claude and codex stubbed out.
package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/oxsean/fav/internal/fixture"
)

func main() {
	out := flag.String("o", "", "directory to create (must not hold a dataset yet)")
	bin := flag.String("tend", "", "tend binary the launchers run (default: tend next to the launcher, else PATH)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: fixture -o DIR [-tend PATH]")
		os.Exit(2)
	}
	d, err := fixture.Build(*out, time.Now())
	if err == nil {
		err = d.WriteLaunchers(*bin)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, s := range d.Sessions {
		fmt.Printf("%-18s %-6s %s listed=%t agent=%t favorite=%t\n", s.Name, s.Provider, s.ID, s.Listed, s.Agent, s.Favorite)
	}
}
