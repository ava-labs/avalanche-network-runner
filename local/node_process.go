package local

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"syscall"

	"github.com/ava-labs/avalanche-network-runner/network"
	"github.com/ava-labs/avalanche-network-runner/network/node"
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
	// Returns true if the process is executing.
	// Returns false if the process has been stopped.
	// Returns false if the process has not been started.
	Alive() bool
}

type nodeProcessImpl struct {
	name string
	lock sync.RWMutex
	cmd  *exec.Cmd
	// to notify Wait() on process stop, and give wait return
	waitReturnCh chan error
	// to notify user of not asked process stops
	unexpectedStopCh chan network.UnexpectedNodeStopMsg
	// maintains process state Initial/Started/Stopping/Stopped/Waited
	state processState
	// to notify SIGKILL goroutine of process end
	closeOnStop chan struct{}
}

type processState int

const (
	Initial processState = iota
	Started
	Stopping
	Stopped
	Waited
)

func newNodeProcessImpl(name string, cmd *exec.Cmd) *nodeProcessImpl {
	return &nodeProcessImpl{
		name:         name,
		cmd:          cmd,
		waitReturnCh: make(chan error, 1),
		closeOnStop:  make(chan struct{}),
	}
}

// to be called only on Initial state
func (p *nodeProcessImpl) Start(unexpectedStopCh chan network.UnexpectedNodeStopMsg) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state != Initial {
		return errors.New("start called on invalid state")
	}
	p.unexpectedStopCh = unexpectedStopCh
	if err := p.cmd.Start(); err != nil {
		return err
	}
	p.state = Started
	go func() {
		// Wait4 to avoid race conditions on stdout/stderr pipes
		var status syscall.WaitStatus
		var rusage syscall.Rusage
		_, err := syscall.Wait4(p.cmd.Process.Pid, &status, 0, &rusage)
		p.waitReturnCh <- err
		p.lock.Lock()
		state := p.state
		p.state = Stopped
		close(p.closeOnStop)
		p.lock.Unlock()
		if state != Stopping {
			p.unexpectedStopCh <- network.UnexpectedNodeStopMsg{
				Name:     p.name,
				ExitCode: status.ExitStatus(),
			}
		}
	}()
	return nil
}

func (p *nodeProcessImpl) Wait() error {
	p.lock.RLock()
	state := p.state
	p.lock.RUnlock()
	if state == Initial {
		return errors.New("wait called on invalid state")
	}
	if state == Waited {
		return nil
	}
	waitReturn := <-p.waitReturnCh
	p.lock.Lock()
	defer p.lock.Unlock()
	p.state = Waited
	return waitReturn
}

// if context is cancelled, assumes a failure in termination
// and uses SIGKILL over process and descendants
func (p *nodeProcessImpl) Stop(ctx context.Context) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	if p.state == Initial {
		return errors.New("stop called on invalid state")
	}
	if p.state != Started {
		return nil
	}
	stopResult := p.cmd.Process.Signal(syscall.SIGTERM)
	p.state = Stopping
	go func() {
		select {
		case <-ctx.Done():
			_ = killDescendants(int32(p.cmd.Process.Pid))
			_ = p.cmd.Process.Signal(syscall.SIGKILL)
		case <-p.closeOnStop:
		}
	}()
	return stopResult
}

func (p *nodeProcessImpl) Alive() bool {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return p.state == Started || p.state == Stopping
}
