package grpc

import "time"

const (
	DefaultEventTimeout = time.Second

	Pause Event = iota
	Resume
)

type Event int

func (e Event) String() string {
	switch e {
	case Pause:
		return "pause"
	case Resume:
		return "resume"
	default:
		return "unknown"
	}
}
