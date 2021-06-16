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

package controllers

import (
	"strconv"
	"time"

	"github.com/jetsetilly/gopher2600/hardware/memory/bus"
	"github.com/jetsetilly/gopher2600/hardware/riot/ports"
	"github.com/jetsetilly/gopher2600/hardware/riot/ports/plugging"
)

// Auto handles the automatic switching between controller types.
type Auto struct {
	port       plugging.PortID
	bus        ports.PeripheralBus
	controller ports.Peripheral
	monitor    plugging.PlugMonitor

	lastStickVal  ports.Event
	lastStickTime time.Time
	stickCt       int

	lastPaddleValue float32
	lastPaddleTime  time.Time
	paddleTouchCt   int

	// if a keyboard is detected via SWACNT then there is no auto-switching
	//
	// to prevent false positives of the SWACNT being in "non-keyboard" mode
	// and unecessarily switching to a non-keyboard controller and then
	// switching immediately back, we time the duration between unplug
	// attempts. if the duration is long enough (keyboardUnplugDelay) then the
	// unplugging is allowed.
	//
	// a good example of a false positive of this type is the Coke Zero demo.
	//
	// it would be better perhaps if we did this the other way around and
	// introduced a delay before switching to the keyboard controller. but at
	// least one title (Codebreaker) sets the SWACNT once and leaves it at
	// that so there's nothing to work on. one possibility is to set up a timer
	// and switching to the keyboard when it expires, unless it's been
	// interrupted, but I don't fancy the idea of having to service a timer on
	// Update()
	keyboardUnplugAttempt bool
	keyboardUnplugTime    time.Time
	keyboardUnplugDelay   time.Duration
}

// the sensitivity values for switching between controller types.
//
// note that changing these values may well break existing playback scripts. do
// not change unless absolutely necessary.
//
// !!TODO: consider versioning the auto-controller type to help the recorder package.
const (
	autoStickSensitivity  = 6
	autoPaddleSensitivity = 6

	// the amount of time an input device will be "awake" and counting inputs before falling asleep again.
	//
	// in other words, activity must be completed in this time frame for the auto-switch to occur.
	wakeTime = 2e09 // two seconds in nanoseconds
)

// NewAuto is the preferred method of initialisation for the Auto type.
// Satisifies the ports.NewPeripheral interface and can be used as an argument
// to ports.AttachPlayer0() and ports.AttachPlayer1().
func NewAuto(port plugging.PortID, bus ports.PeripheralBus) ports.Peripheral {
	aut := &Auto{
		port: port,
		bus:  bus,
	}

	// a two second delay should be sufficient time to require SWACNT to be in
	// "non-keyboard" mode before allowing the controller type to switch
	aut.keyboardUnplugDelay, _ = time.ParseDuration("1s")

	aut.Reset()
	return aut
}

// Plumb implements the Peripheral interface.
func (aut *Auto) Plumb(bus ports.PeripheralBus) {
	aut.bus = bus
	aut.controller.Plumb(bus)
}

// String implements the ports.Peripheral interface.
func (aut *Auto) String() string {
	return aut.controller.String()
}

// PortID implements the ports.Peripheral interface.
func (aut *Auto) PortID() plugging.PortID {
	return aut.port
}

// Name implements the ports.Peripheral interface.
func (aut *Auto) Name() string {
	return aut.controller.Name()
}

// HandleEvent implements the ports.Peripheral interface.
func (aut *Auto) HandleEvent(event ports.Event, data ports.EventData) error {
	// no autoswitching if keyboard is detected
	if _, ok := aut.controller.(*Keyboard); !ok {
		switch event {
		case ports.Left:
			aut.checkStick(event)
		case ports.Right:
			aut.checkStick(event)
		case ports.Up:
			aut.checkStick(event)
		case ports.Down:
			aut.checkStick(event)
		case ports.Fire:
			// no check for fire events
		case ports.PaddleSet:
			aut.checkPaddle(data)
		case ports.KeyboardDown:
			// no check on keyboard down
		case ports.KeyboardUp:
			// no check on keyboard up
		}
	}

	err := aut.controller.HandleEvent(event, data)

	return err
}

// Update implements the ports.Peripheral interface.
func (aut *Auto) Update(data bus.ChipData) bool {
	switch data.Name {
	case "SWACNT":
		if data.Value&0xf0 == 0xf0 {
			// attach keyboard IF NOT attached already
			if _, ok := aut.controller.(*Keyboard); !ok {
				aut.controller = NewKeyboard(aut.port, aut.bus)
				aut.plug()
			} else if aut.keyboardUnplugAttempt {
				// reset keyboardUnplugAttempt if an unplug attempt has been made
				aut.keyboardUnplugAttempt = false
			}
		} else if data.Value&0xf0 == 0x00 {
			if _, ok := aut.controller.(*Keyboard); ok {
				if aut.keyboardUnplugAttempt {
					if time.Since(aut.keyboardUnplugTime) > aut.keyboardUnplugDelay {
						aut.controller = NewStick(aut.port, aut.bus)
						aut.plug()
					}
				} else {
					aut.keyboardUnplugAttempt = true
					aut.keyboardUnplugTime = time.Now()
				}
			}
		}
	}

	return aut.controller.Update(data)
}

// Step implements the ports.Peripheral interface.
func (aut *Auto) Step() {
	aut.controller.Step()
}

// Reset implements the ports.Peripheral interface.
func (aut *Auto) Reset() {
	aut.controller = NewStick(aut.port, aut.bus)
	aut.resetStickDetection()
	aut.resetPaddleDetection()
}

func (aut *Auto) checkStick(event ports.Event) {
	aut.resetPaddleDetection()

	if _, ok := aut.controller.(*Stick); !ok {
		// stick must be "awake" before counting begins
		if time.Since(aut.lastStickTime) < wakeTime {
			// detect stick being waggled. stick detection works a little
			// differently to paddle and keyboard detection. instead of the stick
			// data we record the stick event.
			if event != aut.lastStickVal {
				aut.stickCt++
				if aut.stickCt >= autoStickSensitivity {
					aut.controller = NewStick(aut.port, aut.bus)
					aut.plug()
				}
			}

			aut.lastStickVal = event
		} else {
			// reset paddle detection date before recording time for next paddle event
			aut.resetStickDetection()
			aut.lastStickTime = time.Now()
		}
	}
}

func (aut *Auto) checkPaddle(data ports.EventData) {
	aut.resetStickDetection()

	if _, ok := aut.controller.(*Paddle); !ok {
		// paddle must be "awake" before counting begins
		if time.Since(aut.lastPaddleTime) < wakeTime {
			var pv float32

			// handle possible underlying EventData types
			switch d := data.(type) {
			case ports.EventDataPlayback:
				f, err := strconv.ParseFloat(string(d), 32)
				if err != nil {
					return // ignore error
				}
				pv = float32(f)
			case float32:
				pv = d
			default:
				return
			}

			// detect mouse moving into extreme left/right positions
			if (pv < 0.1 && aut.lastPaddleValue > 0.1) || (pv > 0.9 && aut.lastPaddleValue < 0.9) {
				aut.paddleTouchCt++

				// if mouse has touched extremeties a set number of times then
				// switch to paddle control. for example if the sensitivity value is
				// three:
				//
				//	centre -> right -> left -> switch
				if aut.paddleTouchCt >= autoPaddleSensitivity {
					aut.controller = NewPaddle(aut.port, aut.bus)
					aut.plug()
				}

				aut.lastPaddleValue = pv
			}
		} else {
			// reset paddle detection date before recording time for next paddle event
			aut.resetPaddleDetection()
			aut.lastPaddleTime = time.Now()
		}
	}
}

// resetPaddleDetection called when non-paddle input is detected.
func (aut *Auto) resetPaddleDetection() {
	aut.lastPaddleValue = 0.5
	aut.lastPaddleTime = time.Time{}
	aut.paddleTouchCt = 0
}

// resetPaddleDetection called when non-stick input is detected.
func (aut *Auto) resetStickDetection() {
	aut.lastStickVal = ports.Centre
	aut.lastStickTime = time.Time{}
	aut.stickCt = 0
}

// plug is called by chceckStick(), checkPaddle() and checkKeyboard() and handles the
// plug monitor.
func (aut *Auto) plug() {
	// notify any peripheral monitors
	if aut.monitor != nil {
		aut.monitor.Plugged(aut.port, aut.controller.Name())
	}

	// attach any monitors to newly plugged controllers
	if a, ok := aut.controller.(plugging.Monitorable); ok {
		a.AttachPlugMonitor(aut.monitor)
	}
}

// AttachPlugMonitor implements the plugging.Monitorable interface.
func (aut *Auto) AttachPlugMonitor(m plugging.PlugMonitor) {
	aut.monitor = m

	if a, ok := aut.controller.(plugging.Monitorable); ok {
		a.AttachPlugMonitor(m)
	}
}
