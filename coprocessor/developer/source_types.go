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

import "fmt"

// SourceFile is a single source file indentified by the DWARF data.
type SourceFile struct {
	Filename      string
	ShortFilename string
	Lines         []*SourceLine

	// the source file has at least one global variable if HasGlobals is true
	HasGlobals bool
}

// SourceDisasm is a single disassembled intruction from the ELF binary. Not to
// be confused with the coprocessor.disassembly package. SourceDisasm instances
// are intended to be used by static disasemblers.
type SourceDisasm struct {
	Addr        uint32
	Opcode      uint16
	Instruction string

	Line *SourceLine
}

func (d *SourceDisasm) String() string {
	return fmt.Sprintf("%#08x %04x %s", d.Addr, d.Opcode, d.Instruction)
}

// SourceLine is a single line of source in a source file, identified by the
// DWARF data and loaded from the actual source file.
type SourceLine struct {
	// the actual file/line of the SourceLine
	File       *SourceFile
	LineNumber int

	// the function the line of source can be found within
	Function *SourceFunction

	// plain string of line
	PlainContent string

	// line divided into parts
	Fragments []SourceLineFragment

	// the generated assembly for this line. will be empty if line is a comment or otherwise unsused
	Disassembly []*SourceDisasm

	// some source lines will interleave their coproc instructions
	// (disassembly) with other source lines
	Interleaved bool

	// whether this source line has been responsible for an illegal access of memory
	IllegalAccess bool

	// statistics for the line
	Stats StatsGroup

	// which 2600 kernel has this line executed in
	Kernel KernelVCS
}

func (ln *SourceLine) String() string {
	return fmt.Sprintf("%s:%d", ln.File.Filename, ln.LineNumber)
}

// IsStub returns true if thre is no DWARF data for this function.
func (ln *SourceLine) IsStub() bool {
	// field File will be nil for stub line entries
	return ln.File == nil

	// other properties of a stub line entry will be an empty PlainContent,
	// Fragments and Disassembly fields
}

// SourceFunction is a single function identified by the DWARF data or by the
// ELF symbol table in the case of no DWARF information being available for the
// function.
//
// Use NoSource() to detect if function has no DWARF information.
type SourceFunction struct {
	Name string

	// first source line for each instance of the function. note that the first
	// line of a function may not have any code directly associated with it.
	// the Disassembly and Stats fields therefore should not be relied upon.
	DeclLine *SourceLine

	// stats for the function
	FlatStats       StatsGroup
	CumulativeStats StatsGroup

	// which 2600 kernel has this function executed in
	Kernel KernelVCS

	// whether the call stack involving this function is likely inaccurate
	OptimisedCallStack bool
}

// IsStub returns true if thre is no DWARF data for this function.
func (fn *SourceFunction) IsStub() bool {
	// field DeclLine will be nil for stub function entries
	return fn.DeclLine == nil
}

// SourceType is a single type identified by the DWARF data. Composite types
// are differentiated by the existance of member fields.
type SourceType struct {
	Name string

	// is a constant type
	Constant bool

	// the base type of pointer types. will be nil if type is not a pointer type
	PointerType *SourceType

	// size of values of this type (in bytes)
	Size int

	// empty if type is not a composite type. see SourceVariable.IsComposite()
	// function
	Members []*SourceVariable

	// number of elements in the type. if count is more than zero then this
	// type is an array. see SourceVariable.IsArry() function
	ElementCount int

	// the base type of all the elements in the type
	ElementType *SourceType
}

// IsComposite returns true if SourceType is a composite type.
func (typ *SourceType) IsComposite() bool {
	return len(typ.Members) > 0
}

// IsArray returns true if SourceType is an array type.
func (typ *SourceType) IsArray() bool {
	return typ.ElementType != nil && typ.ElementCount > 0
}

// IsPointer returns true if SourceType is a pointer type.
func (typ *SourceType) IsPointer() bool {
	return typ.PointerType != nil
}

// Hex returns a format string to represent a value as a correctly padded
// hexadecinal number.
func (typ *SourceType) Hex() string {
	// other fields in the SourceType instance depend on the byte size
	switch typ.Size {
	case 1:
		return "%02x"
	case 2:
		return "%04x"
	case 4:
		return "%08x"
	}
	return "%x"
}

// Bin returns a format string to represent a value as a correctly padded
// binary number.
func (typ *SourceType) Bin() string {
	// other fields in the SourceType instance depend on the byte size
	switch typ.Size {
	case 1:
		return "%08b"
	case 2:
		return "%016b"
	case 4:
		return "%032b"
	}
	return "%b"
}

// Mask returns the mask value of the correct size for the type.
func (typ *SourceType) Mask() uint32 {
	switch typ.Size {
	case 1:
		return 0xff
	case 2:
		return 0xffff
	case 4:
		return 0xffffffff
	}
	return 0
}

// SourceVariable is a single variable identified by the DWARF data.
type SourceVariable struct {
	Name string

	// variable type (int, char, etc.)
	Type *SourceType

	// first source line for each instance of the function
	DeclLine *SourceLine

	// address in memory of the variable. if the variable is a member of
	// another type then the Address is an offset from the address of the
	// parent variable
	Address         uint64
	addressIsOffset bool
}

// IsComposite returns true if SourceVariable is of a composite type.
func (varb *SourceVariable) IsComposite() bool {
	return varb.Type.IsComposite()
}

// IsArray returns true if SourceVariables of an array type.
func (varb *SourceVariable) IsArray() bool {
	return varb.Type.IsArray()
}

// IsPointer returns true if SourceVariables is a pointer type.
func (varb *SourceVariable) IsPointer() bool {
	return varb.Type.IsPointer()
}

// AddressIsOffset returns true if SourceVariable is member of another type
func (varb *SourceVariable) AddressIsOffset() bool {
	return varb.addressIsOffset
}

func (varb *SourceVariable) String() string {
	return fmt.Sprintf("%s %s => %#08x", varb.Type.Name, varb.Name, varb.Address)
}