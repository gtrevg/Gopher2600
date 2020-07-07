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

package supercharger

import (
	"fmt"
	"strings"

	"github.com/jetsetilly/gopher2600/errors"
	"github.com/jetsetilly/gopher2600/hardware/memory/bus"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/banks"
	"github.com/jetsetilly/gopher2600/hardware/memory/memorymap"
)

// supercharger has 6k of RAM in total
const numRamBanks = 4
const bankSize = 2048

// Supercharger represents a supercharger cartridge
type Supercharger struct {
	mappingID   string
	description string

	tape      Tape
	registers Registers

	bankSize int
	bios     []uint8
	ram      [3][]uint8
}

// NewSupercharger is the preferred method of initialisation for the
// Supercharger type
func NewSupercharger(data []byte) (*Supercharger, error) {
	cart := &Supercharger{
		mappingID:   "AR",
		description: "supercharger",
		bankSize:    2048,
	}

	// set up tape
	cart.tape.cart = cart
	cart.tape.data = data

	// allocate ram
	for i := range cart.ram {
		cart.ram[i] = make([]uint8, bankSize)
	}

	var err error

	// load bios and activate
	cart.bios, err = loadBIOS()
	if err != nil {
		return nil, err
	}

	cart.Initialise()

	return cart, nil
}

func (cart Supercharger) String() string {
	s := strings.Builder{}
	s.WriteString(fmt.Sprintf("%s [%s] ", cart.mappingID, cart.description))
	s.WriteString(cart.registers.BankString())
	return s.String()
}

// ID implements the cartMapper interface
func (cart Supercharger) ID() string {
	return cart.mappingID
}

// Initialise implements the cartMapper interface
func (cart *Supercharger) Initialise() {
	cart.registers.WriteDelay = 0
	cart.registers.BankingMode = 0
	cart.registers.ROMpower = true
	cart.registers.RAMwrite = true
}

// Read implements the cartMapper interface
func (cart *Supercharger) Read(fullAddr uint16, passive bool) (uint8, error) {
	addr := fullAddr & memorymap.CartridgeBits

	if !passive {
		switch addr {
		case 0x0ff8:
			// control register
			cart.registers.setConfigByte(cart.registers.Value)
			cart.registers.Delay = 0
			return 0, nil

		case 0x0ff9:
			// call load() whenever address is touched, although do not allow
			// it if RAMwrite is false
			if !cart.registers.RAMwrite {
				return 0, nil
			}
			return 0, cart.tape.load()
		}

		// note address to be used as the next value in the control register
		if fullAddr&0xf000 == 0xf000 && fullAddr <= 0xf0ff {
			if cart.registers.Delay == 0 {
				cart.registers.Value = uint8(fullAddr & 0x00ff)
				cart.registers.Delay = 6
			}
		}
	}

	// what bank to read. bank zero refers to the BIOS. bank 1 to 3 refer to
	// one of the RAM banks
	bank := cart.GetBank(addr).Number

	bios := false
	switch bank {
	case 0:
		bios = true
	default:
		// RAM banks are indexed from 0 to 2
		bank--
	}

	if bios {
		if cart.registers.ROMpower {
			return cart.bios[addr&0x7ff], nil
		}
		return 0, errors.New(errors.SuperchargerError, "ROM is powered off")
	}

	if !passive && cart.registers.Delay == 1 {
		if bios {
			return 0, errors.New(errors.SuperchargerError, "trying to write to ROM")
		}
		if cart.registers.RAMwrite {
			cart.ram[bank][addr&0x7ff] = cart.registers.Value
			cart.registers.LastWriteAddress = fullAddr
			cart.registers.LastWriteValue = cart.registers.Value
		}
	}

	return cart.ram[bank][addr&0x7ff], nil
}

// Write implements the cartMapper interface
func (cart *Supercharger) Write(addr uint16, data uint8, passive bool, poke bool) error {
	return nil
}

// NumBanks implements the cartMapper interface
func (cart Supercharger) NumBanks() int {
	return numRamBanks
}

// GetBank implements the cartMapper interface
func (cart Supercharger) GetBank(addr uint16) banks.Details {
	switch cart.registers.BankingMode {
	case 0:
		if addr >= 0x0800 {
			return banks.Details{Number: 0, IsRAM: false, Segment: 0}
		}
		return banks.Details{Number: 3, IsRAM: cart.registers.RAMwrite, Segment: 1}

	case 1:
		if addr >= 0x0800 {
			return banks.Details{Number: 0, IsRAM: false, Segment: 0}
		}
		return banks.Details{Number: 1, IsRAM: cart.registers.RAMwrite, Segment: 1}

	case 2:
		if addr >= 0x0800 {
			return banks.Details{Number: 1, IsRAM: cart.registers.RAMwrite, Segment: 0}
		}
		return banks.Details{Number: 3, IsRAM: cart.registers.RAMwrite, Segment: 1}

	case 3:
		if addr >= 0x0800 {
			return banks.Details{Number: 3, IsRAM: cart.registers.RAMwrite, Segment: 0}
		}
		return banks.Details{Number: 1, IsRAM: cart.registers.RAMwrite, Segment: 1}

	case 4:
		if addr >= 0x0800 {
			return banks.Details{Number: 0, IsRAM: false, Segment: 0}
		}
		return banks.Details{Number: 3, IsRAM: cart.registers.RAMwrite, Segment: 1}

	case 5:
		if addr >= 0x0800 {
			return banks.Details{Number: 0, IsRAM: false, Segment: 0}
		}
		return banks.Details{Number: 2, IsRAM: cart.registers.RAMwrite, Segment: 1}

	case 6:
		if addr >= 0x0800 {
			return banks.Details{Number: 2, IsRAM: cart.registers.RAMwrite, Segment: 0}
		}
		return banks.Details{Number: 3, IsRAM: cart.registers.RAMwrite, Segment: 1}

	case 7:
		if addr >= 0x0800 {
			return banks.Details{Number: 3, IsRAM: cart.registers.RAMwrite, Segment: 0}
		}
		return banks.Details{Number: 2, IsRAM: cart.registers.RAMwrite, Segment: 1}
	}
	panic("unknown banking method")
}

// Patch implements the cartMapper interface
func (cart *Supercharger) Patch(_ int, _ uint8) error {
	return nil
}

// Listen implements the cartMapper interface
func (cart *Supercharger) Listen(addr uint16, _ uint8) {
	cart.registers.transitionCount(addr)
}

// Step implements the cartMapper interface
func (cart *Supercharger) Step() {
}

// IterateBank implemnts the disassemble interface
func (cart Supercharger) IterateBanks(prev *banks.Content) *banks.Content {
	b := prev.Number + 1
	if b == 0 {
		return &banks.Content{Number: b,
			Data: cart.bios,
			Origins: []uint16{
				memorymap.OriginCart,
				memorymap.OriginCart + uint16(cart.bankSize),
			},
		}
	} else if b >= 1 && b <= 3 {
		return &banks.Content{Number: b,
			Data: cart.ram[b-1],
			Origins: []uint16{
				memorymap.OriginCart,
				memorymap.OriginCart + uint16(cart.bankSize),
			},
		}
	}
	return nil
}

// GetRAM implements the bus.CartRAMBus interface
func (cart Supercharger) GetRAM() []bus.CartRAM {
	r := make([]bus.CartRAM, len(cart.ram))

	for i := 0; i < len(cart.ram); i++ {
		mapped := false
		origin := uint16(0x1000)

		// in the documentation and for presentation purporses, RAM banks are
		// counted from 1. when deciding if a bank is mapped or not, we'll use
		// this value rather than the i index; being consistent with the
		// documentation is clearer
		bank := i + 1

		switch cart.registers.BankingMode {
		case 0:
			mapped = bank == 3

		case 1:
			mapped = bank == 1

		case 2:
			mapped = bank == 1
			if mapped {
				origin = 0x1800
			} else {
				mapped = bank == 3
			}

		case 3:
			mapped = bank == 3
			if mapped {
				origin = 0x1800
			} else {
				mapped = bank == 1
			}

		case 4:
			mapped = bank == 3

		case 5:
			mapped = bank == 2

		case 6:
			mapped = bank == 2
			if mapped {
				origin = 0x1800
			} else {
				mapped = bank == 3
			}

		case 7:
			mapped = bank == 3
			if mapped {
				origin = 0x1800
			} else {
				mapped = bank == 2
			}
		}

		r[i] = bus.CartRAM{
			Label:  fmt.Sprintf("2048k [%d]", bank),
			Origin: origin,
			Data:   make([]uint8, len(cart.ram[i])),
			Mapped: mapped,
		}
		copy(r[i].Data, cart.ram[i])
	}

	return r
}

// PutRAM implements the bus.CartRAMBus interface
func (cart *Supercharger) PutRAM(bank int, idx int, data uint8) {
	if bank < len(cart.ram) {
		cart.ram[bank][idx] = data
		return
	}
}
