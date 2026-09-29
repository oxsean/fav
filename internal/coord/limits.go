package coord

import "time"

const (
	stateQueue    = 256     // envelopes a state stream may fall behind before it ends as lagged
	maxReplay     = 10000   // envelopes state.watch replays after after_seq; more come as a snapshot
	snapshotBatch = 1 << 20 // bytes of one snapshot push
)

// The output hubs (output.go).
const (
	maxHubs       = 64                     // runs followed at once; another watch is busy
	outputBatch   = 100 * time.Millisecond // how often a hub sends what came
	maxOutputPush = 256 << 10              // bytes of events in one push
	hubEvents     = 2000                   // a hub keeps the batches of this many events
	hubBytes      = 1 << 20                // or of this many bytes, whichever is less
	hubLinger     = 10 * time.Second       // a hub nobody watches is kept this long
)
