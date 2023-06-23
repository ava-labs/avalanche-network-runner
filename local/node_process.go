package local

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"

	"github.com/ava-labs/avalanche-network-runner/network/node"
	"github.com/ava-labs/avalanche-network-runner/network/node/status"
	"github.com/ava-labs/avalanche-network-runner/utils"
	"github.com/ava-labs/avalanchego/utils/logging"
	"github.com/shirou/gopsutil/process"
	"go.uber.org/zap"
)

var _ NodeProcess = (*nodeProcess)(nil)

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

// NodeProcessCreator is an interface for new node process creation
type NodeProcessCreator interface {
	GetNodeVersion(config node.Config) (string, error)
	NewNodeProcess(config node.Config, args ...string) (NodeProcess, error)
}

type nodeProcessCreator struct {
	log logging.Logger
	// If this node's stdout or stderr are redirected, [colorPicker] determines
	// the color of logs printed to stdout and/or stderr
	colorPicker utils.ColorPicker
	// If this node's stdout is redirected, it will be to here.
	// In practice this is usually os.Stdout, but for testing can be replaced.
	stdout io.Writer
	// If this node's stderr is redirected, it will be to here.
	// In practice this is usually os.Stderr, but for testing can be replaced.
	stderr io.Writer
}

// NewNodeProcess creates a new process of the passed binary
// If the config has redirection set to `true` for either StdErr or StdOut,
// the output will be redirected and colored
func (npc *nodeProcessCreator) NewNodeProcess(config node.Config, args ...string) (NodeProcess, error) {
	// Start the AvalancheGo node and pass it the flags defined above
	cmd := exec.Command(config.BinaryPath, args...) //nolint
	// assign a new color to this process (might not be used if the config isn't set for it)
	color := npc.colorPicker.NextColor()
	// Optionally redirect stdout and stderr
	if config.RedirectStdout {
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			return nil, fmt.Errorf("couldn't create stdout pipe: %w", err)
		}
		// redirect stdout and assign a color to the text
		utils.ColorAndPrepend(stdout, npc.stdout, config.Name, color)
	}
	if config.RedirectStderr {
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return nil, fmt.Errorf("couldn't create stderr pipe: %w", err)
		}
		// redirect stderr and assign a color to the text
		utils.ColorAndPrepend(stderr, npc.stderr, config.Name, color)
	}
	return newNodeProcess(config.Name, npc.log, cmd)
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
}

func newNodeProcess(name string, log logging.Logger, cmd *exec.Cmd) (*nodeProcess, error) {
	np := &nodeProcess{
		name:         name,
		log:          log,
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
		p.log.Debug("node returned error on wait", zap.String("node", p.name), zap.Error(err))
	}

	p.log.Debug("node process finished", zap.String("node", p.name))

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
		p.log.Warn("sending SIGINT errored", zap.Error(err))
	}

	select {
	case <-ctx.Done():
		p.log.Warn("context cancelled while waiting for node to stop", zap.String("node", p.name))
		killDescendants(int32(proc.Pid), p.log)
		if err := proc.Signal(os.Kill); err != nil {
			p.log.Warn("sending SIGKILL errored", zap.Error(err))
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
	defer p.lock.RUnlock()

	return p.state
}

func killDescendants(pid int32, log logging.Logger) {
	procs, err := process.Processes()
	if err != nil {
		log.Warn("couldn't get processes", zap.Error(err))
		return
	}
	for _, proc := range procs {
		ppid, err := proc.Ppid()
		if err != nil {
			log.Warn("couldn't get process ID", zap.Error(err))
			continue
		}
		if ppid != pid {
			continue
		}
		killDescendants(proc.Pid, log)
		if err := proc.Kill(); err != nil {
			log.Warn("error killing process", zap.Int32("pid", proc.Pid), zap.Error(err))
		}
	}
}

// GetNodeVersion gets the version of the executable as per --version flag
func (*nodeProcessCreator) GetNodeVersion(config node.Config) (string, error) {
	// Start the AvalancheGo node and pass it the --version flag
	out, err := exec.Command(config.BinaryPath, "--version").Output() //nolint
	if err != nil {
		return "", err
	}
	return string(out), nil
}

type fakeNodeProcess struct {
	name string
	log  logging.Logger
}

func newFakeNodeProcess(name string, log logging.Logger) (*fakeNodeProcess, error) {
	np := &fakeNodeProcess{
		name: name,
		log:  log,
	}
	return np, nil
}

func (p *fakeNodeProcess) Stop(ctx context.Context) int {
	return 0
}

func (p *fakeNodeProcess) Status() status.Status {
	return status.Running
}
