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
	maxSentField = 16 << 10    // a longer string in a line of output is sent cut to this
	maxSentLine  = 1 << 20     // a longer line is sent as only its first maxSentField bytes
	maxMarksRead = 8 << 20     // what one read of marks.jsonl takes at most
)

// A follow of a run's output.
const (
	followEvery   = 200 * time.Millisecond // how often it looks at the log
	maxFollowPush = 256 << 10              // the lines of one push, about
)
