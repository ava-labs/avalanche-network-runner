package status

// The status of a node.
type Status byte

const (
	// state just after creating the node process.
	// TODO remove?
	Initial Status = iota
	// process has been started and not yet asked to stop or found to be stopped
	Running
	// process has been asked to stop
	Stopping
	// process is verified to be stopped
	Stopped
)
