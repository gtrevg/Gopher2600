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

package memory

import (
	"math/rand"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/hardware/instance"
	"github.com/jetsetilly/gopher2600/hardware/memory/addresses"
	"github.com/jetsetilly/gopher2600/hardware/memory/bus"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge"
	"github.com/jetsetilly/gopher2600/hardware/memory/memorymap"
	"github.com/jetsetilly/gopher2600/hardware/memory/vcs"
)

// Memory is the monolithic representation of the memory in 2600.
type Memory struct {
	bus.DebugBus
	bus.CPUBus

	instance *instance.Instance

	// the four memory areas
	RIOT *vcs.ChipMemory
	TIA  *vcs.ChipMemory
	RAM  *vcs.RAM
	Cart *cartridge.Cartridge

	// the following are only used by the debugging interface. it would be
	// lovely to remove these for non-debugging emulation but there's not much
	// impact on performance so they can stay for now:
	//
	//  . a note of the last (unmapped) memory address to be accessed
	//  . as above but the mapped address
	//  . the value that was written/read from the last address accessed
	//  . whether the last address accessed was written or read
	//
	// Users of this fields shoudl also consider the possibility that the
	// access was a phantom access (PhantomAccess flag in CPU type)
	LastAccessAddress       uint16
	LastAccessAddressMapped uint16
	LastAccessValue         uint8
	LastAccessWrite         bool
}

// NewMemory is the preferred method of initialisation for Memory.
func NewMemory(instance *instance.Instance) *Memory {
	mem := &Memory{
		instance: instance,
		RIOT:     vcs.NewRIOT(instance),
		TIA:      vcs.NewTIA(instance),
		RAM:      vcs.NewRAM(instance),
		Cart:     cartridge.NewCartridge(instance),
	}
	mem.Reset()
	return mem
}

// Snapshot creates a copy of the current memory state.
func (mem *Memory) Snapshot() *Memory {
	n := *mem
	n.RIOT = mem.RIOT.Snapshot()
	n.TIA = mem.TIA.Snapshot()
	n.RAM = mem.RAM.Snapshot()
	n.Cart = mem.Cart.Snapshot()
	return &n
}

// Plumb makes sure everything is ship-shape after a rewind event.
func (mem *Memory) Plumb(fromDifferentEmulation bool) {
	mem.Cart.Plumb(fromDifferentEmulation)
}

// Reset contents of memory.
func (mem *Memory) Reset() {
	mem.RIOT.Reset()
	mem.TIA.Reset()
	mem.RAM.Reset()
	mem.Cart.Reset()
}

// GetArea returns the actual memory of the specified area type.
func (mem *Memory) GetArea(area memorymap.Area) bus.DebugBus {
	switch area {
	case memorymap.TIA:
		return mem.TIA
	case memorymap.RAM:
		return mem.RAM
	case memorymap.RIOT:
		return mem.RIOT
	case memorymap.Cartridge:
		return mem.Cart
	}

	panic("memory areas are not mapped correctly")
}

// read maps an address to the normalised for all memory areas.
func (mem *Memory) read(address uint16, zeroPage bool) (uint8, error) {
	ma, ar := memorymap.MapAddress(address, true)
	area := mem.GetArea(ar)

	var data uint8
	var err error

	if ar == memorymap.Cartridge {
		// some cartridge mappers want to see the unmapped address
		data, err = area.(*cartridge.Cartridge).Read(address)
	} else {
		data, err = area.(bus.CPUBus).Read(ma)
	}

	// we do not return error early because we still want to note the
	// LastAccessAddress, call the cartridge.Listen() function etc. or,
	// for example, the WATCH command will not function as expected
	//
	// we just need to be careful that we do not clobber the err value
	//                                    ----------------------------

	// some memory areas do not change all the bits on the data bus, leaving
	// some bits of the address in the result
	//
	// if the mapped address has an entry in the Mask array then use the most
	// significant byte of the non-mapped address and apply it to the data bits
	// specified in the mask
	//
	// see commentary for DataMasks array for extensive explanation
	if ma < uint16(len(addresses.DataMasks)) {
		if !zeroPage {
			data &= addresses.DataMasks[ma]
			if mem.instance != nil && mem.instance.Prefs.RandomPins.Get().(bool) {
				data |= uint8(rand.Int()) & (addresses.DataMasks[ma] ^ 0xff)
			} else {
				data |= uint8((address>>8)&0xff) & (addresses.DataMasks[ma] ^ 0xff)
			}
		} else {
			data &= addresses.DataMasks[ma]
			if mem.instance != nil && mem.instance.Prefs.RandomPins.Get().(bool) {
				data |= uint8(rand.Int()) & (addresses.DataMasks[ma] ^ 0xff)
			} else {
				data |= uint8(address&0x00ff) & (addresses.DataMasks[ma] ^ 0xff)
			}
		}
	}

	// see the commentary for the Listen() function in the Cartridge interface
	// for an explanation for what is going on here.
	mem.Cart.Listen(address, data)

	// the following is only used by the debugger
	mem.LastAccessAddress = address
	mem.LastAccessAddressMapped = ma
	mem.LastAccessWrite = false
	mem.LastAccessValue = data

	return data, err
}

// Read is an implementation of CPUBus. Address will be normalised and
// processed by the correct memory area.
func (mem *Memory) Read(address uint16) (uint8, error) {
	return mem.read(address, false)
}

// ReadZeroPage is an implementation of CPUBus. Address will be normalised and
// processed by the correct memory areas.
func (mem *Memory) ReadZeroPage(address uint8) (uint8, error) {
	return mem.read(uint16(address), true)
}

// Write is an implementation of CPUBus Address will be normalised and
// processed by the correct memory areas.
func (mem *Memory) Write(address uint16, data uint8) error {
	ma, ar := memorymap.MapAddress(address, false)
	area := mem.GetArea(ar)

	mem.LastAccessAddress = address
	mem.LastAccessAddressMapped = ma
	mem.LastAccessWrite = true
	mem.LastAccessValue = data

	// see the commentary for the Listen() function in the Cartridge interface
	// for an explanation for what is going on here. more to the point, we only
	// need to "listen" if the mapped address is not in Cartridge space
	mem.Cart.Listen(address, data)

	return area.(bus.CPUBus).Write(ma, data)
}

// Peek implements the DebugBus interface.
func (mem *Memory) Peek(address uint16) (uint8, error) {
	ma, ar := memorymap.MapAddress(address, true)
	if area, ok := mem.GetArea(ar).(bus.DebugBus); ok {
		return area.Peek(ma)
	}
	return 0, curated.Errorf(bus.AddressError, address)
}

// Poke implements the DebugBus interface.
func (mem *Memory) Poke(address uint16, data uint8) error {
	ma, ar := memorymap.MapAddress(address, true)
	if area, ok := mem.GetArea(ar).(bus.DebugBus); ok {
		return area.Poke(ma, data)
	}
	return curated.Errorf(bus.AddressError, address)
}
