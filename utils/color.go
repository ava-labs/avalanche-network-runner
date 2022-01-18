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

type color struct {
	lock       sync.Mutex
	usedColors []logging.Color
}

// ColorPicker allows to assign a color to different clients
var ColorPicker = &color{
	usedColors: make([]logging.Color, 0),
}

// AssignNewColor to a client. If all supportedColors have been assigned,
// it starts over with the first color
// Doesn't need to be exported for the current use case but could be useful later
func (c *color) AssignNewColor() logging.Color {
	c.lock.Lock()
	defer c.lock.Unlock()

	if len(c.usedColors) == len(supportedColors) {
		c.usedColors = make([]logging.Color, 0)
	}

	pick := supportedColors[len(c.usedColors)]
	c.usedColors = append(c.usedColors, pick)

	return pick
}

// ScanChildProcessForText scans the given `reader` for text and prepends the `wrapText` parameter to it.
// It also assigns a new color if the `assignedColor` is of zero value
func ScanChildProcessForText(reader io.Reader, writer io.Writer, wrapText string, assignedColor logging.Color) logging.Color {
	stdoutscanner := bufio.NewScanner(reader)
	if assignedColor == "" {
		assignedColor = ColorPicker.AssignNewColor()
	}
	go func(scanner *bufio.Scanner) {
		// we should not need any go routine control here:
		// when the program exits, `Scan()` will hit an EOF and return false,
		// and therefore the routine terminates
		for scanner.Scan() {
			txt := assignedColor.Wrap(fmt.Sprintf("[%s] %s\n", wrapText, scanner.Text()))
			if _, err := writer.Write([]byte(txt)); err != nil {
				// TODO better handling required? Failing and returning doesn't seem quite right either
				fmt.Println("failed to write wrapped text to writer")
			}
		}
	}(stdoutscanner)

	return assignedColor
}
