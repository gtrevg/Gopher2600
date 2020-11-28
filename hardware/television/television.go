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

package television

import (
	"fmt"
	"strings"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/hardware/television/signal"
	"github.com/jetsetilly/gopher2600/hardware/television/specification"
)

// the number of additional lines over the NTSC spec that is allowed before the
// TV flips to the PAL specification.
const excessScanlinesNTSC = 40

// the number of synced frames where we can expect things to be in flux.
const leadingFrames = 5

// the number of synced frames required before the tv frame is considered to "stable".
const stabilityThreshold = 20

type State struct {
	// television specification (NTSC or PAL)
	spec specification.Spec

	// auto flag indicates that the tv type/specification should switch if it
	// appears to be outside of the current spec.
	//
	// in practice this means that if auto is true then we start with the NTSC
	// spec and move to PAL if the number of scanlines exceeds the NTSC maximum
	auto bool

	// state of the television
	//	- the current horizontal position. the position where the next pixel will be
	//  drawn. also used to check we're receiving the correct signals at the
	//  correct time.
	horizPos int
	//	- the current frame
	frameNum int
	//	- the current scanline number
	scanline int
	//  - the current synced frame number. a synced frame is one which was
	//  generated from a valid VSYNC/VBLANK sequence. we use this to detect:
	//   * whether the image is "stable"
	//   * whether specification changes should still occur
	syncedFrameNum int

	// is current frame as a result of a VSYNC flyback or not (a "natural"
	// flyback). we use this in the context of newFrame() so we should probably
	// think of this as the previous frame.
	syncedFrame bool

	// record of signal attributes from the last call to Signal()
	lastSignal signal.SignalAttributes

	// vsyncCount records the number of consecutive colorClocks the vsync signal
	// has been sustained. we use this to help correctly implement vsync.
	vsyncCount int

	// top and bottom of screen as detected by vblank/color signal
	top    int
	bottom int

	// frame resizer
	resizer resizer
}

// Snapshot makes a copy of the television state.
func (s *State) Snapshot() *State {
	n := *s
	return &n
}

// Returns state information. Not that ReqHorizPos counts from
// "-specifcation.HorizClksHblank" and not zero as you might expect.
func (s *State) GetState(request signal.StateReq) int {
	switch request {
	case signal.ReqFramenum:
		return s.frameNum
	case signal.ReqScanline:
		return s.scanline
	case signal.ReqHorizPos:
		return s.horizPos - specification.HorizClksHBlank
	}
	panic(fmt.Sprintf("television: unhandled tv state request (%v)", request))
}

// Television is a Television implementation of the Television interface. In all
// honesty, it's most likely the only implementation required.
type Television struct {
	// spec on creation ID is the string that was to ID the television
	// type/spec on creation. because the actual spec can change, the ID field
	// of the Spec type can not be used for things like regression
	// test recreation etc.
	reqSpecID string

	// framerate limiter
	lmtr limiter

	// list of renderer implementations to consult
	renderers []PixelRenderer

	// list of frametrigger implementations to consult
	frameTriggers []FrameTrigger

	// list of audio mixers to consult
	mixers []AudioMixer

	// a single registered reflector
	reflector ReflectionSynchronising

	state *State

	// list of signals sent to pixel renderers since the beginning of the
	// current frame
	signals []signal.SignalAttributes
	// the index to write the next signal
	signalIdx int
}

// NewReference creates a new instance of the reference television type,
// satisfying the Television interface.
func NewTelevision(spec string) (*Television, error) {
	tv := &Television{
		reqSpecID: strings.ToUpper(spec),
		state:     &State{},
		signals:   make([]signal.SignalAttributes, MaxSignalHistory),
	}

	// initialise frame rate limiter
	tv.lmtr.init(tv)
	tv.SetFPS(-1)

	// set specification
	err := tv.SetSpec(spec)
	if err != nil {
		return nil, err
	}

	// empty list of renderers
	tv.renderers = make([]PixelRenderer, 0)

	return tv, nil
}

func (tv *Television) String() string {
	return fmt.Sprintf("FR=%04d SL=%03d HP=%03d", tv.state.frameNum, tv.state.scanline, tv.state.horizPos-specification.HorizClksHBlank)
}

// Snapshot makes a copy of the television state.
func (tv *Television) Snapshot() *State {
	return tv.state.Snapshot()
}

// Plumb in an existing television state.
func (tv *Television) Plumb(s *State) {
	if s == nil {
		return
	}
	tv.state = s

	// resize renderers to match current state
	for _, r := range tv.renderers {
		_ = r.Resize(tv.state.spec, tv.state.top, tv.state.bottom-tv.state.top)
	}
}

// AddPixelRenderer registers an implementation of PixelRenderer. Multiple
// implemntations can be added.
func (tv *Television) AddPixelRenderer(r PixelRenderer) {
	tv.renderers = append(tv.renderers, r)
	tv.frameTriggers = append(tv.frameTriggers, r)
}

// AddFrameTrigger registers an implementation of FrameTrigger. Multiple
// implemntations can be added.
func (tv *Television) AddFrameTrigger(f FrameTrigger) {
	tv.frameTriggers = append(tv.frameTriggers, f)
}

// AddAudioMixer registers an implementation of AudioMixer. Multiple
// implemntations can be added.
func (tv *Television) AddAudioMixer(m AudioMixer) {
	tv.mixers = append(tv.mixers, m)
}

// AddReflector registers an implementation of ReflectionSynchronising. Only
// one can be added. Subsequence calls replaces existing implementations.
func (tv *Television) AddReflector(r ReflectionSynchronising) {
	tv.reflector = r
}

// Reset the television to an initial state.
func (tv *Television) Reset() error {
	// we definitely do not call this on television initialisation because the
	// rest of the system may not be yet be in a suitable state

	err := tv.SetSpec(tv.reqSpecID)
	if err != nil {
		return err
	}

	tv.state.horizPos = 0
	tv.state.frameNum = 0
	tv.state.scanline = 0
	tv.state.syncedFrameNum = 0
	tv.state.syncedFrame = false
	tv.state.vsyncCount = 0
	tv.state.lastSignal = signal.SignalAttributes{}

	for _, r := range tv.renderers {
		_ = r.Resize(tv.state.spec, tv.state.top, tv.state.bottom-tv.state.top)
		r.Reset()
	}

	return nil
}

// some televisions may need to conclude and/or dispose of resources
// gently. implementations of End() should call EndRendering() and
// EndMixing() on each PixelRenderer and AudioMixer that has been added.
//
// for simplicity, the Television should be considered unusable
// after EndRendering() has been called.
func (tv *Television) End() error {
	var err error

	// call new frame for all renderers
	for _, r := range tv.renderers {
		err = r.EndRendering()
	}

	// flush audio for all mixers
	for _, m := range tv.mixers {
		err = m.EndMixing()
	}

	return err
}

// Signal updates the current state of the television.
func (tv *Television) Signal(sig signal.SignalAttributes) error {
	// mix audio before we do anything else
	if sig.AudioUpdate {
		for _, m := range tv.mixers {
			err := m.SetAudio(sig.AudioData)
			if err != nil {
				return err
			}
		}
	}

	// examine signal for resizing possibility
	tv.state.resizer.examine(tv, sig)

	// a Signal() is by definition a new color clock. increase the horizontal count
	tv.state.horizPos++

	// once we reach the scanline's back-porch we'll reset the horizPos counter
	// and wait for the HSYNC signal. we do this so that the front-porch and
	// back-porch are 'together' at the beginning of the scanline. this isn't
	// strictly technically correct but it's convenient to think about
	// scanlines in this way (rather than having a split front and back porch)
	if tv.state.horizPos >= specification.HorizClksScanline {
		tv.state.horizPos = 0

		// bump scanline counter
		tv.state.scanline++

		// reached end of screen without VSYNC sequence
		if tv.state.scanline > tv.state.spec.ScanlinesTotal {
			// fly-back naturally if VBlank is off. a good example of a ROM
			// that requires this is Andrew Davies' Chess (3e+ test rom)
			if !tv.state.lastSignal.VBlank {
				err := tv.newFrame(false)
				if err != nil {
					return err
				}
			} else {
				// wait until VSYNC sequence is encountered. note that newFrame
				// is not called because it is assumed that the ROM will
				// eventually send one and is just late.
				tv.state.scanline = tv.state.spec.ScanlinesTotal
			}
		} else {
			// if we're not at end of screen then indicate new scanline
			err := tv.newScanline()
			if err != nil {
				return err
			}
		}
	}

	// check vsync signal at the time of the flyback
	//
	// !!TODO: replace VSYNC signal with extended HSYNC signal
	if sig.VSync && !tv.state.lastSignal.VSync {
		tv.state.vsyncCount = 0
	} else if !sig.VSync && tv.state.lastSignal.VSync {
		if tv.state.vsyncCount > 0 {
			err := tv.newFrame(true)
			if err != nil {
				return err
			}
		}
	}

	// we've "faked" the flyback signal above when horizPos reached
	// horizClksScanline. we need to handle the real flyback signal however, by
	// making sure we're at the correct horizPos value.  if horizPos doesn't
	// equal 16 at the front of the HSYNC or 36 at then back of the HSYNC, then
	// it indicates that the RSYNC register was used last scanline.
	if sig.HSync && !tv.state.lastSignal.HSync {
		tv.state.horizPos = 16

		// count vsync lines at start of hsync
		if sig.VSync || tv.state.lastSignal.VSync {
			tv.state.vsyncCount++
		}
	}
	if !sig.HSync && tv.state.lastSignal.HSync {
		tv.state.horizPos = 36
	}

	// doing nothing with CBURST signal

	// augment television signal before sending to pixel renderer
	sig.HorizPos = tv.state.horizPos
	sig.Scanline = tv.state.scanline

	// record the current signal settings so they can be used for reference
	// during the next call to Signal()
	tv.state.lastSignal = sig

	// record signal history
	if tv.signalIdx >= MaxSignalHistory {
		err := tv.setPendingPixels()
		if err != nil {
			return err
		}
	}
	tv.signals[tv.signalIdx] = sig
	tv.signalIdx++

	if tv.lmtr.scale == scalePixel {
		err := tv.setPendingPixels()
		if err != nil {
			return err
		}
	}

	tv.lmtr.checkPixel()

	return nil
}

func (tv *Television) newScanline() error {
	// notify renderers of new scanline
	for _, r := range tv.renderers {
		err := r.NewScanline(tv.state.scanline)
		if err != nil {
			return err
		}
	}

	if tv.lmtr.scale == scaleScanline {
		err := tv.setPendingPixels()
		if err != nil {
			return err
		}
	}

	tv.lmtr.checkScanline()

	return nil
}

func (tv *Television) newFrame(synced bool) error {
	// specification change
	if tv.state.syncedFrameNum > leadingFrames && tv.state.syncedFrameNum < stabilityThreshold {
		if tv.state.auto && !tv.state.syncedFrame && tv.state.scanline > excessScanlinesNTSC {
			// flip from NTSC to PAL
			if tv.state.spec.ID == specification.SpecNTSC.ID {
				_ = tv.SetSpec("PAL")
			}
		}
	}

	// a synced frame is one which was generated from a valid VSYNC/VBLANK sequence
	if synced {
		tv.state.syncedFrameNum++
	}
	tv.state.syncedFrame = synced

	// commit any resizing that maybe pending
	err := tv.state.resizer.commit(tv)
	if err != nil {
		return err
	}

	// prepare for next frame
	tv.state.frameNum++
	tv.state.scanline = 0
	tv.state.resizer.prepare(tv)

	// set pixels for all renderers
	if tv.lmtr.scale == scaleFrame {
		err = tv.setPendingPixels()
		if err != nil {
			return err
		}
	}

	// process all FrameTriggers
	for _, r := range tv.frameTriggers {
		err = r.NewFrame(tv.IsStable())
		if err != nil {
			return err
		}
	}

	// reset reflector for new frame
	if tv.reflector != nil {
		tv.reflector.SyncFrame()
	}

	tv.lmtr.checkFrame()

	return nil
}

// setPendindPixels forwards all pixels in the signalHistory buffer (between
// the *from and *to values) to all pixel renderers.
func (tv *Television) setPendingPixels() error {
	for _, r := range tv.renderers {
		r.UpdatingPixels(true)
		for i := 0; i < tv.signalIdx; i++ {
			sig := tv.signals[i]
			err := r.SetPixel(sig, true)
			if err != nil {
				return curated.Errorf("television", err)
			}
			if tv.reflector != nil {
				tv.reflector.SyncReflectionPixel(i)
			}
		}
		r.UpdatingPixels(false)
	}

	// reset signal history
	tv.signalIdx = 0

	return nil
}

// IsStable returns true if the television thinks the image being sent by
// the VCS is stable.
func (tv *Television) IsStable() bool {
	return tv.state.syncedFrameNum >= stabilityThreshold
}

// Returns a copy of SignalAttributes for reference.
func (tv *Television) GetLastSignal() signal.SignalAttributes {
	return tv.state.lastSignal
}

// Returns state information.
func (tv *Television) GetState(request signal.StateReq) int {
	return tv.state.GetState(request)
}

// Set the television's specification.
func (tv *Television) SetSpec(spec string) error {
	switch strings.ToUpper(spec) {
	case "NTSC":
		tv.state.spec = specification.SpecNTSC
		tv.state.auto = false
	case "PAL":
		tv.state.spec = specification.SpecPAL
		tv.state.auto = false
	case "AUTO":
		tv.state.spec = specification.SpecNTSC
		tv.state.auto = true
	default:
		return curated.Errorf("television: unsupported spec (%s)", spec)
	}

	tv.state.top = tv.state.spec.AtariSafeTop
	tv.state.bottom = tv.state.spec.AtariSafeBottom
	tv.state.resizer.prepare(tv)
	tv.lmtr.setRate(tv.state.spec.FramesPerSecond)

	for _, r := range tv.renderers {
		err := r.Resize(tv.state.spec, tv.state.top, tv.state.bottom-tv.state.top)
		if err != nil {
			return err
		}
	}

	return nil
}

// GetReqSpecID returns the specification that was requested on creation.
func (tv *Television) GetReqSpecID() string {
	return tv.reqSpecID
}

// Returns the television's current specification. Renderers should use
// GetSpec() rather than keeping a private pointer to the specification.
func (tv *Television) GetSpec() specification.Spec {
	return tv.state.spec
}

// Pause indicates that emulation has been paused. All renderers will pause
// rendering and pending pixels pushed.
func (tv *Television) Pause(pause bool) error {
	if pause {
		return tv.setPendingPixels()
	}
	return nil
}

// ForceDraw pushes all pending pixels to the pixel renderers.
func (tv *Television) ForceDraw() error {
	err := tv.setPendingPixels()
	if err != nil {
		return err
	}
	return nil
}

// SetFPSCap whether the emulation should wait for FPS limiter. Returns the
// setting as it was previously.
func (tv *Television) SetFPSCap(limit bool) bool {
	cap := tv.lmtr.limit
	tv.lmtr.limit = limit
	return cap
}

// Request the number frames per second. This overrides the frame rate of
// the specification. A negative  value restores the spec's frame rate.
func (tv *Television) SetFPS(fps float32) {
	_ = tv.setPendingPixels()
	tv.lmtr.setRate(fps)
}

// The requested number of frames per second. Compare with GetActualFPS()
// to check for accuracy.
//
// IS goroutine safe.
func (tv *Television) GetReqFPS() float32 {
	return tv.lmtr.requested.Load().(float32)
}

// The current number of frames per second. Note that FPS measurement still
// works even when frame capping is disabled.
//
// IS goroutine safe.
func (tv *Television) GetActualFPS() float32 {
	return tv.lmtr.actual.Load().(float32)
}