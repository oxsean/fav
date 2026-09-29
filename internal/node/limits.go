package node

import "time"

// What a supervisor holds in memory for its agent.
const (
	maxSpoolOut  = 64 << 20    // stdout read from the pipe, not yet logged and read
	maxSpoolErr  = 16 << 20    // stderr read from the pipe, not yet logged
	maxQueuedIn  = 16 << 20    // what waits to be written to a stream run's stdin; a send beyond it fails
	maxLine      = 32 << 20    // a longer stdout line is logged in pieces as it comes, and not read
	halfLineWait = time.Second // stderr's half line is logged once it waited this long for its end
	halfLineMax  = 64 << 10    // or grew this long
)
