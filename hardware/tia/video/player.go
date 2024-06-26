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

package video

import (
	"fmt"
	"strings"

	"github.com/jetsetilly/gopher2600/hardware/television/specification"
	"github.com/jetsetilly/gopher2600/hardware/tia/delay"
	"github.com/jetsetilly/gopher2600/hardware/tia/phaseclock"
	"github.com/jetsetilly/gopher2600/hardware/tia/polycounter"
	"github.com/jetsetilly/gopher2600/hardware/tia/revision"
)

// PlayerSizes maps player size and copies values to descriptions of those
// values.
var PlayerSizes = []string{
	"one copy",
	"two copies [close]",
	"two copies [med]",
	"three copies [close]",
	"two copies [wide]",
	"double size",
	"three copies [med]",
	"quad size",
}

// playerSizesBrief maps player size and copies values to brief descriptions of
// those values.
var playerSizesBrief = []string{
	"",
	"2 [close]",
	"2 [med]",
	"3 [close]",
	"2 [wide]",
	"double",
	"3 [med]",
	"quad",
}

// PlayerSprite represents a moveable player sprite in the VCS graphical display.
// The VCS has two player sprites.
type PlayerSprite struct {
	// references to some fundamental TIA properties. various combinations of
	// these affect the latching delay when resetting the sprite
	tia tia

	// ^^^ references to other parts of the VCS ^^^

	// position of the sprite as a polycounter value - the basic principle
	// behind VCS sprites is to begin drawing the sprite when position
	// circulates to zero
	position polycounter.Polycounter

	// "Beside each counter there is a two-phase clock generator..."
	pclk phaseclock.PhaseClock

	// horizontal movement
	MoreHMOVE bool
	Hmove     uint8

	// the following attributes are used for information purposes only:

	// the name of the sprite instance (eg. "player 0")
	label string

	// the pixel at which the sprite was reset. in the case of the ball and
	// missile sprites the scan counter starts at the ResetPixel. for the
	// player sprite however, there is additional latching to consider. rather
	// than introducing an additional variable keeping track of the start
	// pixel, the ResetPixel is modified according to the player sprite's
	// current NUSIZ.
	ResetPixel int

	// the pixel at which the sprite was reset plus any HMOVE modification see
	// prepareForHMOVE() for a note on the presentation of HmovedPixel
	HmovedPixel int

	// ^^^ the above are common to all sprite types ^^^

	// player sprite attributes
	Color         uint8 // equal to missile color
	Reflected     bool
	VerticalDelay bool

	// which gfx register we use depends on the value of vertical delay
	GfxDataNew uint8
	GfxDataOld uint8
	gfxData    *uint8

	// for convenience we store the raw NUSIZ value and the significant size
	// and copy bits
	Nusiz         uint8 // the raw value from the NUSIZ register
	SizeAndCopies uint8 // just the three left-most bits

	// position reset and enclockifier start events are both delayed by a small
	// number of cycles
	futureReset delay.Event
	futureStart delay.Event

	// unique to the player sprite, setting of the nusiz value is also delayed
	// under certain conditions
	futureSetNUSIZ delay.Event

	// ScanCounter implements the "graphics scan counter" as described in
	// TIA_HW_Notes.txt:
	//
	// "The Player Graphics Scan Counters are 3-bit binary ripple counters
	// attached to the player objects, used to determine which pixel
	// of the player is currently being drawn by generating a 3-bit
	// source pixel address. These are the only binary ripple counters
	// in the TIA."
	//
	// equivalent to the Enclockifier used by the ball and missile sprites
	ScanCounter scanCounter

	// state of player "pixel"
	pixelOn        bool
	pixelCollision bool
}

func newPlayerSprite(label string, tia tia) *PlayerSprite {
	ps := &PlayerSprite{
		label: label,
		tia:   tia,
	}
	ps.ScanCounter.Pixel = -1

	ps.ScanCounter.sizeAndCopies = &ps.SizeAndCopies
	ps.ScanCounter.pclk = &ps.pclk
	ps.gfxData = &ps.GfxDataNew

	ps.position = polycounter.ResetValue

	return ps
}

// Snapshot creates a copy of the player sprite in its current state.
func (ps *PlayerSprite) Snapshot() *PlayerSprite {
	n := *ps
	return &n
}

// Plumb changes into player sprite.
func (ps *PlayerSprite) Plumb(tia tia) {
	ps.tia = tia
	ps.ScanCounter.sizeAndCopies = &ps.SizeAndCopies
	ps.ScanCounter.pclk = &ps.pclk

	if ps.VerticalDelay {
		ps.gfxData = &ps.GfxDataOld
	} else {
		ps.gfxData = &ps.GfxDataNew
	}
}

// Label returns an appropriate name for the sprite.
func (ps *PlayerSprite) Label() string {
	return ps.label
}

func (ps *PlayerSprite) String() string {
	// the hmove value as maintained by the sprite type is normalised for
	// for purposes of presentation
	normalisedHmove := int(ps.Hmove) - 8
	if normalisedHmove < 0 {
		normalisedHmove = 16 + normalisedHmove
	}

	s := strings.Builder{}
	s.WriteString(ps.label)
	s.WriteString(": ")
	s.WriteString(fmt.Sprintf("%s %s [%03d ", ps.position, ps.pclk, ps.ResetPixel))
	s.WriteString(fmt.Sprintf("> %#1x >", normalisedHmove))
	s.WriteString(fmt.Sprintf(" %03d", ps.HmovedPixel))
	if ps.MoreHMOVE {
		s.WriteString("*]")
	} else {
		s.WriteString("]")
	}

	// add a note to indicate that the nusiz value is about to update
	if ps.ScanCounter.IsActive() && ps.SizeAndCopies != ps.ScanCounter.LatchedSizeAndCopies {
		s.WriteString(" *")
	}

	// nusiz info
	if int(ps.SizeAndCopies) > len(playerSizesBrief) {
		panic("illegal size value for player")
	}
	playerSize := playerSizesBrief[ps.SizeAndCopies]
	if playerSize != "" {
		s.WriteString(fmt.Sprintf(" %s", playerSize))
	}

	// hmove information
	if ps.MoreHMOVE {
		s.WriteString(fmt.Sprintf(" hmoving [%04b],", ps.Hmove))
	}

	// drawing or latching information
	if ps.ScanCounter.IsActive() {
		s.WriteString(fmt.Sprintf(" drw (px %d", ps.ScanCounter.Pixel))

		// add "sub-pixel" information. this happens when the player sprite is
		// being stretched by NUSIZ
		if ps.ScanCounter.count > 0 {
			s.WriteString(fmt.Sprintf(".%d", ps.ScanCounter.count))
		}

		s.WriteString("),")
	} else if ps.ScanCounter.IsLatching() {
		s.WriteString(fmt.Sprintf(" latch (drw in %d),", ps.ScanCounter.latch))
	}

	// copy information if drawing or latching and nusiz is a multiple copy
	// value
	if (ps.ScanCounter.IsActive() || ps.ScanCounter.IsLatching()) &&
		ps.SizeAndCopies != 0x0 && ps.SizeAndCopies != 0x5 && ps.SizeAndCopies != 0x07 {
		switch ps.ScanCounter.Cpy {
		case 0:
		case 1:
			s.WriteString(" 2nd,")
		case 2:
			s.WriteString(" 3rd,")
		default:
			panic("more than 2 copies of player!?")
		}
	}

	if ps.VerticalDelay {
		s.WriteString(" vdel,")
	}
	if ps.Reflected {
		s.WriteString(" ref,")
	}

	return strings.TrimSuffix(s.String(), ",")
}

func (ps *PlayerSprite) rsync(adjustment int) {
	ps.ResetPixel -= adjustment
	ps.HmovedPixel -= adjustment
	if ps.ResetPixel < 0 {
		ps.ResetPixel += specification.ClksVisible
	}
	if ps.HmovedPixel < 0 {
		ps.HmovedPixel += specification.ClksVisible
	}
}

func (ps *PlayerSprite) tickHBLANK() bool {
	// check to see if there is more movement required for this sprite
	ps.MoreHMOVE = ps.MoreHMOVE && compareHMOVE(ps.tia.hmove.Ripple, ps.Hmove)
	if !ps.MoreHMOVE {
		return false
	}

	// update hmoved pixel value & adjust for screen boundary
	ps.HmovedPixel--
	if ps.HmovedPixel < 0 {
		ps.HmovedPixel += specification.ClksVisible
	}

	return ps.tick()
}

func (ps *PlayerSprite) tickHMOVE() bool {
	// check to see if there is more movement required for this sprite
	ps.MoreHMOVE = ps.MoreHMOVE && compareHMOVE(ps.tia.hmove.Ripple, ps.Hmove)

	// cancel motion clock if necessary
	if ps.MoreHMOVE && ps.tia.env.Prefs.Revision.Live.LostMOTCK.Load().(bool) {
		return false
	}

	return ps.tick()
}

// returns true if pixel has changed.
func (ps *PlayerSprite) tick() bool {
	// tick graphics scan counter during visible screen and during HMOVE.
	// from TIA_HW_Notes.txt:
	//
	// "Note that a HMOVE can gobble up the wrapped player graphics"
	//
	// in addition, the size value for the player affects how often the
	// scan counter ticks. from TIA_HW_Notes.txt:
	//
	// "The count frequency is determined by the NUSIZ register for that
	// player; this is used to selectively mask off the clock signals to
	// the Graphics Scan Counter. Depending on the player stretch mode, one
	// clock signal is allowed through every 1, 2 or 4 graphics CLK.  The
	// stretched modes are derived from the two-phase clock; the H@2 phase
	// allows 1 in 4 CLK through (4x stretch), both phases ORed together
	// allow 1 in 2 CLK through (2x stretch)."
	ps.ScanCounter.tick()

	// tick phase clock after scancounter tick
	ps.pclk++
	if ps.pclk >= phaseclock.NumStates {
		ps.pclk = 0
	}

	// I cannot find a direct reference that describes when position
	// counters are ticked forward. however, TIA_HW_Notes.txt does say the
	// HSYNC counter ticks forward on the rising edge of Phi2. it is
	// reasonable to assume that the sprite position counters do likewise.
	if ps.pclk == phaseclock.RisingPhi2 {
		ps.position++
		if ps.position >= polycounter.LenTable6Bit {
			ps.position = 0
		}

		// drawing must not start if a reset position event has been
		// recently scheduled.
		//
		// rules discovered through observation (games that do bad things
		// to HMOVE)
		if !ps.futureReset.IsActive() || ps.futureReset.JustStarted() {
			// startDrawingEvent is delayed by 5 ticks. from TIA_HW_Notes.txt:
			//
			// "Each START decode is delayed by 4 CLK in decoding, plus a
			// further 1 CLK to latch the graphics scan counter..."
			//
			// the "further 1 CLK" is actually a further 2 CLKs in the case of
			// 2x and 4x size sprites. we'll handle the additional latching in
			// the scan counter
			//
			// note that the additional latching has an impact of what we
			// report as being the reset pixel.

			// "... The START decodes are ANDed with flags from the NUSIZ register
			// before being latched, to determine whether to draw that copy."
			switch ps.position {
			case 3:
				if ps.SizeAndCopies == 0x01 || ps.SizeAndCopies == 0x03 {
					ps.futureStart.Schedule(4, 1)
				}
			case 7:
				if ps.SizeAndCopies == 0x03 || ps.SizeAndCopies == 0x02 || ps.SizeAndCopies == 0x06 {
					cpy := 1
					if ps.SizeAndCopies == 0x03 {
						cpy = 2
					}
					ps.futureStart.Schedule(4, uint8(cpy))
				}
			case 15:
				if ps.SizeAndCopies == 0x04 || ps.SizeAndCopies == 0x06 {
					cpy := 1
					if ps.SizeAndCopies == 0x06 {
						cpy = 2
					}
					ps.futureStart.Schedule(4, uint8(cpy))
				}
			case 39:
				ps.futureStart.Schedule(4, 0)

			case 40:
				ps.position = polycounter.ResetValue
			}
		}
	}

	// tick delayed events
	ps.futureSetNUSIZ.Tick(ps._futureSetNUSIZ)
	ps.futureReset.Tick(ps._futureResetPosition)
	ps.futureStart.Tick(ps._futureStartDrawingEvent)

	return true
}

func (ps *PlayerSprite) _futureStartDrawingEvent(v uint8) {
	// it is useful for debugging to know which copy of the sprite is
	// currently being drawn. we'll update this value in the switch
	// below, taking great care to note the value of ms.copies at each
	// trigger point
	//
	// this is used by the missile sprites when in reset-to-player
	// mode
	ps.ScanCounter.Cpy = int(v)
	ps.ScanCounter.start()
}

func (ps *PlayerSprite) prepareForHMOVE() {
	// the latching delay should already have been consumed when servicing the
	// HMOVE signal in tia.go

	ps.MoreHMOVE = true

	if *ps.tia.hblank {
		// adjust hmovedPixel value. this value is subject to further change so
		// long as moreHMOVE is true. the String() function this value is
		// annotated with a "*" to indicate that HMOVE is still in progress
		ps.HmovedPixel += 8

		// adjust for screen boundary
		if ps.HmovedPixel > specification.ClksVisible {
			ps.HmovedPixel -= specification.ClksVisible
		}
	}
}

func (ps *PlayerSprite) resetPosition() {
	// delay of 5 clocks using. from TIA_HW_Notes.txt:
	//
	// "This arrangement means that resetting the player counter on any
	// visible pixel will cause the main copy of the player to appear
	// at that same pixel position on the next and subsequent scanlines.
	// There are 5 CLK worth of clocking/latching to take into account,
	// so the actual position ends up 5 pixels to the right of the
	// reset pixel (approx. 9 pixels after the start of STA RESP0)."
	delay := 4

	hblank := *ps.tia.hblank

	// RESPx responding late to the end of HBLANK is dependent on heat. The
	// HeatThreshold() function handles the increasing operating temperature
	// for us.
	if (*ps.tia.hsync == 16 || *ps.tia.hsync == 18) && *ps.tia.pclk == phaseclock.RisingPhi2 {
		if ps.tia.env.Prefs.Revision.Live.RESPxHBLANK.Load().(bool) {
			hblank = !revision.HeatThreshold(ps.tia.tv.GetCoords().Scanline)
		}
	}

	// if we're scheduling the reset during a HBLANK however there are extra
	// conditions which adjust the delay value. these figures have been gleaned
	// through observation. with some supporting notes from the following
	// threads.
	//
	// https://atariage.com/forums/topic/207444-questionproblem-about-sprite-positioning-during-hblank/
	// https://atariage.com/forums/topic/311795-576-and-1008-characters/?tab=comments#comment-4748106
	// https://github.com/stella-emu/stella/issues/699#issuecomment-698004074
	//
	// that said, I'm not entirely sure what's going on and why these
	// adjustments are required.
	//
	// the slideshow ROM in the link below is "wrong" unless LateRESPx is
	// true. it's not clear if this is correct and whether this coincides with
	// the
	//
	// https://atariage.com/forums/topic/181816-bigger-bitmaps-with-dpc/
	if hblank {
		// this tricky branch happens when reset is triggered inside the
		// HBLANK period and HMOVE is active in some way.

		if ps.tia.hmove.Ripple >= 1 && ps.tia.hmove.Ripple <= 15 {
			// HMOVE is currently rippling note that HMOVE does not need to
			// have been latched for this to be true
			delay = 2

			// if HMOVE is latched or ripple has just started then check the
			// TIA revision for a longer delay
			if ps.tia.hmove.Latch || ps.tia.hmove.Ripple == 15 {
				if ps.tia.env.Prefs.Revision.Live.LateRESPx.Load().(bool) {
					delay = 3
				}
			}
		} else if ps.tia.hmove.Latch {
			// HMOVE has been activated this scanline but not currently rippling.
			//
			// maybe surprisingly, this is comparatively unusual. many ROMs if
			// the reset the player during the HBLANK at all will have called
			// HMOVE straight after the WSYNC, as advised by the Stella
			// Programmer's Guide.
			//
			// a good examples of where it is required is Midnight Madness
			// (there is a gap in the crossbar of the T of 'Midnite')
			delay = 3
		} else {
			// HMOVE has not been activated at all this scanline
			delay = 2
		}
	}

	// pause pending start drawing events unless it is about to start this
	// cycle rules discovered through observation (principally, 'games that do
	// bad things to HMOVE')
	if !ps.futureStart.AboutToEnd() {
		// not entirely sure this condition is correct but works for the known cases
		//
		// https://atariage.com/forums/topic/311795-576-and-1008-characters/?tab=comments#comment-4748106
		if ps.tia.env.Prefs.Revision.Live.EarlyScancounter.Load().(bool) {
			if !(ps.tia.hmove.RippleJustEnded && (ps.pclk == phaseclock.RisingPhi1 || ps.pclk == phaseclock.FallingPhi1)) {
				ps.futureStart.Pause()
			}
		} else {
			ps.futureStart.Pause()
		}
	}

	// stop any existing reset events. generally, this codepath will not apply
	// because a resetPositionEvent will conculde before being triggered again.
	// but it is possible when using a very quick instruction on the reset register,
	// like a zero page INC, for requests to overlap
	if ps.futureReset.IsActive() {
		ps.futureReset.Push()
		return
	}

	ps.futureReset.Schedule(delay, 0)
}

func (ps *PlayerSprite) _futureResetPosition(_ uint8) {
	// the pixel at which the sprite has been reset, in relation to the
	// left edge of the screen
	ps.ResetPixel = ps.tia.tv.GetCoords().Clock

	if ps.ResetPixel >= 0 {
		// resetPixel adjusted by +1 because the tv is not yet in the correct.
		// position. and another +1 because of the latching required before
		// player sprites begin drawing
		ps.ResetPixel += 2

		// if size is 2x or 4x then we need an additional reset pixel
		//
		// note that we need to monkey with resetPixel whenever NUSIZ changes.
		// see setNUSIZ() function below
		if ps.SizeAndCopies == 0x05 || ps.SizeAndCopies == 0x07 {
			ps.ResetPixel++
		}

		// adjust resetPixel for screen boundaries
		if ps.ResetPixel > specification.ClksVisible {
			ps.ResetPixel -= specification.ClksVisible
		}

		// by definition the current pixel is the same as the reset pixel at
		// the moment of reset
		ps.HmovedPixel = ps.ResetPixel
	} else {
		// if reset occurs off-screen then force reset pixel to be zero
		// (see commentary in ball sprite for detailed reasoning of this
		// branch)
		ps.ResetPixel = 0
		ps.HmovedPixel = 7
	}

	// reset both sprite position and clock
	ps.position = polycounter.ResetValue
	ps.pclk = phaseclock.ResetValue

	// a player reset doesn't normally start drawing straight away unless
	// one was a about to start
	//
	// rules discovered through observation (games that do bad things to HMOVE)
	if ps.futureStart.IsActive() {
		v := ps.futureStart.Force()
		ps._futureStartDrawingEvent(v)
	}
}

// always return player color because when in "scoremode" the playfield
// wants to know the color of the player.
func (ps *PlayerSprite) pixel() {
	// pick the pixel from the gfxData register
	if ps.ScanCounter.IsActive() {
		var offset int

		if ps.Reflected {
			offset = 7 - ps.ScanCounter.Pixel
		} else {
			offset = ps.ScanCounter.Pixel
		}

		ps.pixelOn = *ps.gfxData>>offset&0x01 == 0x01
		ps.pixelCollision = ps.pixelOn

		return
	}

	ps.pixelOn = false
	ps.pixelCollision = false

	// scancounter is not active but we still need to check what the first
	// pixel in the scancounter is in case the the player is latching in the
	// HBLANK period.
	//
	// we can see this on the 3rd screen to the left of Keystone Kapers. the
	// ball on the second corridor will wrongly collide with the policeman
	// unless we take into account the first pixel of the scancounter
	//
	// (maybe surprisingly, collision detction in Pitfall is sensitive to this,
	// which makes it a good test case)

	if *ps.tia.hblank && ps.ScanCounter.IsLatching() {
		if ps.Reflected {
			ps.pixelCollision = (*ps.gfxData>>7)&0x01 == 0x01
		} else {
			ps.pixelCollision = *ps.gfxData&0x01 == 0x01
		}
	}
}

func (ps *PlayerSprite) setGfxData(data uint8) {
	ps.GfxDataNew = data
}

// from TIA_HW_Notes.txt:
//
// "Writes to GRP0 always modify the "new" P0 value, and the contents of
// the "new" P0 are copied into "old" P0 whenever GRP1 is written.
// (Likewise, writes to GRP1 always modify the "new" P1 value, and the
// contents of the "new" P1 are copied into "old" P1 whenever GRP0 is
// written). It is safe to modify GRPn at any time, with immediate effect."
//
// the significance of this wasn't clear to me originally but was
// crystal clear after reading Erik Mooney's post, "48-pixel highres
// routine explained".
func (ps *PlayerSprite) setOldGfxData() {
	ps.GfxDataOld = ps.GfxDataNew
}

// SetVerticalDelay bit also alters which gfx registers is being used.
// Debuggers should use this function to set the delay bit rather than setting
// it directly.
func (ps *PlayerSprite) SetVerticalDelay(vdelay bool) {
	// from TIA_HW_Notes.txt:
	//
	// "Vertical Delay bit - this is also read every time a pixel is generated
	// and used to select which of the "new" (0) or "old" (1) Player Graphics
	// registers is used to generate the pixel. (ie the pixel is retrieved from
	// both registers in parallel, and this flag used to choose between them at
	// the graphics output).  It is safe to modify VDELPn at any time, with
	// immediate effect."
	ps.VerticalDelay = vdelay

	if ps.VerticalDelay {
		ps.gfxData = &ps.GfxDataOld
	} else {
		ps.gfxData = &ps.GfxDataNew
	}
}

func (ps *PlayerSprite) setReflection(value bool) {
	// from TIA_HW_Notes.txt:
	//
	// "Player Reflect bit - this is read every time a pixel is generated,
	// and used to conditionally invert the bits of the source pixel
	// address. This has the effect of flipping the player image drawn.
	// This flag could potentially be changed during the rendering of
	// the player, for example this might be used to draw bits 01233210."
	ps.Reflected = value
}

// !!TODO: the setNUSIZ() function needs untangling. I reckon with a bit of
// reordering we can simplify it quite a bit.
func (ps *PlayerSprite) setNUSIZ(value uint8) {
	// from TIA_HW_Notes.txt:
	//
	// "The NUSIZ register can be changed at any time in order to alter
	// the counting frequency, since it is read every graphics CLK.
	// This should allow possible player graphics warp effects etc."

	// whilst the notes say that the register can be changed at any time, there
	// is a delay of sorts in certain situations; although  under most
	// circumstances, TIA_HW_Notes is correct, there is no delay.
	//
	// for convenience, we still call the Schedule() function but with a delay
	// value of -1 (see Schedule() function notes)
	delay := -1

	if ps.futureStart.IsActive() {
		if ps.SizeAndCopies == 0x05 || ps.SizeAndCopies == 0x07 {
			delay = 0
		} else if ps.futureStart.Remaining() == 0 {
			delay = 1
		} else if ps.futureStart.Remaining() >= 2 &&
			ps.SizeAndCopies != value && ps.SizeAndCopies != 0x00 &&
			(value == 0x05 || value == 0x07) {
			// this branch applies when a sprite is changing from a single
			// width sprite to a double/quadruple width sprite. in that
			// instance we drop the drawing event if it has only recently
			// started
			//
			// I'm not convinced by this branch at all but the rule was
			// discovered through observation and balancing of the test roms:
			//
			//  o player_switching.bin
			//	o testSize2Copies_A.bin
			//	o properly_model_nusiz_during_player_decode_and_draw/player8.bin
			//
			// the rules maybe more subtle or more general than this
			ps.futureStart.Drop()
		}
	} else if ps.ScanCounter.IsLatching() || ps.ScanCounter.IsActive() {
		if (ps.SizeAndCopies == 0x05 || ps.SizeAndCopies == 0x07) && (value == 0x05 || value == 0x07) {
			// minimal delay if future/current NUSIZ is double/quadruple width
			delay = 0
		} else {
			delay = 1
		}
	}

	if delay >= 0 {
		ps.futureSetNUSIZ.Schedule(delay, value)
	} else {
		ps.SetNUSIZ(value)
	}
}

func (ps *PlayerSprite) _futureSetNUSIZ(v uint8) {
	ps.SetNUSIZ(v)
}

// SetNUSIZ is called when the NUSIZ register changes, after a delay. It should
// also be used to set the NUSIZ value from a debugger for immediate effect.
// Setting the value directly will upset reset/hmove pixel information.
func (ps *PlayerSprite) SetNUSIZ(value uint8) {
	// if size is 2x or 4x currently then take off the additional pixel. we'll
	// add it back on afterwards if needs be
	if ps.SizeAndCopies == 0x05 || ps.SizeAndCopies == 0x07 {
		ps.ResetPixel--
		ps.HmovedPixel--
	}

	// note raw NUSIZ value
	ps.Nusiz = value

	// for convenience we pick out the size/count values from the raw NUSIZ
	// value
	ps.SizeAndCopies = value & 0x07

	// if size is 2x or 4x then we need to record an additional pixel on the
	// reset point value
	if ps.SizeAndCopies == 0x05 || ps.SizeAndCopies == 0x07 {
		ps.ResetPixel++
		ps.HmovedPixel++
	}

	// adjust reset pixel for screen boundaries
	if ps.ResetPixel > specification.ClksVisible {
		ps.ResetPixel -= specification.ClksVisible
	}
	if ps.HmovedPixel > specification.ClksVisible {
		ps.HmovedPixel -= specification.ClksVisible
	}
}

func (ps *PlayerSprite) setColor(value uint8) {
	// there is nothing in TIA_HW_Notes.txt about the color registers
	ps.Color = value
}

// setHmoveValue normalises the nibbles from the NUSIZ register.
func (ps *PlayerSprite) setHmoveValue(v uint8) {
	ps.Hmove = (v ^ 0x80) >> 4
}

func (ps *PlayerSprite) clearHmoveValue() {
	ps.Hmove = 0x08
}

// reset missile to player position. from TIA_HW_Notes.txt:
//
// "The Missile-to-player reset is implemented by resetting the M0 counter
// when the P0 graphics scan counter is at %100 (in the middle of drawing
// the player graphics) AND the main copy of P0 is being drawn (ie the
// missile counter will not be reset when a subsequent copy is drawn, if
// any). This second condition is generated from a latch outputting [FSTOB]
// that is reset when the P0 counter wraps around, and set when the START
// signal is decoded for a 'close', 'medium' or 'far' copy of P0."
//
// note: the FSTOB output is the primary flag in the parent player's
// scancounter.
func (ps *PlayerSprite) triggerMissileReset() bool {
	if ps.ScanCounter.Cpy != 0 {
		return false
	}

	switch ps.SizeAndCopies {
	case 0x05:
		return ps.ScanCounter.Pixel == 4 && ps.ScanCounter.count == 1
	case 0x07:
		return ps.ScanCounter.Pixel == 5 && ps.ScanCounter.count == 3
	}
	return ps.ScanCounter.Pixel == 2
}
