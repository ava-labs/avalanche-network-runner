package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"

	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/shirou/gopsutil/process"
)

var _ NodeProcess = (*nodeProcess)(nil)

// NodeProcess as an interface so we can mock running
// AvalancheGo binaries in tests
type NodeProcess interface {
	// Sends a SIGTINT to this process and returns when the process
	// has exited or when [ctx] is cancelled.
	// If [ctx] is cancelled, sends a SIGKILL to this process and descendants
	// and returns [ctx.Err()].
	// Otherwise, returns nil when the process exits.
	// Subsequent calls to [Stop] always return nil.
	Stop(ctx context.Context) error
	// Returns when the process exits.
	// Returns an error if there was a process-level problem
	// (i.e. the process couldn't run) or if the process's
	// exit code was non-zero.
	// Subsequent calls to [Wait] always return the same value.
	Wait() error
	// Returns the status of the process.
	Status() status.Status
}

type nodeProcess struct {
	name string
	lock sync.RWMutex
	cmd  *exec.Cmd
	// Process status
	state status.Status
	// Closed when the process exits.
	// If closed, [onExitErr] and [exitCode] are guaranteed to be set.
	closedOnStop chan struct{}
	// Set when the process exits.
	// Non-nil if there was a process-level problem or
	// if the process had a non-zero exit code.
	onExitErr error
	// Set when the process exits.
	exitCode int
}

func newNodeProcess(name string, cmd *exec.Cmd) (*nodeProcess, error) {
	np := &nodeProcess{
		name:         name,
		cmd:          cmd,
		closedOnStop: make(chan struct{}),
	}
	return np, np.start()
}

// Start this process.
// Must only be called once.
func (p *nodeProcess) start() error {
	p.lock.Lock()
	defer p.lock.Unlock()

	p.state = status.Running
	if err := p.cmd.Start(); err != nil {
		p.state = status.Stopped
		return fmt.Errorf("couldn't start process: %w", err)
	}

	go func() {
		// Wait for the process to exit.
		err := p.cmd.Wait()
		p.lock.Lock()
		p.state = status.Stopped
		p.onExitErr = err
		p.exitCode = p.cmd.ProcessState.ExitCode()
		close(p.closedOnStop)
		p.lock.Unlock()
	}()
	return nil
}

func (p *nodeProcess) Wait() error {
	<-p.closedOnStop
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.onExitErr
}

func (p *nodeProcess) Stop(ctx context.Context) error {
	p.lock.Lock()
	defer p.lock.Unlock()

	if p.state != status.Running {
		return nil
	}
	p.state = status.Stopping

	// There isn't anything to do with this error.
	// Either the process got the signal, in which case
	// we should wait until it exits, or it didn't,
	// in which case we should wait until the context
	// is cancelled and then try to SIGKILL it.
	_ = p.cmd.Process.Signal(os.Interrupt)

	select {
	case <-ctx.Done():
		_ = killDescendants(int32(p.cmd.Process.Pid))
		_ = p.cmd.Process.Signal(os.Kill)
		return ctx.Err()
	case <-p.closedOnStop:
		return nil
	}
}

func (p *nodeProcess) Status() status.Status {
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.state
}

func killDescendants(pid int32) error {
	procs, err := process.Processes()
	if err != nil {
		return err
	}
	for _, proc := range procs {
		ppid, err := proc.Ppid()
		if err != nil {
			return err
		}
		if ppid != pid {
			continue
		}
		if err := killDescendants(proc.Pid); err != nil {
			return err
		}
		_ = proc.Kill()
	}
	return nil
}
