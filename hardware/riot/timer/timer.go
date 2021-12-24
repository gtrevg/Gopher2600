// Tjis file is part of Gopher2600.
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

package timer

import (
	"fmt"

	"github.com/jetsetilly/gopher2600/hardware/instance"
	"github.com/jetsetilly/gopher2600/hardware/memory/chipbus"
	"github.com/jetsetilly/gopher2600/hardware/memory/cpubus"
)

// Interval indicates how often (in CPU cycles) the timer value decreases.
// the following rules apply:
//		* set to 1, 8, 64 or 1024 depending on which address has been
//			written to by the CPU
//		* is used to reset the cyclesRemaining
//		* is changed to 1 once value reaches 0
//		* is reset to its initial value of 1, 8, 64, or 1024 whenever INTIM
//			is read by the CPU
type Interval int

// List of valid Interval values.
const (
	TIM1T  Interval = 1
	TIM8T  Interval = 8
	TIM64T Interval = 64
	T1024T Interval = 1024
)

func (in Interval) String() string {
	switch in {
	case TIM1T:
		return "TIM1T"
	case TIM8T:
		return "TIM8T"
	case TIM64T:
		return "TIM64T"
	case T1024T:
		return "T1024T"
	}
	panic("unknown timer interval")
}

// Timer implements the timer part of the PIA 6532 (the T in RIOT).
type Timer struct {
	mem chipbus.Memory

	instance *instance.Instance

	// the interval value most recently requested by the CPU
	Divider Interval

	// INTIMvalue is the current timer value and is a reflection of the INTIM
	// RIOT memory register. set with SetValue() function
	INTIMvalue uint8

	// the state of TIMINT. use timintValue() when writing to register
	expired bool
	pa7     bool

	// TicksRemaining is the number of CPU cycles remaining before the
	// value is decreased. the following rules apply:
	//		* set to 0 when new timer is set
	//		* causes value to decrease whenever it reaches -1
	//		* is reset to divider whenever value is decreased
	//
	// with regards to the last point, note that divider changes to 1
	// once INTIMvalue reaches 0
	TicksRemaining int
}

// NewTimer is the preferred method of initialisation of the Timer type.
func NewTimer(instance *instance.Instance, mem chipbus.Memory) *Timer {
	tmr := &Timer{
		instance: instance,
		mem:      mem,
		Divider:  T1024T,
	}

	tmr.Reset()

	return tmr
}

// Reset timer to an initial state.
func (tmr *Timer) Reset() {
	tmr.pa7 = true

	if tmr.instance.Prefs.RandomState.Get().(bool) {
		tmr.Divider = T1024T
		tmr.TicksRemaining = tmr.instance.Random.Intn(0xffff)
		tmr.INTIMvalue = uint8(tmr.instance.Random.Intn(0xff))
	} else {
		tmr.Divider = T1024T
		tmr.TicksRemaining = int(T1024T)
		tmr.INTIMvalue = 0
	}

	tmr.mem.ChipWrite(chipbus.INTIM, tmr.INTIMvalue)
	tmr.mem.ChipWrite(chipbus.TIMINT, tmr.timintValue())
}

// Snapshot creates a copy of the RIOT Timer in its current state.
func (tmr *Timer) Snapshot() *Timer {
	n := *tmr
	return &n
}

// Plumb a new ChipBus into the Timer.
func (tmr *Timer) Plumb(mem chipbus.Memory) {
	tmr.mem = mem
}

func (tmr *Timer) String() string {
	return fmt.Sprintf("INTIM=%#02x remn=%#02x intv=%s TIMINT=%v",
		tmr.INTIMvalue,
		tmr.TicksRemaining,
		tmr.Divider,
		tmr.expired,
	)
}

func (tmr *Timer) timintValue() uint8 {
	v := uint8(0)
	if tmr.expired {
		v |= 0x80
	}
	if tmr.pa7 {
		v |= 0x40
	}
	return v
}

// Update checks to see if ChipData applies to the Timer type and updates the
// internal timer state accordingly.
//
// Returns true if ChipData has *not* been serviced.
func (tmr *Timer) Update(data chipbus.ChangedRegister) bool {
	if tmr.SetInterval(data.Register) {
		return true
	}

	// writing to INTIM register has a similar effect on the expired bit of the
	// TIMINT register as reading. See commentary in the Step() function
	if tmr.TicksRemaining == 0 && tmr.INTIMvalue == 0xff {
		tmr.expired = true
		tmr.mem.ChipWrite(chipbus.TIMINT, tmr.timintValue())
	} else {
		tmr.expired = false
		tmr.mem.ChipWrite(chipbus.TIMINT, tmr.timintValue())
	}

	tmr.INTIMvalue = data.Value

	// the ticks remaining value should be zero or one for accurate timing (as
	// tested with these test ROMs https://github.com/stella-emu/stella/issues/108).
	//
	// I'm not sure which value is correct so setting at zero until there's a
	// good reason to do otherwise
	//
	// note however, the internal values in the emulated machine (and as reported by
	// the debugger) will not match the debugging values in stella. to match
	// the debugging values in stella a value of 2 is required.
	tmr.TicksRemaining = 0

	// write value to INTIM straight-away
	tmr.mem.ChipWrite(chipbus.INTIM, tmr.INTIMvalue)

	return false
}

// Step timer forward one cycle.
func (tmr *Timer) Step() {
	if ok, a := tmr.mem.LastReadAddress(); ok {
		switch cpubus.Read[a] {
		case cpubus.INTIM:
			// if INTIM is *read* then the decrement reverts to once per timer
			// interval. this won't have any discernable effect unless the timer
			// interval has been flipped to 1 when INTIM cycles back to 255
			//
			// if the expired flag has *just* been set (ie. in the previous cycle)
			// then do not do the reversion. see discussion:
			//
			// https://atariage.com/forums/topic/303277-to-roll-or-not-to-roll/
			//
			// https://atariage.com/forums/topic/133686-please-explain-riot-timmers/?do=findComment&comment=1617207
			if tmr.TicksRemaining != 0 || tmr.INTIMvalue != 0xff {
				tmr.expired = false
				tmr.mem.ChipWrite(chipbus.TIMINT, tmr.timintValue())
			}
		case cpubus.TIMINT:
			// from the NMOS 6532:
			//
			// "Clearing of the PA7 Interrupt Flag occurs when the microprocessor
			// reads the Interrupt Flag Register."
			//
			// and from the Rockwell 6532 documentation:
			//
			// "To clear PA7 interrupt flag, simply read the Interrupt Flag
			// Register"
			tmr.pa7 = false
		}
	}

	tmr.TicksRemaining--
	if tmr.TicksRemaining <= 0 {
		tmr.INTIMvalue--
		if tmr.INTIMvalue == 0xff {
			tmr.expired = true
			tmr.mem.ChipWrite(chipbus.TIMINT, tmr.timintValue())
		}

		// copy value to INTIM memory register
		tmr.mem.ChipWrite(chipbus.INTIM, tmr.INTIMvalue)

		if tmr.expired {
			tmr.TicksRemaining = 0
		} else {
			tmr.TicksRemaining = int(tmr.Divider)
		}
	}
}

// SetValue sets the timer value. Prefer this to setting INTIMvalue directly.
func (tmr *Timer) SetValue(value uint8) {
	tmr.INTIMvalue = value
	tmr.mem.ChipWrite(chipbus.INTIM, tmr.INTIMvalue)
}

// SetInterval sets the timer interval based on timer register name.
func (tmr *Timer) SetInterval(interval cpubus.Register) bool {
	switch interval {
	case cpubus.TIM1T:
		tmr.Divider = TIM1T
	case cpubus.TIM8T:
		tmr.Divider = TIM8T
	case cpubus.TIM64T:
		tmr.Divider = TIM64T
	case cpubus.T1024T:
		tmr.Divider = T1024T
	default:
		return true
	}

	return false
}

// SetTicks sets the number of remaining ticks.
func (tmr *Timer) SetTicks(ticks int) {
	tmr.TicksRemaining = ticks
}
