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

package mapper

import (
	"debug/dwarf"
	"fmt"
)

// CartCoProcBus is implemented by cartridge mappers that have a coprocessor that
// functions independently from the VCS.
type CartCoProcBus interface {
	CoProcID() string
	SetDisassembler(CartCoProcDisassembler)
	SetDeveloper(CartCoProcDeveloper)

	// returns any DWARF data for the cartridge. not all cartridges that
	// implement the CartCoProcBus interface will be able to meaningfully
	// return any data but none-the-less would benefit from DWARF debugging
	// information. in those instances, the DWARF data must be retreived
	// elsewhere
	DWARF() *dwarf.Data

	// returns the offset of the named ELF section and whether the named
	// section exists. not all cartridges that implement this interface will be
	// able to meaningfully answer this function call
	ELFSection(string) (uint32, bool)
}

// CartCoProcExecution is implemented by cartridge mappers that have a
// coprocessor. These coprocessors require careful monitoring so that they
// interact with the main emulation correctly.
//
// For example, these coprocessors can halt mid-operation due to a breakpoint
// and the debugging loop need to understand when this happened.
//
// TODO: this interface should be folded into the CartCoProcBus interface.
// there's no need to differentiate the two
type CartCoProcExecution interface {
	CoProcIsActive() bool
	BreakpointHasTriggered() bool
	ResumeAfterBreakpoint() error
	BreakpointsDisable(bool)
}

// CartCoProcDisasmEntry represents a single decoded instruction by the coprocessor.
type CartCoProcDisasmEntry interface {
	Key() string
	CSV() string
}

// CartCoProcDisasmSummary represents a summary of a coprocessor execution.
type CartCoProcDisasmSummary interface {
	String() string
}

// CartCoProcDisassembler defines the functions that must be defined for a
// disassembler to be attached to a coprocessor.
type CartCoProcDisassembler interface {
	// Start is called at the beginning of coprocessor program execution.
	Start()

	// Step called after every instruction in the coprocessor program.
	Step(CartCoProcDisasmEntry)

	// End is called when coprocessor program has finished.
	End(CartCoProcDisasmSummary)
}

// CartCoProcDeveloper is used by the coprocessor to provide functions
// available to developers when the source code is available.
type CartCoProcDeveloper interface {
	// addr accessed illegally by instruction at pc address. should return the
	// empty string if no meaningful information could be found
	IllegalAccess(event string, pc uint32, addr uint32) string

	// address is the same as the null pointer, indicating the address access
	// is likely to be a null pointer dereference
	NullAccess(event string, pc uint32, addr uint32) string

	// stack has collided with variable memtop
	StackCollision(pc uint32, sp uint32) string

	// returns the highest address utilised by program memory. the coprocessor
	// uses this value to detect stack collisions. should return zero if no
	// variables information is available
	VariableMemtop() uint32

	// checks if address has a breakpoint assigned to it
	CheckBreakpoint(addr uint32) bool

	// execution of the coprocessor has started
	ExecutionStart()

	// accumulate cycles for executed addresses. profile is a map of
	// instruction addresses and the number of cycles they have consumed
	ExecutionProfile(profile map[uint32]float32)

	// execution of the coprocessor has ended
	ExecutionEnd()
}

// CartCoProcDisassemblerStdout is a minimial implementation of the CartCoProcDisassembler
// interface. It outputs entries to stdout immediately upon request.
type CartCoProcDisassemblerStdout struct {
}

// Start implements the CartCoProcDisassembler interface.
func (c *CartCoProcDisassemblerStdout) Start() {
}

// Instruction implements the CartCoProcDisassembler interface.
func (c *CartCoProcDisassemblerStdout) Step(e CartCoProcDisasmEntry) {
	fmt.Println(e)
}

// End implements the CartCoProcDisassembler interface.
func (c *CartCoProcDisassemblerStdout) End(s CartCoProcDisasmSummary) {
	fmt.Println(s)
}
