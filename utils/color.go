package utils

import (
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

var ColorPicker = &color{
	usedColors: make([]logging.Color, 0),
}

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
