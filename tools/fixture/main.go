// Command fixture writes the synthetic test dataset (internal/fixture) and launchers that run fav against it.
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
	bin := flag.String("fav", "", "fav binary the launchers run (default: fav next to the launcher, else PATH)")
	flag.Parse()
	if *out == "" {
		fmt.Fprintln(os.Stderr, "usage: fixture -o DIR [-fav PATH]")
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
		fmt.Printf("%-18s %-6s %s listed=%t agent=%t fav=%t\n", s.Name, s.Provider, s.ID, s.Listed, s.Agent, s.Favorite)
	}
}
