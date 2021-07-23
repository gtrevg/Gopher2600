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
	"image"
	"image/color"
	"sync"

	"github.com/jetsetilly/gopher2600/gui"
	"github.com/jetsetilly/gopher2600/hardware/television/signal"
	"github.com/jetsetilly/gopher2600/hardware/television/specification"
	"github.com/jetsetilly/gopher2600/reflection"
)

// textureRenderers should consider that the timing of the VCS produces
// "pixels" of two pixels across.
const pixelWidth = 2

// textureRenderers can share the underlying pixels of the screen type instance. both of these functions
// should be called inside the screen critical section.
type textureRenderer interface {
	render()
	resize()
}

// screen implements television.PixelRenderer.
type screen struct {
	img *SdlImgui

	crit screenCrit

	// list of renderers to call from render. renderers are added with
	// addTextureRenderer()
	renderers  []textureRenderer
	emuWait    chan bool
	emuWaitAck chan bool

	// the mouse coords used in the most recent call to PushGotoCoords(). only
	// read/write by the GUI thread so doesn't need to be in critical section.
	gotoCoordsX int
	gotoCoordsY int
}

// for clarity, variables accessed in the critical section are encapsulated in
// their own subtype.
type screenCrit struct {
	// critical sectioning
	section sync.Mutex

	// whether or not to sync with monitor refresh rate
	monitorSync bool

	// copy of the spec being used by the TV. the TV notifies us through the
	// Resize() function
	spec specification.Spec

	// whether the current frame was generated from a stable television state
	stable bool

	// whether the current frame is vsynced
	vsynced bool

	// the number of consecutive unsynced frames received by NewFrame().
	// * playmode only
	unsyncedCt int

	// when an unsynced frame is encountered the screen will roll. we only
	// allow this in playmode. there's no value in seeing the screen-roll in
	// debug mode.
	//
	// unsyncedScanline keeps track of the accumulated scanline position
	//
	// * playmode only
	unsyncedScanline int

	// unsyncedRecoveryCt is used to help the screen regain the correct
	// position once a vsynced frame is received.
	//
	// * playmode only
	unsyncedRecoveryCt int

	// current values for *playable* area of the screen
	topScanline    int
	bottomScanline int

	// the pixels array is used in the presentation texture of the play and debug screen.
	pixels *image.RGBA

	// bufferPixels are what we plot pixels to while we wait for a frame to complete.
	// - the larger the buffer the greater the input lag
	// - the smaller the buffer the more the emulation will have to wait the
	//		screen to catch up (see emuWait and emuWaitAck channels)
	// - a five frame buffer seems good. ten frames can feel laggy
	bufferPixels [5]*image.RGBA

	// which buffer we'll be plotting to and which bufffer we'll be rendering
	// from. in playmode we make sure these two indexes never meet. in
	// debugmode we plot and render from the same index, it doesn't matter.
	plotIdx       int
	renderIdx     int
	prevRenderIdx int

	// element colors and overlay colors are only used in the debugger so we
	// don't need to replicate the "backing pixels" idea.
	elementPixels *image.RGBA
	overlayPixels *image.RGBA

	// 2d array of disasm entries. resized at the same time as overlayPixels resize
	reflection [][]reflection.VideoStep

	// the cropped view of the screen pixels. note that these instances are
	// created through the SubImage() command and should not be written to
	// directly
	cropPixels        *image.RGBA
	cropElementPixels *image.RGBA
	cropOverlayPixels *image.RGBA

	// the coordinates of the last SetPixel(). used to help set the alpha
	// channel when emulation is paused
	lastX int
	lastY int

	// the selected overlay
	overlay string

	// when paused we show two adjacent frames over-and-over. this flag tracks
	// which of those frames to show
	pauseFrame bool
}

func newScreen(img *SdlImgui) *screen {
	scr := &screen{
		img:        img,
		emuWait:    make(chan bool),
		emuWaitAck: make(chan bool),
	}

	scr.crit.overlay = overlayLabels[overlayNone]
	scr.crit.monitorSync = true

	// default to NTSC. this will change on the first instance of
	scr.resize(specification.SpecNTSC, specification.SpecNTSC.AtariSafeTop, specification.SpecNTSC.AtariSafeBottom)
	scr.Reset()

	return scr
}

// Reset implements the television.PixelRenderer interface. Note that Reset
// *does not* imply a Resize().
//
// called on startup and also whenever the VCS is reset, including when a new
// cartridge is inserted.
func (scr *screen) Reset() {
	// we don't callresize on screen Reset

	scr.crit.section.Lock()
	defer scr.crit.section.Unlock()

	scr.clearPixels()
	scr.crit.plotIdx = 0
	scr.crit.renderIdx = 0

	scr.crit.lastX = 0
	scr.crit.lastY = 0

	// screen is vsynced by definition on reset
	scr.crit.vsynced = true
}

// clear all pixel information including reflection data.
//
// called when screen is reset and also when it is resize.
//
// must be called from inside a critical section.
func (scr *screen) clearPixels() {
	// clear pixels
	for y := 0; y < scr.crit.pixels.Bounds().Size().Y; y++ {
		for x := 0; x < scr.crit.pixels.Bounds().Size().X; x++ {
			scr.crit.pixels.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			scr.crit.elementPixels.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			scr.crit.overlayPixels.SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
		}
	}
	for i := range scr.crit.bufferPixels {
		for y := 0; y < scr.crit.pixels.Bounds().Size().Y; y++ {
			for x := 0; x < scr.crit.pixels.Bounds().Size().X; x++ {
				scr.crit.bufferPixels[i].SetRGBA(x, y, color.RGBA{0, 0, 0, 255})
			}
		}
	}

	// reset reflection information
	for i := range scr.crit.reflection {
		for j := range scr.crit.reflection[i] {
			scr.crit.reflection[i][j] = reflection.VideoStep{}
		}
	}
	scr.replotOverlay()
}

// resize() is called by Resize() and by NewScreen().
func (scr *screen) resize(spec specification.Spec, topScanline int, bottomScanline int) {
	scr.crit.section.Lock()
	defer scr.crit.section.Unlock()

	// do nothing if resize values are the same as previously
	if scr.crit.spec.ID == spec.ID && scr.crit.topScanline == topScanline && scr.crit.bottomScanline == bottomScanline {
		return
	}

	scr.crit.spec = spec
	scr.crit.topScanline = topScanline
	scr.crit.bottomScanline = bottomScanline

	// the total number scanlines we going to have is the number total number
	// of scanlines in the screen spec +1. the additional one is so that
	// scalines with the VSYNC on show up on the uncropped screen. this is
	// particularly important if we want the cursor to be visible.
	totalScanlines := spec.ScanlinesTotal + 1

	scr.crit.pixels = image.NewRGBA(image.Rect(0, 0, specification.ClksScanline, totalScanlines))
	scr.crit.elementPixels = image.NewRGBA(image.Rect(0, 0, specification.ClksScanline, totalScanlines))
	scr.crit.overlayPixels = image.NewRGBA(image.Rect(0, 0, specification.ClksScanline, totalScanlines))

	for i := range scr.crit.bufferPixels {
		scr.crit.bufferPixels[i] = image.NewRGBA(image.Rect(0, 0, specification.ClksScanline, totalScanlines))
	}

	// allocate reflection info
	scr.crit.reflection = make([][]reflection.VideoStep, specification.ClksScanline)
	for x := 0; x < specification.ClksScanline; x++ {
		scr.crit.reflection[x] = make([]reflection.VideoStep, totalScanlines)
	}

	// create a cropped image from the main
	crop := image.Rect(
		specification.ClksHBlank, scr.crit.topScanline,
		specification.ClksHBlank+specification.ClksVisible, scr.crit.bottomScanline,
	)
	scr.crit.cropPixels = scr.crit.pixels.SubImage(crop).(*image.RGBA)
	scr.crit.cropElementPixels = scr.crit.elementPixels.SubImage(crop).(*image.RGBA)
	scr.crit.cropOverlayPixels = scr.crit.overlayPixels.SubImage(crop).(*image.RGBA)

	// make sure all pixels are clear
	scr.clearPixels()

	// resize texture renderers
	for _, r := range scr.renderers {
		r.resize()
	}
}

// Resize implements the television.PixelRenderer interface
//
// called when the television detects a new TV specification.
//
// it is also called by the television when the rewind system is used, in order
// to make sure that the screen specification is accurate.
//
// MUST NOT be called from the gui thread.
func (scr *screen) Resize(spec specification.Spec, topScanline int, bottomScanline int) error {
	scr.img.polling.service <- func() {
		scr.resize(spec, topScanline, bottomScanline)
		scr.img.polling.serviceErr <- nil
	}
	return <-scr.img.polling.serviceErr
}

// NewFrame implements the television.PixelRenderer interface
//
// MUST NOT be called from the gui thread.
func (scr *screen) NewFrame(vsynced bool, stable bool) error {
	// unlocking must be done carefully
	scr.crit.section.Lock()

	scr.crit.stable = stable
	scr.crit.vsynced = vsynced

	if scr.img.isPlaymode() {
		// check screen rolling if crtprefs are enabled
		if scr.img.crtPrefs.Enabled.Get().(bool) {
			if vsynced {
				scr.crit.unsyncedCt = 0

				// recovery required
				if scr.crit.unsyncedScanline > 0 {
					scr.crit.unsyncedRecoveryCt++
					scr.crit.unsyncedScanline *= 8
					scr.crit.unsyncedScanline /= 10

					if scr.crit.unsyncedScanline == 0 {
						scr.crit.unsyncedRecoveryCt = 0
					}
				}
			} else {
				// without the stable check, the screen can desync and recover
				// from a roll on startup on most ROMs, which looks quite cool but
				// we'll leave it disabled for now.
				if stable {
					scr.crit.unsyncedCt++
					if scr.crit.unsyncedCt > scr.img.crtPrefs.UnsyncTolerance.Get().(int) {
						scr.crit.unsyncedScanline = (scr.crit.unsyncedScanline + scr.crit.lastY) % specification.AbsoluteMaxScanlines
						scr.crit.unsyncedRecoveryCt = 0
					}
				}
			}
		} else {
			scr.crit.unsyncedScanline = 0
		}

		scr.crit.plotIdx++
		if scr.crit.plotIdx >= len(scr.crit.bufferPixels) {
			scr.crit.plotIdx = 0
		}

		// if plot index has crashed into the render index then
		if scr.crit.plotIdx == scr.crit.renderIdx && scr.crit.monitorSync {
			// ** screen update not keeping up with emulation **

			// we must unlock the critical section or the gui thread will not
			// be able to process the channel
			scr.crit.section.Unlock()

			// pause emulation until screen has caught up
			scr.emuWait <- true
			<-scr.emuWaitAck

			return nil
		}
	} else if scr.img.state != gui.StateRewinding {
		// clear reflection pixels beyond the last X/Y plot
		//
		// this is hardly ever required but it is important for consistent
		// reflection feedback if a frame is smaller than any previous frame.
		//
		// for example, during ROM initialisation, the limits of a frame might
		// be far beyond normal, meaning reflection data from that phase will
		// remain for the duration of the execution.
		//
		// note that we don't do this if the current gui state is
		// StateRewinding. this is because in the case of GotoCoords() the
		// lastX and lastY values are probably misleading for this purpose (the
		// emulation being paused before the end of the screen)
		if scr.crit.lastY < len(scr.crit.reflection[0]) {
			for x := scr.crit.lastX; x < len(scr.crit.reflection); x++ {
				scr.crit.reflection[x][scr.crit.lastY] = reflection.VideoStep{}
			}
			for x := 0; x < len(scr.crit.reflection); x++ {
				for y := scr.crit.lastY + 1; y < len(scr.crit.reflection[x]); y++ {
					scr.crit.reflection[x][y] = reflection.VideoStep{}
				}
			}
		}
	}

	scr.crit.section.Unlock()

	return nil
}

// NewScanline implements the television.PixelRenderer interface.
func (scr *screen) NewScanline(scanline int) error {
	return nil
}

// UpdatingPixels implements the television PixelRenderer and PixelRefresh interfaces.
func (scr *screen) UpdatingPixels(updating bool) {
	if updating {
		scr.crit.section.Lock()
		return
	}

	scr.crit.section.Unlock()
}

// SetPixel implements the television.PixelRenderer interface.
//
// Must only be called between calls to UpdatingPixels(true) and UpdatingPixels(false).
func (scr *screen) SetPixel(sig signal.SignalAttributes, current bool) error {
	col := color.RGBA{R: 0, G: 0, B: 0, A: 255}

	// handle VBLANK by setting pixels to black
	if !sig.VBlank {
		col = scr.crit.spec.GetColor(sig.Pixel)
	}

	if current {
		scr.crit.lastX = sig.Clock
		scr.crit.lastY = sig.Scanline
	}

	// if sig is outside the bounds of the image then the SetRGBA() will silently fail

	adjustedScanline := (sig.Scanline + scr.crit.unsyncedScanline) % scr.crit.spec.ScanlinesTotal
	scr.crit.bufferPixels[scr.crit.plotIdx].SetRGBA(sig.Clock, adjustedScanline, col)

	return nil
}

// SetPixels implements the television.PixelRenderer interface.
//
// Must only be called between calls to UpdatingPixels(true) and UpdatingPixels(false).
func (scr *screen) SetPixels(sig []signal.SignalAttributes, current bool) error {
	if len(sig) == 0 {
		return nil
	}

	var col color.RGBA

	offset := sig[0].Clock * 4
	adjustedScanline := (sig[0].Scanline + scr.crit.unsyncedScanline) % scr.crit.spec.ScanlinesTotal
	offset += adjustedScanline * scr.crit.bufferPixels[scr.crit.plotIdx].Rect.Size().X * 4

	for i := range sig[0:] {
		if offset >= len(scr.crit.bufferPixels[scr.crit.plotIdx].Pix) {
			return nil
		}

		// handle VBLANK by setting pixels to black
		if sig[i].VBlank {
			col = color.RGBA{R: 0, G: 0, B: 0}
		} else {
			col = scr.crit.spec.GetColor(sig[i].Pixel)
		}

		scr.crit.bufferPixels[scr.crit.plotIdx].Pix[offset] = col.R
		scr.crit.bufferPixels[scr.crit.plotIdx].Pix[offset+1] = col.G
		scr.crit.bufferPixels[scr.crit.plotIdx].Pix[offset+2] = col.B

		// alpha channel never changes

		offset += 4
		offset %= len(scr.crit.bufferPixels[scr.crit.plotIdx].Pix)
	}

	if current {
		scr.crit.lastX = sig[len(sig)-1].Clock
		scr.crit.lastY = sig[len(sig)-1].Scanline
	}

	return nil
}

// EndRendering implements the television.PixelRenderer interface.
func (scr *screen) EndRendering() error {
	return nil
}

// Reflect implements reflection.Renderer interface.
//
// Must only be called between calls to UpdatingPixels(true) and UpdatingPixels(false).
func (scr *screen) Reflect(v reflection.VideoStep) error {
	// array indexes into the reflection 2d array are taken from the reflected
	// TV signal.
	x := v.TV.Clock
	y := v.TV.Scanline

	// store Reflection instance
	if x < len(scr.crit.reflection) && y < len(scr.crit.reflection[x]) {
		scr.crit.reflection[x][y] = v

		// set element pixel according to the video element that ulimately informed
		// the color of the pixel generated by the videocycle
		rgb := altColors[v.VideoElement]
		scr.crit.elementPixels.SetRGBA(x, y, rgb)

		// write to overlay. this uses the current overlay settings but it can be
		// changed later with a call to replotOverlay()
		scr.plotOverlay(x, y, v)
	}

	return nil
}

// replotOverlay should be called from within a scr.crit.section Lock().
func (scr *screen) replotOverlay() {
	for y := 0; y < scr.crit.overlayPixels.Bounds().Size().Y; y++ {
		for x := 0; x < scr.crit.overlayPixels.Bounds().Size().X; x++ {
			scr.plotOverlay(x, y, scr.crit.reflection[x][y])
		}
	}
}

// plotOverlay should be called from within a scr.crit.section Lock().
func (scr *screen) plotOverlay(x, y int, ref reflection.VideoStep) {
	scr.crit.overlayPixels.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
	switch scr.crit.overlay {
	case overlayLabels[overlayWSYNC]:
		if ref.WSYNC {
			scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.WSYNC])
		}
	case overlayLabels[overlayCollisions]:
		if ref.Collision.LastVideoCycle.IsCXCLR() {
			scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.CXCLR])
		} else if !ref.Collision.LastVideoCycle.IsNothing() {
			scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.Collision])
		}
	case overlayLabels[overlayHMOVE]:
		// HmoveCt counts to -1 (or 255 for a uint8)
		if ref.Hmove.Delay {
			scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.HMOVEdelay])
		} else if ref.Hmove.Latch {
			if ref.Hmove.RippleCt != 255 {
				scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.HMOVEripple])
			} else {
				scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.HMOVElatched])
			}
		}
	case overlayLabels[overlayRSYNC]:
		if ref.RSYNCalign {
			scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.RSYNCalign])
		} else if ref.RSYNCreset {
			scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.RSYNCreset])
		}
	case overlayLabels[overlayCoprocessor]:
		if ref.CoprocessorActive {
			scr.crit.overlayPixels.SetRGBA(x, y, reflectionColors[reflection.CoprocessorActive])
		}
	}
}

// texture renderers can share the underlying pixels in the screen instance.
func (scr *screen) addTextureRenderer(r textureRenderer) {
	scr.renderers = append(scr.renderers, r)
	r.resize()
}

// unset all attached texture renderers.
func (scr *screen) clearTextureRenderers() {
	scr.renderers = make([]textureRenderer, 0)
}

// called by service loop.
func (scr *screen) render() {
	if scr.img.isPlaymode() {
		scr.copyPixelsPlaymode()
	} else {
		scr.copyPixelsDebugmode()
	}

	for _, r := range scr.renderers {
		r.render()
	}
}

// copy pixels from buffer to the pixels.
func (scr *screen) copyPixelsDebugmode() {
	scr.crit.section.Lock()
	defer scr.crit.section.Unlock()

	// copy pixels from render buffer to the live copy.
	copy(scr.crit.pixels.Pix, scr.crit.bufferPixels[scr.crit.renderIdx].Pix)
}

// copy pixels from buffer to the pixels.
func (scr *screen) copyPixelsPlaymode() {
	scr.crit.section.Lock()
	defer scr.crit.section.Unlock()

	if scr.img.state == gui.StatePaused {
		// when emulation is paused we alternate which frame to show. this
		// simple technique means that two-frame flicker kernels will show a
		// still image that looks natural.
		//
		// it does mean that for three-frame flicker kernels and single frame
		// kernels are sub-optimal but I think this is better than allowing a
		// flicker kernel to show only half the pixels necessary to show a
		// natural image.
		if scr.crit.pauseFrame {
			copy(scr.crit.pixels.Pix, scr.crit.bufferPixels[scr.crit.renderIdx].Pix)
		} else {
			copy(scr.crit.pixels.Pix, scr.crit.bufferPixels[scr.crit.prevRenderIdx].Pix)
		}
		scr.crit.pauseFrame = !scr.crit.pauseFrame
		return
	} else {
		// attempt to sync frame generation with monitor refresh rate
		if scr.crit.monitorSync {
			// advance render index
			scr.crit.prevRenderIdx = scr.crit.renderIdx
			scr.crit.renderIdx++
			if scr.crit.renderIdx >= len(scr.crit.bufferPixels) {
				scr.crit.renderIdx = 0
			}

			// render index has bumped into the plotting index. revert render index
			if scr.crit.renderIdx == scr.crit.plotIdx {
				// ** emulation not keeping up with screen update **

				// undo frame advancement. in earlier versions of the code we
				// reduced the renderIdx by two, having the effect of reusing not
				// the previous frame, but the frame before that.
				//
				// it was thought that this would help out the displaying of
				// two-frame flicker kernels. which it did, but in some cases that
				// could result in stuttering of moving sprites. it was
				// particularly bad if the FPS of the ROM was below the refresh
				// rate of the monitor.
				//
				// a good example of this is the introductory scroller in the demo
				// Ataventure (by KK of DMA). a scroller that updates every frame
				// at 50fps and causes very noticeable side-effects on a 60Hz
				// monitor.

				// by undoing the frame advancement however, we will cause the
				// prevRenderIdx to be the same as the renderIdx, which will cause
				// an ineffective pause screen for flicker kernels. remedy this by
				// simply swapping the current and previous index values
				t := scr.crit.prevRenderIdx
				scr.crit.prevRenderIdx = scr.crit.renderIdx
				scr.crit.renderIdx = t
			}
		}

		// copy pixels from render buffer to the live copy.
		copy(scr.crit.pixels.Pix, scr.crit.bufferPixels[scr.crit.renderIdx].Pix)
	}

	// let the emulator thread know it's okay to continue as soon as possible
	//
	// this is only ever the case if monitorSync is true but there's no
	// performance harm in allowing the select block to run in all instances
	select {
	case <-scr.emuWait:
		scr.emuWaitAck <- true
	default:
	}
}
