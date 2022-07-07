package local

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/shirou/gopsutil/process"
)

var _ NodeProcess = (*nodeProcess)(nil)

const firstStatusCallWait = time.Second

// NodeProcess as an interface so we can mock running
// AvalancheGo binaries in tests
type NodeProcess interface {
	// Sends a SIGINT to this process and returns the process's
	// exit code.
	// If [ctx] is cancelled, sends a SIGKILL to this process and descendants.
	// We assume sending a SIGKILL to a process will always successfully kill it.
	// Subsequent calls to [Stop] have no effect.
	Stop(ctx context.Context) int
	// Returns the status of the process.
	Status() status.Status
}

type nodeProcess struct {
	name string
	log  logging.Logger
	lock sync.RWMutex
	cmd  *exec.Cmd
	// Process status
	state status.Status
	// Closed when the process exits.
	closedOnStop chan struct{}
	// on first status call, wait some time to give time to the process to fail on common startup errors
	firstStatusCall bool
}

func newNodeProcess(name string, log logging.Logger, cmd *exec.Cmd) (*nodeProcess, error) {
	np := &nodeProcess{
		name:            name,
		log:             log,
		cmd:             cmd,
		closedOnStop:    make(chan struct{}),
		firstStatusCall: true,
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
		close(p.closedOnStop)
		return fmt.Errorf("couldn't start process: %w", err)
	}

	go p.awaitExit()
	return nil
}

// Wait for the process to exit.
// When it does, update the state and close [p.closedOnStop]
func (p *nodeProcess) awaitExit() {
	if err := p.cmd.Wait(); err != nil {
		p.log.Debug("node %q returned error on wait: %s", p.name, err)
	}

	p.lock.Lock()
	defer p.lock.Unlock()

	p.state = status.Stopped
	close(p.closedOnStop)
}

func (p *nodeProcess) Stop(ctx context.Context) int {
	p.lock.Lock()

	// The process is already stopped.
	if p.state == status.Stopped {
		exitCode := p.cmd.ProcessState.ExitCode()
		p.lock.Unlock()
		return exitCode
	}

	// There's another call to Stop executing right now.
	// Wait for it to finish.
	if p.state == status.Stopping {
		p.lock.Unlock()
		<-p.closedOnStop
		p.lock.RLock()
		defer p.lock.RUnlock()

		return p.cmd.ProcessState.ExitCode()
	}

	p.state = status.Stopping
	proc := p.cmd.Process
	// We have to unlock here so that [p.awaitExit] can grab the lock
	// and close [p.closedOnStop].
	p.lock.Unlock()

	if err := proc.Signal(os.Interrupt); err != nil {
		p.log.Warn("sending SIGINT errored: %w", err)
	}

	select {
	case <-ctx.Done():
		p.log.Warn("context cancelled while waiting for node %q to stop", p.name)
		killDescendants(int32(proc.Pid), p.log)
		if err := proc.Signal(os.Kill); err != nil {
			p.log.Warn("sending SIGKILL errored: %w", err)
		}
	case <-p.closedOnStop:
	}

	<-p.closedOnStop
	p.lock.RLock()
	defer p.lock.RUnlock()

	return p.cmd.ProcessState.ExitCode()

}

func (p *nodeProcess) Status() status.Status {
	p.lock.RLock()
	if p.firstStatusCall {
		p.lock.RUnlock()
		time.Sleep(firstStatusCallWait)
	}

	p.lock.RLock()
	defer p.lock.RUnlock()
	p.firstStatusCall = false
	return p.state
}

func killDescendants(pid int32, log logging.Logger) {
	procs, err := process.Processes()
	if err != nil {
		log.Warn("couldn't get processes: %s", err)
		return
	}
	for _, proc := range procs {
		ppid, err := proc.Ppid()
		if err != nil {
			log.Warn("couldn't get process ID: %s", err)
			continue
		}
		if ppid != pid {
			continue
		}
		killDescendants(proc.Pid, log)
		if err := proc.Kill(); err != nil {
			log.Warn("error killing process %d: %s", proc.Pid, err)
		}
	}
}
