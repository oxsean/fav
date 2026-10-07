package migrate

import "github.com/oxsean/fav/internal/pathmap"

// End is one machine as a path mapping needs it, from its hello (pathmap.End on the wire).
type End struct {
	OS   string `json:"os"`
	Home string `json:"home"`
	Host string `json:"host,omitempty"`
	WSL  bool   `json:"wsl,omitempty"`
}

func (e End) Map() pathmap.End { return pathmap.End{OS: e.OS, Home: e.Home, Host: e.Host, WSL: e.WSL} }

// Peer names the other machine of a handoff or migration as the initiator saw it.
type Peer struct {
	Name     string `json:"name"`     // display only: names differ per viewer and mode
	Endpoint string `json:"endpoint"` // hello.endpoint: what a record matches on
	NodeID   string `json:"node_id,omitempty"`
	End      End    `json:"end"`
}

// Pair is a source directory and its counterpart on the target, given explicitly.
type Pair struct {
	From string `json:"from"`
	To   string `json:"to"`
}
