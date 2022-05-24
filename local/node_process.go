package local

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/shirou/gopsutil/process"
)

// interface compliance
var (
	_ node.Node   = (*localNode)(nil)
	_ NodeProcess = (*nodeProcessImpl)(nil)
)

// NodeProcess as an interface so we can mock running
// AvalancheGo binaries in tests
type NodeProcess interface {
	// Start this process.
	// Returns error if not called after instantiation.
	// returns error if the process already started.
	// If process stops without a previous call to Stop(), send notification msg over given channel.
	Start(chan network.UnexpectedNodeStopMsg) error
	// Send a SIGTERM to this process.
	// If ctx is cancelled, send SIGKILL to this process and descendants.
	// Returns error if called before Start.
	// Returns nil if the process is already stopping/stopped.
	Stop(ctx context.Context) error
	// Returns when the process finishes exiting.
	// Returns error if called before Start.
	// Returns nil if already waited for the process.
	Wait() error
	// Returns the state of the process
	Status() status.Status
}

type nodeProcessImpl struct {
	name string
	lock sync.RWMutex
	cmd  *exec.Cmd
	// maintains process state Initial/Started/Stopping/Stopped
	state status.Status
	// closed when the AvalancheGo process returns
	closedOnStop chan struct{}
	// wait return
	waitReturn error
}

func newNodeProcessImpl(name string, cmd *exec.Cmd) *nodeProcessImpl {
	return &nodeProcessImpl{
		name:         name,
		cmd:          cmd,
		closedOnStop: make(chan struct{}),
	}
}

// to be called only on Initial state
func (p *nodeProcessImpl) Start(unexpectedStopCh chan network.UnexpectedNodeStopMsg) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != status.Initial {
		return errors.New("start called on invalid state")
	}
	if err := p.cmd.Start(); err != nil {
		return err
	}
	p.state = status.Running
	go func() {
		// Wait4 to avoid race conditions on stdout/stderr pipes
		var waitStatus syscall.WaitStatus
		var rusage syscall.Rusage
		_, err := syscall.Wait4(p.cmd.Process.Pid, &waitStatus, 0, &rusage)
		p.lock.Lock()
		state := p.state
		p.state = status.Stopped
		p.waitReturn = err
		close(p.closedOnStop)
		p.lock.Unlock()
		if state != status.Stopping {
			unexpectedStopCh <- network.UnexpectedNodeStopMsg{
				NodeName: p.name,
				ExitCode: waitStatus.ExitStatus(),
			}
		}
	}()
	return nil
}

func (p *nodeProcessImpl) Wait() error {
	p.lock.RLock()
	state := p.state
	p.lock.RUnlock()
	if state == status.Initial {
		return errors.New("wait called on invalid state")
	}
	<-p.closedOnStop
	p.lock.Lock()
	defer p.lock.Unlock()
	// only return wait err first time is called
	waitReturn := p.waitReturn
	p.waitReturn = nil
	return waitReturn
}

// if context is cancelled, assumes a failure in termination
// and uses SIGKILL over process and descendants
func (p *nodeProcessImpl) Stop(ctx context.Context) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state == status.Initial {
		return errors.New("stop called on invalid state")
	}
	if p.state != status.Running {
		return nil
	}
	stopResult := p.cmd.Process.Signal(os.Interrupt)
	p.state = status.Stopping
	go func() {
		select {
		case <-ctx.Done():
			_ = killDescendants(int32(p.cmd.Process.Pid))
			_ = p.cmd.Process.Signal(os.Kill)
		case <-p.closedOnStop:
		}
	}()
	return stopResult
}

func (p *nodeProcessImpl) Status() status.Status {
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
