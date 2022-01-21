package utils

import (
	"bytes"
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ava-labs/avalanchego/utils/logging"
)

// TestColorAssignment tests that each color assignment is different and that it "wraps"
func TestColorAssignment(t *testing.T) {
	maxlen := len(supportedColors)
	c := NewColorPicker()
	// iterate 3 times to make sure that it "wraps" again to the beginning
	// of the supportedColors slice
	for i := 0; i < 3*maxlen; i++ {
		color := c.NextColor()
		if color != supportedColors[i%maxlen] {
			// due to the actual nature of "color" (a string interpreted by the terminal)
			// printing the color string doesn't actually show anything
			t.Fatalf("expected different color")
		}
	}
}

// syncedBuffer writes to a channel after the Write operation
// so that we are notified in testing when the value arrived
type syncedBuffer struct {
	bytes.Buffer
	sync chan struct{}
}

// Write calls the embedded `Buffer.Write` but also
// writes to the channel for notification
func (s *syncedBuffer) Write(b []byte) (int, error) {
	w, err := s.Buffer.Write(b)
	s.sync <- struct{}{}
	return w, err
}

// TestColorAndPrepend tests that passed colors are wrapped correctly
func TestColorAndPrepend(t *testing.T) {
	fakeCmd := exec.Command("echo", "test")
	ro, err := fakeCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	re, err := fakeCmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	bufout := &syncedBuffer{
		sync: make(chan struct{}),
	}

	// for the stderr case we don't need a syncedBuffer because
	// nothing should be written to stderr in this test case
	var buferr bytes.Buffer
	fakeNodeName := "fake"

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := make(chan struct{})
	errCh := make(chan error)
	go func() {
		// wait until we got the value
		start <- struct{}{}
		select {
		case <-ctx.Done():
			errCh <- ctx.Err()
		case <-bufout.sync:
			errCh <- nil
		}
	}()
	color := NewColorPicker().NextColor()
	ColorAndPrepend(ro, bufout, fakeNodeName, color)
	ColorAndPrepend(re, &buferr, fakeNodeName, color)
	<-start
	if err := fakeCmd.Run(); err != nil {
		t.Fatal(err)
	}

	if err := <-errCh; err != nil {
		t.Fatal(err)
	}
	res := bufout.String()
	if !strings.Contains(res, "test") {
		t.Fatal("expected writer to contain the string `test`, but it didn't")
	}

	// 4 is []<space>\n
	expLen := len("test") + len(color) + len(fakeNodeName) + 4 + len(logging.Reset)
	if len(res) != expLen {
		t.Fatalf("expected lengh to be %d, but was %d", expLen, len(res))
	}

	res = buferr.String()
	// nothing should have been written to stderr
	expLen = 0
	if len(res) != expLen {
		t.Fatalf("expected lengh to be %d, but was %d", expLen, len(res))
	}
}
