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

package developer

import (
	"debug/elf"
	"encoding/binary"
	"fmt"

	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
)

type loclistSection struct {
	ByteOrder binary.ByteOrder
	data      []uint8
}

func newLoclistSection(ef *elf.File) (*loclistSection, error) {
	loc := &loclistSection{
		ByteOrder: ef.ByteOrder,
	}

	var err error

	loc.data, err = relocateELFSection(ef, ".debug_loc")
	if err != nil {
		return nil, err
	}

	return loc, nil
}

type loclistContext interface {
	coproc() mapper.CartCoProc
	framebase() (uint64, error)
}

// the location type is used in two ways. (1) as a way of encoding the
// current address/value of a variables; and (2) as a interim value on the
// stack
//
// with regard to point (2) the valueOk field is used to control how the value
// should be interpreted. if the field is true then the value should be
// considered a value that can potentially be used as a result and presented to
// the user of the debugger. however, if it is false then it is an interim
// value and should be considered to be an address
//
// similarly, if addressOk is false then the address field should be considered
// unusable
//
// the operator field indicates the most recent DWARF operator that led to the
// value. when viewed in the context of the stack field in the loclist type, it
// can be used to help describe the process by which the value was derived.
type location struct {
	address   uint64
	addressOk bool
	value     uint32
	valueOk   bool

	// the operator that created this value
	operator string
}

func (l *location) String() string {
	return fmt.Sprintf("%s %08x", l.operator, l.value)
}

type dwarfOperator func(*loclist) (location, error)

type loclist struct {
	ctx        loclistContext
	list       []dwarfOperator
	stack      []location
	derivation []location
}

func newLoclistJustContext(ctx loclistContext) *loclist {
	return &loclist{
		ctx: ctx,
	}
}

type commitLoclist func(start, end uint64, loc *loclist)

func newLoclistFromSingleValue(ctx loclistContext, addr uint64) (*loclist, error) {
	loc := &loclist{
		ctx: ctx,
	}
	op := func(loc *loclist) (location, error) {
		return location{
			value:   uint32(addr),
			valueOk: true,
		}, nil
	}
	loc.list = append(loc.list, op)
	return loc, nil
}

func newLoclistFromSingleOperator(ctx loclistContext, expr []uint8) (*loclist, error) {
	loc := &loclist{
		ctx: ctx,
	}
	op, n := decodeDWARFoperation(expr, 0)
	if n == 0 {
		return nil, fmt.Errorf("unknown expression operator %02x", expr[0])
	}
	loc.list = append(loc.list, op)
	return loc, nil
}

func newLoclist(ctx loclistContext, debug_loc *loclistSection, debug_frame *frameSection,
	ptr int64, compilationUnitAddress uint64,
	commit commitLoclist) error {

	if debug_loc == nil {
		return fmt.Errorf("no location list information")
	}

	// "Location lists, which are used to describe objects that have a limited lifetime or change
	// their location during their lifetime. Location lists are more completely described below."
	// page 26 of "DWARF4 Standard"
	//
	// "Location lists are used in place of location expressions whenever the object whose location is
	// being described can change location during its lifetime. Location lists are contained in a separate
	// object file section called .debug_loc . A location list is indicated by a location attribute whose
	// value is an offset from the beginning of the .debug_loc section to the first byte of the list for the
	// object in question"
	// page 30 of "DWARF4 Standard"
	//
	// "loclistptr: This is an offset into the .debug_loc section (DW_FORM_sec_offset). It consists
	// of an offset from the beginning of the .debug_loc section to the first byte of the data making up
	// the location list for the compilation unit. It is relocatable in a relocatable object file, and
	// relocated in an executable or shared object. In the 32-bit DWARF format, this offset is a 4-
	// byte unsigned value; in the 64-bit DWARF format, it is an 8-byte unsigned value (see
	// Section 7.4)"
	// page 148 of "DWARF4 Standard"

	// "The applicable base address of a location list entry is determined by the closest preceding base
	// address selection entry (see below) in the same location list. If there is no such selection entry,
	// then the applicable base address defaults to the base address of the compilation unit (see
	// Section 3.1.1)"
	//
	// "A base address selection entry affects only the list in which it is contained"
	// page 31 of "DWARF4 Standard"
	baseAddress := compilationUnitAddress

	// start and end address. this will be updated at the end of every for loop iteration
	startAddress := uint64(debug_loc.ByteOrder.Uint32(debug_loc.data[ptr:]))
	ptr += 4
	endAddress := uint64(debug_loc.ByteOrder.Uint32(debug_loc.data[ptr:]))
	ptr += 4

	// "The end of any given location list is marked by an end of list entry, which consists of a 0 for the
	// beginning address offset and a 0 for the ending address offset. A location list containing only an
	// end of list entry describes an object that exists in the source code but not in the executable
	// program". page 31 of "DWARF4 Standard"
	for !(startAddress == 0x0 && endAddress == 0x0) {
		loc := &loclist{
			ctx: ctx,
		}

		// "A base address selection entry consists of:
		// 1. The value of the largest representable address offset (for example, 0xffffffff when the size of
		// an address is 32 bits).
		// 2. An address, which defines the appropriate base address for use in interpreting the beginning
		// and ending address offsets of subsequent entries of the location list"
		// page 31 of "DWARF4 Standard"
		if startAddress == 0xffffffff {
			baseAddress = endAddress
		} else {
			// reduce end address by one. this is because the value we've read "marks the
			// first address past the end of the address range over which the location is
			// valid" (page 30 of "DWARF4 Standard")
			endAddress -= 1

			// length of expression
			length := int(debug_loc.ByteOrder.Uint16(debug_loc.data[ptr:]))
			ptr += 2

			// loop through stack operations
			for length > 0 {
				r, n := decodeDWARFoperation(debug_loc.data[ptr:], 0)
				if n == 0 {
					return fmt.Errorf("unknown expression operator %02x", debug_loc.data[ptr])
				}

				// add resolver to variable
				loc.addOperator(r)

				// reduce length value
				length -= n

				// advance debug_loc pointer by length value
				ptr += int64(n)
			}

			// "A location list entry (but not a base address selection or end of list entry) whose beginning
			// and ending addresses are equal has no effect because the size of the range covered by such
			// an entry is zero". page 31 of "DWARF4 Standard"
			//
			// "The ending address must be greater than or equal to the beginning address"
			// page 30 of "DWARF4 Standard"
			if startAddress < endAddress {
				commit(startAddress+baseAddress, endAddress+baseAddress, loc)
			}
		}

		// read next address range
		startAddress = uint64(debug_loc.ByteOrder.Uint32(debug_loc.data[ptr:]))
		ptr += 4
		endAddress = uint64(debug_loc.ByteOrder.Uint32(debug_loc.data[ptr:]))
		ptr += 4
	}

	return nil
}

func (loc *loclist) addOperator(r dwarfOperator) {
	loc.list = append(loc.list, r)
}

func (loc *loclist) resolve() (location, error) {
	if loc.ctx == nil {
		return location{}, fmt.Errorf("no context")
	}

	loc.stack = loc.stack[:0]
	loc.derivation = loc.derivation[:0]

	for i := range loc.list {
		r, err := loc.list[i](loc)
		if err != nil {
			return location{}, err
		}

		loc.stack = append(loc.stack, r)
		loc.derivation = append(loc.derivation, r)
	}

	if len(loc.stack) == 0 {
		return location{}, fmt.Errorf("stack is empty")
	}

	// if top of stack does not have a valid value then we treat it as an
	// address and dereference it. put the changed location back on the stack
	// and on the derivation list
	//
	// we tend to see this when DW_OP_fbreg is the only instruction in the
	// loclist and also more commonly with DW_OP_addr in context of global
	// variables
	r := loc.stack[len(loc.stack)-1]
	if !r.valueOk {
		r.address = uint64(r.value)
		r.addressOk = true
		r.value, r.valueOk = loc.ctx.coproc().CoProcRead32bit(r.value)
		loc.stack[len(loc.stack)-1] = r
		loc.derivation[len(loc.stack)-1] = r
	}

	return r, nil
}

// lastResolved implements the resolver interface
func (loc *loclist) lastResolved() location {
	if len(loc.stack) == 0 {
		return location{}
	}
	return loc.stack[len(loc.stack)-1]
}

func (loc *loclist) pop() (location, bool) {
	l := len(loc.stack)
	if l == 0 {
		return location{}, false
	}
	r := loc.stack[l-1]
	loc.stack = loc.stack[:l-1]
	return r, true
}
