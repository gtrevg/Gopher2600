// This file is part of Gopher2600
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

package sdldebug

import (
	"image/color"
	"io"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/gui"
	"github.com/jetsetilly/gopher2600/hardware/television"
	"github.com/jetsetilly/gopher2600/reflection"

	"github.com/veandco/go-sdl2/sdl"
)

// SdlDebug is a simple SDL implementation of the television.PixelRenderer interfac.
type SdlDebug struct {
	tv *television.Television

	// functions that need to be performed in the main thread should be queued
	// for service
	service    chan func()
	serviceErr chan error

	// ReqFeature() hands off requests to the featureReq channel for servicing
	featureReq chan featureRequest
	featureErr chan error

	// connects SDL guiLoop with the parent process
	events chan gui.Event

	// sdl stuff
	window   *sdl.Window
	renderer *sdl.Renderer
	textures *textures
	pixels   *pixels
	overlay  *overlay

	// current values for *playable* area of the screen. horizontal size never
	// changes
	//
	// these values are not the same as the window size. window size is scaled
	// appropriately
	scanlines   int32
	topScanline int

	// the rectangle used to limit which pixels are copied from the pixels
	// array to the rendering texture
	cpyRect *sdl.Rect

	// the number of pixels in the various pixel arrays. this includes the
	// pixel array in the overlay type
	pitch int

	// by how much each pixel should be scaled. note that this value needs to
	// be factored by both pixelWidth and GetSpec().AspectBias when applied to
	// the X axis
	pixelScale float32

	// if the emulation is paused then the image is output slightly differently
	paused bool

	// use debug colors to highlight video elements
	useDbgColors bool

	// use metapixel overlay
	useOverlay bool

	// show the overscan/hblank areas
	cropped bool

	// the position of the previous call to Pixel(). used for drawing cursor
	// and plotting meta-pixels
	lastX int
	lastY int

	// mouse coords at last frame
	mx, my int32

	// whether mouse is captured
	isCaptured bool
}

const windowTitle = "Gopher2600"
const windowTitleCaptured = "Gopher2600 [captured]"

// NewSdlDebug is the preferred method of initialisation for SdlDebug.
func NewSdlDebug(tv *television.Television, scale float32) (*SdlDebug, error) {
	scr := &SdlDebug{
		tv:         tv,
		service:    make(chan func(), 1),
		serviceErr: make(chan error, 1),
		featureReq: make(chan featureRequest, 1),
		featureErr: make(chan error, 1),
		pitch:      television.HorizClksScanline * pixelDepth,
		cropped:    true,
		paused:     true,
	}

	var err error

	// set up sdl
	err = sdl.Init(sdl.INIT_EVERYTHING)
	if err != nil {
		return nil, curated.Errorf("sdldebug: %v", err)
	}

	setupService()

	// SDL window - window size is set in Resize() function
	scr.window, err = sdl.CreateWindow(windowTitle,
		int32(sdl.WINDOWPOS_UNDEFINED), int32(sdl.WINDOWPOS_UNDEFINED),
		0, 0,
		uint32(sdl.WINDOW_HIDDEN))
	if err != nil {
		return nil, curated.Errorf("sdldebug: %v", err)
	}

	// sdl renderer. we set the scaling amount in the setWindow function later
	// once we know what the tv specification is
	scr.renderer, err = sdl.CreateRenderer(scr.window, -1, uint32(sdl.RENDERER_ACCELERATED))
	if err != nil {
		return nil, curated.Errorf("sdldebug: %v", err)
	}

	// register ourselves as a television.Renderer
	scr.tv.AddPixelRenderer(scr)

	// resize window
	spec := scr.tv.GetSpec()
	err = scr.resize(spec.ScanlineTop, spec.ScanlinesVisible)
	if err != nil {
		return nil, curated.Errorf("sdldebug: %v", err)
	}

	// set window scaling to default value
	err = scr.setWindow(scale)
	if err != nil {
		return nil, curated.Errorf("sdldebug: %v", err)
	}

	// note that we've elected not to show the window on startup
	// window is instead opened on a ReqSetVisibility request

	scr.renderer.Clear()
	scr.renderer.Present()

	return scr, nil
}

// Destroy implements GuiCreator interface.
func (scr *SdlDebug) Destroy(output io.Writer) {
	scr.overlay.destroy(output)
	scr.textures.destroy(output)

	err := scr.renderer.Destroy()
	if err != nil {
		output.Write([]byte(err.Error()))
	}

	err = scr.window.Destroy()
	if err != nil {
		output.Write([]byte(err.Error()))
	}
}

// show or hide window.
func (scr SdlDebug) showWindow(show bool) {
	if show {
		scr.window.Show()
	} else {
		scr.window.Hide()
	}
}

// the desired window width is different depending on whether the frame is
// cropped or uncropped.
func (scr SdlDebug) windowWidth() (int32, float32) {
	spec := scr.tv.GetSpec()
	scale := scr.pixelScale * pixelWidth * spec.AspectBias

	if scr.cropped {
		return int32(float32(television.HorizClksVisible) * scale), scale
	}

	return int32(float32(television.HorizClksScanline) * scale), scale
}

// the desired window height is different depending on whether the frame is
// cropped or uncropped.
func (scr SdlDebug) windowHeight() (int32, float32) {
	if scr.cropped {
		return int32(float32(scr.scanlines) * scr.pixelScale), scr.pixelScale
	}

	spec := scr.tv.GetSpec()
	return int32(float32(spec.ScanlinesTotal) * scr.pixelScale), scr.pixelScale
}

// use scale of -1 to reapply existing scale value.
func (scr *SdlDebug) setWindow(scale float32) error {
	if scale >= 0 {
		scr.pixelScale = scale
	}

	w, ws := scr.windowWidth()
	h, hs := scr.windowHeight()
	scr.window.SetSize(w, h)

	// make sure everything drawn through the renderer is correctly scaled
	err := scr.renderer.SetScale(ws, hs)
	if err != nil {
		return err
	}

	// make copy rectangled
	if scr.cropped {
		scr.cpyRect = &sdl.Rect{
			X: television.HorizClksHBlank, Y: int32(scr.topScanline),
			W: television.HorizClksVisible, H: scr.scanlines,
		}
	} else {
		spec := scr.tv.GetSpec()
		scr.cpyRect = &sdl.Rect{
			X: 0, Y: 0,
			W: television.HorizClksScanline, H: int32(spec.ScanlinesTotal),
		}
	}

	return nil
}

// resize is the non-service-wrapped resize function.
func (scr *SdlDebug) resize(topScanline, numScanlines int) error {
	// new screen limits
	scr.topScanline = topScanline
	scr.scanlines = int32(numScanlines)

	var err error

	// pixels arrays (including the overlay) and textures are always the
	// maximum size allowed by the specification. we need to remake them here
	// because the specification may have changed as part of the resize() event

	spec := scr.tv.GetSpec()
	scr.pixels = newPixels(television.HorizClksScanline, spec.ScanlinesTotal)

	scr.textures, err = newTextures(scr.renderer, television.HorizClksScanline, spec.ScanlinesTotal)
	if err != nil {
		return curated.Errorf("sdldebug: %v", err)
	}

	scr.overlay, err = newOverlay(scr.renderer, television.HorizClksScanline, spec.ScanlinesTotal)
	if err != nil {
		return curated.Errorf("sdldebug: %v", err)
	}

	// setWindow dimensions. see commentary for Resize() function in
	// PixelRenderer interface definition
	if !scr.tv.IsStable() {
		scr.setWindow(-1)
	}

	return nil
}

// Resize implements television.PixelRenderer interface
//
// MUST NOT be called from #mainthread.
func (scr *SdlDebug) Resize(_ television.Spec, topScanline, numScanlines int) error {
	scr.service <- func() {
		scr.serviceErr <- scr.resize(topScanline, numScanlines)
	}
	return <-scr.serviceErr
}

// update is called automatically on every call to NewFrame() and whenever a
// state change in ReqFeature() requires it.
func (scr *SdlDebug) update() error {
	scr.renderer.SetDrawColor(0, 0, 0, 255)
	err := scr.renderer.Clear()
	if err != nil {
		return err
	}

	// decide whether to use regular or dbg pixels
	pixels := scr.pixels.regular
	if scr.useDbgColors {
		pixels = scr.pixels.dbg
	}

	// render main textures
	err = scr.textures.render(scr.cpyRect, pixels, scr.pitch)
	if err != nil {
		return err
	}

	spec := scr.tv.GetSpec()

	// render screen guides
	if !scr.cropped {
		scr.renderer.SetDrawBlendMode(sdl.BLENDMODE_BLEND)
		scr.renderer.SetDrawColor(100, 100, 100, 50)
		r := &sdl.Rect{X: 0, Y: 0,
			W: int32(television.HorizClksHBlank), H: int32(spec.ScanlinesTotal)}
		err = scr.renderer.FillRect(r)
		if err != nil {
			return err
		}

		r = &sdl.Rect{X: 0, Y: 0,
			W: int32(television.HorizClksScanline), H: int32(spec.ScanlineTop)}
		err = scr.renderer.FillRect(r)
		if err != nil {
			return err
		}

		r = &sdl.Rect{X: 0, Y: int32(spec.ScanlineBottom),
			W: int32(television.HorizClksScanline), H: int32(spec.ScanlinesOverscan)}
		err = scr.renderer.FillRect(r)
		if err != nil {
			return err
		}
	}

	// render overlay
	if scr.useOverlay {
		err = scr.overlay.render(scr.cpyRect, scr.pitch)
		if err != nil {
			return err
		}
	}

	if scr.paused {
		// adjust cursor coordinates
		x := scr.lastX
		y := scr.lastY

		if scr.cropped {
			y -= scr.topScanline
			x -= television.HorizClksHBlank - 1
		}

		// cursor is one step ahead of pixel -- move to new scanline if
		// necessary
		if x >= television.HorizClksScanline {
			x = 0
			y++
		}

		// set cursor color
		scr.renderer.SetDrawBlendMode(sdl.BLENDMODE_NONE)
		scr.renderer.SetDrawColor(100, 100, 255, 255)

		// check to see if cursor is "off-screen". if so, draw at the zero
		// line and use a different cursor color
		if x < 0 {
			scr.renderer.SetDrawColor(255, 100, 100, 255)
			x = 0
		}
		if y < 0 {
			scr.renderer.SetDrawColor(255, 100, 100, 255)
			y = 0
		}

		// leave the current pixel visible at the top-left corner of the cursor
		scr.renderer.DrawRect(&sdl.Rect{X: int32(x + 1), Y: int32(y), W: 1, H: 1})
		scr.renderer.DrawRect(&sdl.Rect{X: int32(x + 1), Y: int32(y + 1), W: 1, H: 1})
		scr.renderer.DrawRect(&sdl.Rect{X: int32(x), Y: int32(y + 1), W: 1, H: 1})
	}

	scr.renderer.Present()

	return nil
}

// NewFrame implements television.PixelRenderer interface.
func (scr *SdlDebug) NewFrame(frameNum int, _ bool) error {
	// the sdlplay version of this function does not wait for the error signal
	// before continuing. we do so here (in the update() function) because if
	// we don't the screen image will tear badly. the difference is because in
	// sdldebug we clear pixels between frames.

	scr.service <- func() {
		scr.serviceErr <- scr.update()
	}
	if err := <-scr.serviceErr; err != nil {
		return err
	}

	scr.pixels.clear()
	scr.overlay.clear()
	return scr.textures.flip()
}

// UpdatingPixels implements television.PixelRenderer interface.
func (scr *SdlDebug) UpdatingPixels(_ bool) {
}

// SetPixel implements television.PixelRenderer interface.
func (scr *SdlDebug) SetPixel(sig television.SignalAttributes, current bool) error {
	var col color.RGBA

	// handle VBLANK by setting pixels to black
	if !sig.VBlank {
		spec := scr.tv.GetSpec()
		col = spec.GetColor(sig.Pixel)
	}

	i := (sig.Scanline*int(television.HorizClksScanline) + sig.HorizPos) * pixelDepth
	if i <= scr.pixels.length()-pixelDepth {
		scr.pixels.regular[i] = col.R
		scr.pixels.regular[i+1] = col.G
		scr.pixels.regular[i+2] = col.B
		scr.pixels.regular[i+3] = 255
	}

	// update cursor position
	scr.lastX = sig.HorizPos
	scr.lastY = sig.Scanline

	return nil
}

// GetReflectionRenderer implements the relfection.Broker interface.
func (scr *SdlDebug) GetReflectionRenderer() reflection.Renderer {
	return scr
}

// Reflect implements the relfection.Renderer interface.
func (scr *SdlDebug) Reflect(result reflection.LastResult) error {
	col := reflection.PaletteElements[result.VideoElement]

	i := (scr.lastY*int(television.HorizClksScanline) + scr.lastX) * pixelDepth
	if i <= scr.pixels.length()-pixelDepth {
		scr.pixels.dbg[i] = col.R
		scr.pixels.dbg[i+1] = col.G
		scr.pixels.dbg[i+2] = col.B
		scr.pixels.dbg[i+3] = col.A
	}

	if result.WSYNC {
		i := (scr.lastY*int(television.HorizClksScanline) + scr.lastX) * pixelDepth
		if i <= scr.overlay.length()-pixelDepth {
			rgb := reflection.PaletteEvents["WSYNC"]
			scr.overlay.pixels[i] = rgb.R
			scr.overlay.pixels[i+1] = rgb.G
			scr.overlay.pixels[i+2] = rgb.B
			scr.overlay.pixels[i+3] = rgb.A
		}
	}

	return nil
}

// NewScanline implements television.PixelRenderer interface.
func (scr *SdlDebug) NewScanline(scanline int) error {
	return nil
}

// PauseRendering implements television.PixelRenderer interface.
func (scr *SdlDebug) PauseRendering(paused bool) {
	scr.paused = paused
	scr.update()
}

// EndRendering implements television.PixelRenderer interface.
func (scr *SdlDebug) EndRendering() error {
	return nil
}

// Refresh implements television.PixelRenderer interface.
func (scr *SdlDebug) Refresh(_ bool) {
}
