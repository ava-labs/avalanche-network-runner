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
	NextColor() logging.Color // get the next color
}

// ColorPickerImpl implements ColorPicker
type ColorPickerImpl struct {
	lock      sync.Mutex
	usedIndex int
}

// NewColorPicker allows to assign a color to different clients
func NewColorPicker() *ColorPickerImpl {
	return &ColorPickerImpl{}
}

// NextColor for a client. If all supportedColors have been assigned,
// it starts over with the first color
// Doesn't need to be exported for the current use case but could be useful later
func (c *ColorPickerImpl) NextColor() logging.Color {
	c.lock.Lock()
	defer c.lock.Unlock()

	if c.usedIndex > len(supportedColors)-1 {
		c.usedIndex = 0
	}
	pick := supportedColors[c.usedIndex]
	c.usedIndex++

	return pick
}

// ColorReaderTextOnWriter scans the given `reader` for text and prepends the `wrapText` parameter to it.
// It also assigns the passed color to the text and writes it to the given `writer`
func ColorReaderTextOnWriter(reader io.Reader, writer io.Writer, wrapText string, color logging.Color) {
	scanner := bufio.NewScanner(reader)
	go func(scanner *bufio.Scanner) {
		// we should not need any go routine control here:
		// when the program exits, `Scan()` will hit an EOF and return false,
		// and therefore the routine terminates
		for scanner.Scan() {
			txt := color.Wrap(fmt.Sprintf("[%s] %s\n", wrapText, scanner.Text()))
			if _, err := writer.Write([]byte(txt)); err != nil {
				// this error handling is required for linting, but we can ignore this situation as it is highly unlikely
				fmt.Println("failed to write wrapped text to writer")
			}
		}
	}(scanner)
}
