package status

// The status of a node.
type Status byte

const (
	// Process is running and hasn't been asked to stop.
	Running Status = iota + 1
	// Process has been asked to stop.
	Stopping
	// Process has exited.
	Stopped
)

func (s Status) String() string {
	switch s {
	case Running:
		return "running"
	case Stopping:
		return "stopping"
	case Stopped:
		return "stopped"
	default:
		return "invalid status"
	}
}
