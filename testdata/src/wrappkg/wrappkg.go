package wrappkg

import (
	"os"

	"github.com/rs/zerolog"
)

func NewLogger() zerolog.Logger {
	return zerolog.New(os.Stdout)
}

func Info() *zerolog.Event {
	return NewLogger().Info()
}
