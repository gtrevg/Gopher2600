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

package sdlimgui

import (
	"fmt"

	"github.com/inkyblackness/imgui-go/v4"
)

// the window type represents all the windows used in the sdlimgui.
type window interface {
	// initialisation function. by the first call to manager.draw()
	init()

	// id should return a unique identifier for the window. note that the
	// window title and any menu entry do not have to have the same value as
	// the id() but it can.
	id() string
}

type windowGeometry interface {
	pos() imgui.Vec2
	size() imgui.Vec2
}

// size and position of the window. is embedded in playmodeWin and debuggerWin
// interfaces. for window type that implent interfaces then windowGeom
// method calls will need to disambiguate which geometry to use
type windowGeom struct {
	windowPos  imgui.Vec2
	windowSize imgui.Vec2
}

func (g *windowGeom) update() {
	g.windowPos = imgui.WindowPos()
	g.windowSize = imgui.WindowSize()
}

func (g windowGeom) pos() imgui.Vec2 {
	return g.windowPos
}

func (g windowGeom) size() imgui.Vec2 {
	return g.windowSize
}

// playmodeWin is a partial implementation of the playmodeWindow interface. it
// does not implement playmodeDraw or the any of the plain window interface.
type playmodeWin struct {
	playmodeWindow
	playmodeOpen bool
	playmodeGeom windowGeom
}

func (w playmodeWin) playmodeID(id string) string {
	return fmt.Sprintf("%s##playmode", id)
}

func (w playmodeWin) playmodeIsOpen() bool {
	return w.playmodeOpen
}

func (w *playmodeWin) playmodeSetOpen(open bool) {
	w.playmodeOpen = open
}

func (w *playmodeWin) playmodeGeometry() windowGeom {
	return w.playmodeGeom
}

// debuggerWin is a partial implementation of the debuggerWindow interface. it
// does not implement debuggerDraw or the any of the plain window interface.
type debuggerWin struct {
	debuggerWindow
	debuggerOpen bool
	debuggerGeom windowGeom
}

func (w debuggerWin) debuggerID(id string) string {
	return fmt.Sprintf("%s##debugger", id)
}

func (w *debuggerWin) debuggerIsOpen() bool {
	return w.debuggerOpen
}

func (w *debuggerWin) debuggerSetOpen(open bool) {
	w.debuggerOpen = open
}

func (w *debuggerWin) debuggerGeometry() windowGeom {
	return w.debuggerGeom
}

type playmodeWindow interface {
	window
	playmodeDraw()
	playmodeIsOpen() bool
	playmodeSetOpen(bool)
	playmodeGeometry() windowGeom
}

type debuggerWindow interface {
	window
	debuggerDraw()
	debuggerIsOpen() bool
	debuggerSetOpen(bool)
	debuggerGeometry() windowGeom
}

// toggles a window open according to emulation state
func (wm *manager) toggleOpen(winID string) bool {
	if wm.img.isPlaymode() {
		w, ok := wm.playmodeWindows[winID]
		if !ok {
			return false
		}
		w.playmodeSetOpen(!w.playmodeIsOpen())
		return w.playmodeIsOpen()
	} else {
		w, ok := wm.debuggerWindows[winID]
		if !ok {
			return false
		}
		w.debuggerSetOpen(!w.debuggerIsOpen())
		return w.debuggerIsOpen()
	}

	return false
}