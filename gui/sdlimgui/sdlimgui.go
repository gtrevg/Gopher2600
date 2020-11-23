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
	"io"
	"sync/atomic"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/debugger/terminal"
	"github.com/jetsetilly/gopher2600/gui"
	"github.com/jetsetilly/gopher2600/gui/crt"
	"github.com/jetsetilly/gopher2600/gui/sdlaudio"
	"github.com/jetsetilly/gopher2600/gui/sdlimgui/lazyvalues"
	"github.com/jetsetilly/gopher2600/hardware"
	"github.com/jetsetilly/gopher2600/hardware/television"
	"github.com/jetsetilly/gopher2600/logger"
	"github.com/jetsetilly/gopher2600/paths"
	"github.com/jetsetilly/gopher2600/reflection"
	"github.com/veandco/go-sdl2/sdl"

	"github.com/inkyblackness/imgui-go/v2"
)

// imguiIniFile is where imgui will store the coordinates of the imgui windows
// !!TODO: duplicate imgui.SetIniFilename so that is uses prefs package. we
// should be able to do this a smart implementation of io.Reader and io.Writer.
const imguiIniFile = "debugger_imgui.ini"

// SdlImgui is an sdl based visualiser using imgui.
type SdlImgui struct {
	// the mechanical requirements for the gui
	io      imgui.IO
	context *imgui.Context
	plt     *platform
	glsl    *glsl

	// references to the emulation
	lz  *lazyvalues.LazyValues
	tv  *television.Television
	vcs *hardware.VCS

	// is gui in playmode. use setPlaymode() and isPlaymode() to access this
	playmode atomic.Value

	// terminal interface to the debugger
	term *term

	// implementations of screen television protocols
	screen *screen
	audio  *sdlaudio.Audio

	// imgui window management
	wm *windowManager

	// the colors used by the imgui system. includes the TV colors converted to
	// a suitable format
	cols *imguiColors

	// functions that need to be performed in the main thread should be queued
	// for service
	service    chan func()
	serviceErr chan error

	// ReqFeature() and GetFeature() hands off requests to the featureReq
	// channel for servicing. think of this as a special instance of the
	// service chan
	featureSet     chan featureRequest
	featureSetErr  chan error
	featureGet     chan featureRequest
	featureGetData chan gui.FeatureReqData
	featureGetErr  chan error

	// events channel is not created but assigned with the feature request
	// gui.ReqSetEventChan
	events chan gui.Event

	// the gui renders differently depending on EmulationState. use setState()
	// to set the value
	state gui.EmulationState

	// mouse coords at last frame
	mx, my int32

	// the preferences we'll be saving to disk
	prefs    *Preferences
	crtPrefs *crt.Preferences

	// hasModal should be true for the duration of when a modal popup is on the screen
	hasModal bool

	// a request for the PlusROM first installation procedure has been received
	plusROMFirstInstallation *gui.PlusROMFirstInstallation
}

// NewSdlImgui is the preferred method of initialisation for type SdlImgui
//
// MUST ONLY be called from the gui thread.
func NewSdlImgui(tv *television.Television, playmode bool) (*SdlImgui, error) {
	img := &SdlImgui{
		context:        imgui.CreateContext(nil),
		io:             imgui.CurrentIO(),
		tv:             tv,
		service:        make(chan func(), 1),
		serviceErr:     make(chan error, 1),
		featureSet:     make(chan featureRequest, 1),
		featureSetErr:  make(chan error, 1),
		featureGet:     make(chan featureRequest, 1),
		featureGetData: make(chan gui.FeatureReqData, 1),
		featureGetErr:  make(chan error, 1),
	}

	// not in playmode by default
	img.playmode.Store(false)

	var err error

	// define colors
	img.cols = newColors()

	img.plt, err = newPlatform(img)
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}

	img.glsl, err = newGlsl(img.io, img)
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}

	iniPath, err := paths.ResourcePath("", imguiIniFile)
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}
	img.io.SetIniFilename(iniPath)

	img.lz = lazyvalues.NewLazyValues()
	img.screen = newScreen(img)
	img.term = newTerm()

	img.wm, err = newWindowManager(img)
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}

	// connect pixel renderer/referesher to television and texture renderer to
	// pixel renderer
	tv.AddPixelRenderer(img.screen)
	img.screen.addTextureRenderer(img.wm.dbgScr)
	img.screen.addTextureRenderer(img.wm.playScr)

	// this audio mixer produces the sound. there is another AudioMixer
	// implementation in winAudio which visualises the sound
	img.audio, err = sdlaudio.NewAudio()
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}
	tv.AddAudioMixer(img.audio)

	// initialise debugger preferences. in the event of playmode being set this
	// will immediately be replaced but frankly doing it this way is cleaner
	img.prefs, err = newPreferences(img, prefsGrpDebugger)
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}

	// initialise crt preferences
	img.crtPrefs, err = crt.NewPreferences()
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}

	// set playmode according to the playmode argument
	err = img.setPlaymode(playmode)
	if err != nil {
		return nil, curated.Errorf("sdlimgui: %v", err)
	}

	// open container window
	img.plt.window.Show()

	return img, nil
}

// Destroy implements GuiCreator interface
//
// MUST ONLY be called from the gui thread.
func (img *SdlImgui) Destroy(output io.Writer) {
	img.wm.destroy()
	err := img.audio.EndMixing()
	if err != nil {
		output.Write([]byte(err.Error()))
	}
	img.glsl.destroy()

	err = img.plt.destroy()
	if err != nil {
		output.Write([]byte(err.Error()))
	}

	img.context.Destroy()
}

func (img *SdlImgui) draw() {
	img.wm.draw()
	img.drawPlusROMFirstInstallation()
}

// GetTerminal implements terminal.Broker interface.
func (img *SdlImgui) GetTerminal() terminal.Terminal {
	return img.term
}

// GetReflectionRendere implements reflection.Broker interface.
func (img *SdlImgui) GetReflectionRenderer() reflection.Renderer {
	return img.screen
}

// set emulation state and handle any changes.
func (img *SdlImgui) setState(state gui.EmulationState) {
	img.state = state
	img.screen.render()
}

// is the gui in playmode or not.
func (img *SdlImgui) isPlaymode() bool {
	return img.playmode.Load().(bool)
}

// set playmode and handle the changeover gracefully. this includes the saving
// and loading of preference groups.
func (img *SdlImgui) setPlaymode(set bool) error {
	if set {
		if !img.playmode.Load().(bool) {
			if img.prefs != nil {
				err := img.prefs.save()
				if err != nil {
					return err
				}
			}

			var err error
			img.prefs, err = newPreferences(img, prefsGrpPlaymode)
			if err != nil {
				return curated.Errorf("sdlimgui: %v", err)
			}

			img.wm.playScr.setOpen(true)
		}
	} else {
		if img.playmode.Load().(bool) {
			if img.prefs != nil {
				if err := img.prefs.save(); err != nil {
					return err
				}
			}

			var err error
			img.prefs, err = newPreferences(img, prefsGrpDebugger)
			if err != nil {
				return curated.Errorf("sdlimgui: %v", err)
			}

			img.wm.playScr.setOpen(false)
		}
	}

	img.playmode.Store(set)

	return nil
}

func (img *SdlImgui) isCaptured() bool {
	if img.isPlaymode() {
		return img.wm.playScr.isCaptured
	}
	return img.wm.dbgScr.isCaptured
}

func (img *SdlImgui) setCapture(set bool) {
	if img.isPlaymode() {
		img.wm.playScr.isCaptured = set
	} else {
		img.wm.dbgScr.isCaptured = set
	}

	err := sdl.CaptureMouse(set)
	if err != nil {
		logger.Log("sdlimgui", err.Error())
	}

	img.plt.window.SetGrab(set)

	if set {
		_, err = sdl.ShowCursor(sdl.DISABLE)
		if err != nil {
			logger.Log("sdlimgui", err.Error())
		}
	} else {
		_, err = sdl.ShowCursor(sdl.ENABLE)
		if err != nil {
			logger.Log("sdlimgui", err.Error())
		}
	}
}

// scaling of the tv screen also depends on whether playmode is active

type scalingScreen interface {
	getScaling(horiz bool) float32
	setScaling(scaling float32)
}

func (img *SdlImgui) setScale(scaling float32, adjust bool) {
	var scr scalingScreen

	if img.isPlaymode() {
		scr = img.wm.playScr
	} else {
		scr = img.wm.dbgScr
	}

	if adjust {
		scale := scr.getScaling(false)
		if scale > 0.5 && scale < 4.0 {
			scr.setScaling(scale + scaling)
		}
	} else {
		scr.setScaling(scaling)
	}
}
