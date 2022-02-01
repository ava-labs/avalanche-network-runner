package utils

import (
	"bufio"
	"fmt"
	"io"
	"sync"

	"github.com/ava-labs/avalanchego/utils/logging"
)

var supportedColors = []logging.Color{
	logging.Cyan,
	logging.LightBlue,
	logging.LightGray,
	logging.LightGreen,
	logging.LightPurple,
	logging.LightCyan,
	logging.Purple,
	logging.Yellow,
}

// ColorPicker allows to assign a new color
type ColorPicker interface {
	// get the next color
	NextColor() logging.Color
}

// colorPicker implements ColorPicker
type colorPicker struct {
	lock      sync.Mutex
	usedIndex int
}

// NewColorPicker allows to assign a color to different clients
func NewColorPicker() *colorPicker {
	return &colorPicker{}
}

// NextColor for a client. If all supportedColors have been assigned,
// it starts over with the first color.
func (c *colorPicker) NextColor() logging.Color {
	c.lock.Lock()
	defer c.lock.Unlock()

	color := supportedColors[c.usedIndex%len(supportedColors)]
	c.usedIndex++
	return color
}

// ColorAndPrepend reads each line from [reader], prepends it
// with [prependText] and colors it with [color], and then prints the
// prepended/colored line to [writer].
func ColorAndPrepend(reader io.Reader, writer io.Writer, prependText string, color logging.Color) {
	scanner := bufio.NewScanner(reader)
	go func(scanner *bufio.Scanner) {
		// we should not need any go routine control here:
		// when the program exits, Scan() will hit an EOF and return false,
		// and therefore the routine terminates
		for scanner.Scan() {
			txt := color.Wrap(fmt.Sprintf("[%s] %s\n", prependText, scanner.Text()))
			_, _ = writer.Write([]byte(txt))
		}
	}(scanner)
}
