package main

import (
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/tend"
)

// withMode attaches the permission mode the cached index last read for r: a store record pick returns lacks it.
func withMode(r *tend.Rec) {
	if r.Host != "" || r.Permission != (tend.Permission{}) {
		return
	}
	if idx, err := index.Open(); err == nil {
		r.Permission = idx.Env(r.Provider, r.SessionID).Permission()
	}
}
