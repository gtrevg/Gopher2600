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

package sdlimgui

import (
	"fmt"
	"os"
	"runtime"

	"github.com/inkyblackness/imgui-go/v2"
	"github.com/jetsetilly/gopher2600/paths"
	"github.com/veandco/go-sdl2/sdl"
)

const windowTitle = "Gopher2600"
const winIniFile = "debugger_win.ini"

type platform struct {
	img *SdlImgui

	window     *sdl.Window
	shouldStop bool

	time uint64
}

// newPlatform is the preferred method of initialisation for the platform type
func newPlatform(img *SdlImgui) (*platform, error) {
	runtime.LockOSThread()

	err := sdl.Init(sdl.INIT_EVERYTHING)
	if err != nil {
		return nil, fmt.Errorf("SDL2: %v", err)
	}

	plt := &platform{
		img: img,
	}

	ini, err := plt.readIniFile()
	if err != nil {
		return nil, fmt.Errorf("SDL2: %v", err)
	}

	// map sdl key codes to imgui codes
	plt.setKeyMapping()

	plt.window, err = sdl.CreateWindow(windowTitle,
		sdl.WINDOWPOS_CENTERED, sdl.WINDOWPOS_CENTERED,
		ini.width, ini.height,
		sdl.WINDOW_OPENGL|sdl.WINDOW_ALLOW_HIGHDPI|sdl.WINDOW_RESIZABLE)

	if err != nil {
		sdl.Quit()
		return nil, fmt.Errorf("SDL: window creation: %v", err)
	}

	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MAJOR_VERSION, 2)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MINOR_VERSION, 1)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MAJOR_VERSION, 3)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_MINOR_VERSION, 2)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_FLAGS, sdl.GL_CONTEXT_FORWARD_COMPATIBLE_FLAG)
	_ = sdl.GLSetAttribute(sdl.GL_CONTEXT_PROFILE_MASK, sdl.GL_CONTEXT_PROFILE_CORE)
	_ = sdl.GLSetAttribute(sdl.GL_DOUBLEBUFFER, 1)
	_ = sdl.GLSetAttribute(sdl.GL_DEPTH_SIZE, 24)
	_ = sdl.GLSetAttribute(sdl.GL_STENCIL_SIZE, 8)

	glContext, err := plt.window.GLCreateContext()
	if err != nil {
		plt.destroy()
		return nil, fmt.Errorf("SDL: OpenGL: %v", err)
	}
	err = plt.window.GLMakeCurrent(glContext)
	if err != nil {
		plt.destroy()
		return nil, fmt.Errorf("SDL: OpenGL: %v", err)
	}

	_ = sdl.GLSetSwapInterval(1)

	return plt, nil
}

// destroy cleans up the resources.
func (plt *platform) destroy() error {
	err := plt.writeIniFile()
	if err != nil {
		return err
	}

	if plt.window != nil {
		_ = plt.window.Destroy()
		plt.window = nil
	}
	sdl.Quit()

	return nil
}

// setDisplaySize resizes the window
func (plt *platform) setDisplaySize(w, h int) {
	plt.window.SetSize(int32(w), int32(h))
}

// displaySize returns the dimension of the display.
func (plt *platform) displaySize() [2]float32 {
	w, h := plt.window.GetSize()
	return [2]float32{float32(w), float32(h)}
}

// framebufferSize returns the dimension of the framebuffer.
func (plt *platform) framebufferSize() [2]float32 {
	w, h := plt.window.GLGetDrawableSize()
	return [2]float32{float32(w), float32(h)}
}

// newFrame marks the begin of a render pass. It forwards all current state to imgui.CurrentIO().
func (plt *platform) newFrame() {
	// Setup display size (every frame to accommodate for window resizing)
	displaySize := plt.displaySize()
	plt.img.io.SetDisplaySize(imgui.Vec2{X: displaySize[0], Y: displaySize[1]})

	// Setup time step (we don't use SDL_GetTicks() because it is using millisecond resolution)
	frequency := sdl.GetPerformanceFrequency()
	currentTime := sdl.GetPerformanceCounter()
	if plt.time > 0 {
		plt.img.io.SetDeltaTime(float32(currentTime-plt.time) / float32(frequency))
	} else {
		plt.img.io.SetDeltaTime(1.0 / 60.0)
	}
	plt.time = currentTime

	// If a mouse press event came, always pass it as "mouse held this frame", so we don't miss click-release events that are shorter than 1 frame.
	x, y, state := sdl.GetMouseState()

	// if mouse is captured and the mouse is not over the tv screen then ignore
	// the mouse button state. the check against isHovered because we want
	// imgui to recognise the initial click to activate the window. the check
	// against isCaptured is because we don't want the tv screen to be
	// deactivated when the "invisible" mouse is outside the tv screen bounds.
	//
	// TODO: roll mouse updates into service loop
	if plt.img.wm.scr.isCaptured && !plt.img.wm.scr.isHovered {
		state = 0
	}

	plt.img.io.SetMousePosition(imgui.Vec2{X: float32(x), Y: float32(y)})
	for i, button := range []uint32{sdl.BUTTON_LEFT, sdl.BUTTON_RIGHT, sdl.BUTTON_MIDDLE} {
		plt.img.io.SetMouseButtonDown(i, (state&sdl.Button(button)) != 0)
	}
}

// PostRender performs a buffer swap.
func (plt *platform) postRender() {
	plt.window.GLSwap()
}

// show the main window (or not)
//
// MUST NOT be called from the #mainthread
func (plt *platform) showWindow(show bool) {
	plt.img.service <- func() {
		if show {
			plt.window.Show()
		} else {
			plt.window.Hide()
		}
	}
}

// in addition to the imgui ini file we need to keep another ini file just for
// the SDL window. we're calling it an ini file only because that is the system
// imgui uses.
//
// we'll try to use the same precise ini syntax as the imgui file but for now
// we're just storing window size so I'm not too worried about it; just using
// simple Fprintf() and Fscanf()

type iniFile struct {
	width  int32
	height int32
}

func (plt *platform) readIniFile() (*iniFile, error) {
	iniPath, err := paths.ResourcePath("", winIniFile)
	if err != nil {
		return nil, fmt.Errorf("debugger_win.ini file error: %v", err)
	}

	ini := &iniFile{
		width:  1280,
		height: 720,
	}

	f, err := os.Open(iniPath)
	if err != nil {
		switch err.(type) {
		case *os.PathError:
			// path errors are okay. we'll just use the defaults and a new ini
			// file will be created when the window is destroyed
			return ini, nil
		}
		return nil, fmt.Errorf("debugger_win.ini file error: %v", err)
	}
	defer f.Close()

	_, err = fmt.Fscanf(f, "%d, %d\n", &ini.width, &ini.height)
	if err != nil {
		return nil, fmt.Errorf("debugger_win.ini error: %v", err)
	}

	return ini, nil
}

func (plt *platform) writeIniFile() error {
	iniPath, err := paths.ResourcePath("", winIniFile)
	if err != nil {
		return fmt.Errorf("writing window ini file: %v", err)
	}

	f, err := os.Create(iniPath)
	if err != nil {
		return fmt.Errorf("writing window ini file: %v", err)
	}
	defer f.Close()

	w, h := plt.window.GetSize()
	fmt.Fprintf(f, "%d, %d\n", w, h)

	return nil
}
