package utils

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

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

// TestColorReaderTextOnWriter tests that passed colors are wrapped correctly
func TestColorReaderTextOnWriter(t *testing.T) {
	fakeCmd := exec.Command("echo", "test")
	ro, err := fakeCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	re, err := fakeCmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	var bufout, buferr bytes.Buffer
	fakeNodeName := "fake"

	color := NewColorPicker().NextColor()
	ColorReaderTextOnWriter(ro, &bufout, fakeNodeName, color)
	ColorReaderTextOnWriter(re, &buferr, fakeNodeName, color)

	if err := fakeCmd.Run(); err != nil {
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
