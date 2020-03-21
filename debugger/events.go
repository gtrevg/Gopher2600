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
//
// *** NOTE: all historical versions of this file, as found in any
// git repository, are also covered by the licence, even when this
// notice is not present ***

package debugger

import (
	"fmt"

	"github.com/jetsetilly/gopher2600/debugger/terminal"
	"github.com/jetsetilly/gopher2600/errors"
	"github.com/jetsetilly/gopher2600/gui"
	"github.com/jetsetilly/gopher2600/playmode"
)

func (dbg *Debugger) guiEventHandler(ev gui.Event) error {
	var err error

	switch ev := ev.(type) {
	case gui.EventQuit:
		dbg.running = false
		return errors.New(errors.UserInterrupt)

	case gui.EventKeyboard:
		var handled bool

		// check playmode key presses first
		handled, err = playmode.KeyboardEventHandler(ev, dbg.vcs)
		if err != nil {
			break // switch ev.(type)
		}

		if !handled {
			if ev.Down && ev.Mod == gui.KeyModNone {
				switch ev.Key {
				case "F12":
					err = dbg.scr.SetFeature(gui.ReqToggleCropping)

				case "F11":
					// toggle debugging colours
					err = dbg.scr.SetFeature(gui.ReqToggleAltColors)
				case "F10":
					// toggle overlay
					err = dbg.scr.SetFeature(gui.ReqToggleOverlay)

				case "=":
					fallthrough // equal sign is the same as plus, for convenience
				case "+":
					// increase scaling
					err = dbg.scr.SetFeature(gui.ReqIncScale)
				case "-":
					// decrease window scanling
					err = dbg.scr.SetFeature(gui.ReqDecScale)
				}
			}
		}

	case gui.EventDbgMouseButton:
		switch ev.Button {
		case gui.MouseButtonRight:
			if ev.Down {
				_, err = dbg.parseInput(fmt.Sprintf("%s sl %d & hp %d", cmdBreak, ev.Scanline, ev.HorizPos), false, false)
				if err == nil {
					dbg.printLine(terminal.StyleFeedbackNonInteractive, "mouse break on sl->%d and hp->%d", ev.Scanline, ev.HorizPos)
				}
			}
		}

	case gui.EventMouseButton:
		_, err := playmode.MouseButtonEventHandler(ev, dbg.vcs, dbg.scr)
		return err

	case gui.EventMouseMotion:
		_, err := playmode.MouseMotionEventHandler(ev, dbg.vcs)
		return err
	}

	// wrap error in GUIEventError
	if err != nil {
		err = errors.New(errors.GUIEventError, err)
	}

	return err

}

// returns true if the terminal needs reading
func (dbg *Debugger) checkEvents(inputter terminal.Input) (bool, error) {
	var err error

	if inputter != nil && inputter.TermReadCheck() {
		return true, nil
	}

	done := false
	for !done {
		// check interrupt channel and run any functions we find in there
		select {
		case <-dbg.events.IntEvents:
			// #ctrlc halt emulation
			if dbg.runUntilHalt {
				// stop emulation at the next step
				dbg.runUntilHalt = false

				// !!TODO: rather than halting immediately set a flag that says to
				// halt at the next manual-break point. if there is no manual break
				// point then stop immediately (or end of current frame might be
				// better)

			} else {
				// runUntilHalt is false which means that the emulation is
				// not running. at this point, an input loop is probably
				// running.
				//
				// note that ctrl-c signals do not always reach
				// this far into the program.  for instance, the colorterm
				// implementation of UserRead() puts the terminal into raw
				// mode and so must handle ctrl-c events differently.

				if dbg.scriptScribe.IsActive() {
					// unlike in the equivalent code in the QUIT command, there's
					// no need to call Rollback() here because the ctrl-c event
					// will not be recorded to the script
					dbg.scriptScribe.EndSession()
				} else {
					dbg.running = false
				}
			}
		case ev := <-dbg.events.GuiEvents:
			err = dbg.guiEventHandler(ev)

		case ev := <-dbg.events.RawEvents:
			ev()

		default:
			done = true
		}
	}

	return false, err
}

// PushRawEvent onto the event queue
func (dbg *Debugger) PushRawEvent(f func()) {
	dbg.events.RawEvents <- f
}
