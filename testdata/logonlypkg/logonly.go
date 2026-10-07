// Package logonlypkg logs only through zerolog's global log package and holds
// no zerolog value of its own.
package logonlypkg

import "github.com/rs/zerolog/log"

func Print() {
	log.Print("global") // want `zerolog output is not proven to carry context before Print\(\)`
}
