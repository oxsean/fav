package coord

const (
	stateQueue    = 256     // envelopes a state stream may fall behind before it ends as lagged
	maxReplay     = 10000   // envelopes state.watch replays after after_seq; more come as a snapshot
	snapshotBatch = 1 << 20 // bytes of one snapshot push
)
