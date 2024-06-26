// This file is part of Gopher2600.
//
// Gopher2600 is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// Gopher2600 is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with Gopher2600.  If not, see <https://www.gnu.org/licenses/>.

// Package wrangler keeps track of the running bot and handles the activating
// and termination of running bots.
package wrangler

import (
	"fmt"

	"github.com/jetsetilly/gopher2600/bots"
	"github.com/jetsetilly/gopher2600/bots/chess"
	"github.com/jetsetilly/gopher2600/bots/spacejockey"
	"github.com/jetsetilly/gopher2600/logger"
)

// Bots keeps track of the running bot and handles loading and termination.
type Bots struct {
	input bots.Input
	tv    bots.TV

	running bots.Bot
}

// NewBots is the preferred method of initialisation for the Bots type.
func NewBots(input bots.Input, tv bots.TV) *Bots {
	return &Bots{
		input: input,
		tv:    tv,
	}
}

// ActivateBot uses the cartridge hash value to and loads any available bot.
func (b *Bots) ActivateBot(cartHash string) (*bots.Feedback, error) {
	b.Quit()

	var err error

	switch cartHash {
	case "043ef523e4fcb9fc2fc2fda21f15671bf8620fc3":
		// NTSC version of VideoChess
		b.running, err = chess.NewVideoChess(b.input, b.tv, "NTSC")
		if err != nil {
			return nil, fmt.Errorf("bots: %w", err)
		}
		logger.Logf(logger.Allow, "bots", "%s started", b.running.BotID())

	case "5bdd8af54020fa43065750bd4239a497695d403b":
		// NTSC version of SpaceJockey
		b.running, err = spacejockey.NewSpaceJockey(b.input, b.tv, "NTSC")
		if err != nil {
			return nil, fmt.Errorf("bots: %w", err)
		}
		logger.Logf(logger.Allow, "bots", "%s started", b.running.BotID())

	default:
		return nil, nil
	}

	return b.running.Feedback(), nil
}

// Quit stops execution of running bots.
func (b *Bots) Quit() {
	if b.running != nil {
		b.running.Quit()
		logger.Logf(logger.Allow, "bots", "%s finished", b.running.BotID())
		b.running = nil
	}
}
