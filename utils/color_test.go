package utils

import (
	"os"
	"os/exec"
	"testing"
)

// TestColorAssignment tests that each color assignment is different and that it "wraps"
func TestColorAssignment(t *testing.T) {
	maxlen := len(supportedColors)
	// iterate 3 times to make sure that it "wraps" again to the beginning
	// of the supportedColors slice
	for i := 0; i < 3*maxlen; i++ {
		c := ColorPicker.AssignNewColor()
		if c != supportedColors[i%maxlen] {
			// due to the actual nature of "color" (a string interpreted by the terminal)
			// printing the color string doesn't actually show anything
			t.Fatalf("expected different color")
		}
	}
}

// TestScanProcessForChildText tests that passing an existing color returns the same
func TestScanProcessForChildText(t *testing.T) {
	fakeCmd := exec.Command("ls")
	ro, err := fakeCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	wo := os.Stdout
	c1 := ScanChildProcessForText(ro, wo, "fake", "")

	re, err := fakeCmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	we := os.Stderr
	c2 := ScanChildProcessForText(re, we, "fake", c1)

	if c1 != c2 {
		t.Fatal("expected colors to be equal, but they are not")
	}

	// test that passing another color returns a different one again
	ctrlCmd := exec.Command("any")
	roc, err := ctrlCmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c3 := ScanChildProcessForText(roc, wo, "more fake", "")
	if c3 == c1 {
		t.Fatal("expected control color to be different, but it is equal")
	}
}
