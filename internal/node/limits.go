package node

// What a supervisor holds in memory for its agent.
const (
	maxSpoolOut = 64 << 20 // stdout read from the pipe, not yet logged and read
	maxSpoolErr = 16 << 20 // stderr read from the pipe, not yet logged
	maxQueuedIn = 16 << 20 // what waits to be written to a stream run's stdin; a send beyond it fails
)
