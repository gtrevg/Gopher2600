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

package dbgmem

import (
	"fmt"
	"strconv"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/disassembly/symbols"
	"github.com/jetsetilly/gopher2600/hardware"
	"github.com/jetsetilly/gopher2600/hardware/memory/bus"
	"github.com/jetsetilly/gopher2600/hardware/memory/memorymap"
)

// DbgMem is a front-end to the real VCS memory. it allows addressing by
// symbol name and uses the AddressInfo type for easier presentation.
type DbgMem struct {
	VCS *hardware.VCS
	Sym *symbols.Symbols
}

// MapAddress allows addressing by symbols in addition to numerically.
func (dbgmem DbgMem) MapAddress(address interface{}, read bool) *AddressInfo {
	ai := &AddressInfo{Read: read}

	var searchTable symbols.SearchTable

	if read {
		searchTable = symbols.SearchRead
	} else {
		searchTable = symbols.SearchWrite
	}

	switch address := address.(type) {
	case uint16:
		ai.Address = address
		res := dbgmem.Sym.SearchByAddress(ai.Address, searchTable)
		if res == nil {
			ai.MappedAddress, ai.Area = memorymap.MapAddress(ai.Address, read)
			res := dbgmem.Sym.SearchByAddress(ai.MappedAddress, searchTable)
			if res != nil {
				ai.AddressLabel = res.Entry.Symbol
			}
		} else {
			ai.MappedAddress, ai.Area = memorymap.MapAddress(ai.Address, read)
			ai.AddressLabel = res.Entry.Symbol
		}
	case string:
		var err error

		res := dbgmem.Sym.SearchBySymbol(address, searchTable)
		if res != nil {
			ai.Address = res.Address
			ai.AddressLabel = res.Entry.Symbol
			ai.MappedAddress, ai.Area = memorymap.MapAddress(ai.Address, read)
		} else {
			// this may be a string representation of a numerical address
			var addr uint64

			addr, err = strconv.ParseUint(address, 0, 16)
			if err != nil {
				return nil
			}

			ai.Address = uint16(addr)
			res := dbgmem.Sym.SearchByAddress(ai.Address, searchTable)
			if res == nil {
				ai.MappedAddress, ai.Area = memorymap.MapAddress(ai.Address, read)
				res := dbgmem.Sym.SearchByAddress(ai.MappedAddress, searchTable)
				if res != nil {
					ai.AddressLabel = res.Entry.Symbol
				}
			} else {
				ai.MappedAddress, ai.Area = memorymap.MapAddress(ai.Address, read)
				ai.AddressLabel = res.Entry.Symbol
			}
		}
	default:
		panic(fmt.Sprintf("unsupported address type (%T)", address))
	}

	return ai
}

// Formatted errors for Peek() and Poke(). These can be used by other packages,
// if required, for consistency.
const (
	PeekError = "cannot peek address (%v)"
	PokeError = "cannot poke address (%v)"
)

// Peek returns the contents of the memory address, without triggering any side
// effects. address can be expressed numerically or symbolically.
func (dbgmem DbgMem) Peek(address interface{}) (*AddressInfo, error) {
	ai := dbgmem.MapAddress(address, true)
	if ai == nil {
		return nil, curated.Errorf(PeekError, address)
	}

	area := dbgmem.VCS.Mem.GetArea(ai.Area)

	var err error
	ai.Data, err = area.Peek(ai.MappedAddress)
	if err != nil {
		if curated.Is(err, bus.AddressError) {
			return nil, curated.Errorf(PeekError, address)
		}
		return nil, err
	}

	ai.Peeked = true

	return ai, nil
}

// Poke writes a value at the specified address, which may be numeric or
// symbolic.
func (dbgmem DbgMem) Poke(address interface{}, data uint8) (*AddressInfo, error) {
	ai := dbgmem.MapAddress(address, false)
	if ai == nil {
		return nil, curated.Errorf(PokeError, address)
	}

	area := dbgmem.VCS.Mem.GetArea(ai.Area)

	err := area.Poke(ai.MappedAddress, data)
	if err != nil {
		if curated.Is(err, bus.AddressError) {
			return nil, curated.Errorf(PokeError, address)
		}
		return nil, err
	}

	ai.Data = data
	ai.Peeked = true

	return ai, err
}
