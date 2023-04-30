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
	"debug/dwarf"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"io"
	"io/ioutil"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/arm"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
	"github.com/jetsetilly/gopher2600/logger"
)

// compile units are made up of many children. for convenience/speed we keep
// track of the children as an index rather than a tree.
type compileUnit struct {
	unit     *dwarf.Entry
	children map[dwarf.Offset]*dwarf.Entry
	address  uint64
}

// coproc shim makes sure that loclist and framebase resolution is always using
// the current cartridge coprocessor instance. the instance can change after a
// rewind event
type coprocShim struct {
	cart CartCoProcDeveloper
}

// CoProcRegister implements the loclistCoproc and frameCoproc interfaces
func (shim coprocShim) CoProcRegister(n int) (uint32, bool) {
	coproc := shim.cart.GetCoProc()
	if coproc == nil {
		return 0, false
	}
	return coproc.CoProcRegister(n)
}

// CoProcRegister implements the loclistCoproc interface
func (shim coprocShim) CoProcRead32bit(addr uint32) (uint32, bool) {
	coproc := shim.cart.GetCoProc()
	if coproc == nil {
		return 0, false
	}
	return coproc.CoProcRead32bit(addr)
}

// Source is created from available DWARF data that has been found in relation
// to and ELF file that looks to be related to the specified ROM.
//
// It is possible for the arrays/map fields to be empty
type Source struct {
	// shim to the cartridge coprocessor
	coprocShim coprocShim

	// ELF sections that help DWARF locate local variables in memory
	debugLoc   *loclistSection
	debugFrame *frameSection

	// source is compiled with optimisation
	Optimised bool

	// every compile unit in the dwarf data
	compileUnits []*compileUnit

	// instructions in the source code
	Instructions map[uint64]*SourceInstruction

	// all the files in all the compile units
	Files     map[string]*SourceFile
	Filenames []string

	// as above but indexed by the file's short filename, which is sometimes
	// more useful than the full name
	//
	// short filenames also only include files that are in the same path as the
	// ROM file
	FilesByShortname map[string]*SourceFile
	ShortFilenames   []string

	// functions found in the compile units
	Functions     map[string]*SourceFunction
	FunctionNames []string

	// best guess at what the "main" function is in the program. very often
	// this function will be called "main" and will be easy to discern but
	// sometimes it is named something else and we must figure out as best we
	// can which function it is
	//
	// if no function can be found at all, MainFunction will be a stub entry
	MainFunction *SourceFunction

	// special purpose line used to collate instructions that are outside the
	// loaded ROM and are very likely instructions handled by the "driver". the
	// actual driver function is in the Functions map as normal, under the name
	// given in "const driverFunction"
	driverSourceLine *SourceLine

	// sorted list of every function in all compile unit
	SortedFunctions SortedFunctions

	// types used in the source
	types map[dwarf.Offset]*SourceType

	// all global variables in all compile units
	globals          map[string]*SourceVariable
	GlobalsByAddress map[uint64]*SourceVariable
	SortedGlobals    SortedVariables

	// all local variables in all compile units
	locals       []*SourceVariableLocal
	SortedLocals SortedVariablesLocal

	// the highest address of any variable (not just global variables, any
	// variable)
	HighAddress uint64

	// lines of source code found in the compile units. this is a sparse
	// coverage of the total address space
	LinesByAddress map[uint64]*SourceLine

	// sorted list of every source line in all compile units
	SortedLines SortedLines

	// every non-blank line of source code in all compile units
	AllLines AllSourceLines

	// sorted lines filtered by function name
	FunctionFilters []*FunctionFilter

	// statistics for the entire program
	Stats StatsGroup

	// flag to indicate whether the execution profile has changed since it was cleared
	//
	// cheap and easy way to prevent sorting too often - rather than sort after
	// every call to execute(), we can use this flag to sort only when we need
	// to in the GUI.
	//
	// probably not scalable but sufficient for our needs of a single GUI
	// running and using the statistics for only one reason
	ExecutionProfileChanged bool

	// list of breakpoints on ARM program
	Breakpoints map[uint32]bool

	// keeps track of the previous breakpoint check. see checkBreakPointByAddr()
	prevBreakpointCheck *SourceLine

	// call stack of running program
	CallStack CallStack
}

// NewSource is the preferred method of initialisation for the Source type.
//
// If no ELF file or valid DWARF data can be found in relation to the ROM file
// the function will return nil with an error.
//
// Once the ELF and DWARF file has been identified then Source will always be
// non-nil but with the understanding that the fields may be empty.
func NewSource(romFile string, cart CartCoProcDeveloper, elfFile string) (*Source, error) {
	src := &Source{
		coprocShim: coprocShim{
			cart: cart,
		},
		Instructions:     make(map[uint64]*SourceInstruction),
		Files:            make(map[string]*SourceFile),
		Filenames:        make([]string, 0, 10),
		FilesByShortname: make(map[string]*SourceFile),
		ShortFilenames:   make([]string, 0, 10),
		Functions:        make(map[string]*SourceFunction),
		FunctionNames:    make([]string, 0, 10),
		types:            make(map[dwarf.Offset]*SourceType),
		globals:          make(map[string]*SourceVariable),
		GlobalsByAddress: make(map[uint64]*SourceVariable),
		SortedGlobals: SortedVariables{
			Variables: make([]*SourceVariable, 0, 100),
		},
		SortedFunctions: SortedFunctions{
			Functions: make([]*SourceFunction, 0, 100),
		},
		LinesByAddress: make(map[uint64]*SourceLine),
		SortedLines: SortedLines{
			Lines: make([]*SourceLine, 0, 100),
		},
		ExecutionProfileChanged: true,
		Breakpoints:             make(map[uint32]bool),
	}

	var err error

	// the path component of the romFile
	pathToROM := filepath.Dir(romFile)

	// readSourceFile() will shorten the filepath of a source file using the
	// pathToROM string. however, symbolic links can confuse this so we expand
	// all symbolic links in readSourceFile() and need to do the same with the
	// pathToROM value
	var pathToROM_nosymlinks string
	pathToROM_nosymlinks, err = filepath.EvalSymlinks(pathToROM)
	if err != nil {
		pathToROM_nosymlinks = pathToROM
	}

	// open ELF file
	var ef *elf.File
	var fromCartridge bool
	if elfFile != "" {
		ef, err = elf.Open(elfFile)
		if err != nil {
			return nil, fmt.Errorf("dwarf: %w", err)
		}

	} else {
		ef, fromCartridge = findELF(romFile)
		if ef == nil {
			return nil, fmt.Errorf("dwarf: compiled ELF file not found")
		}
	}
	defer ef.Close()

	// whether ELF file is relocatable or not
	relocatable := ef.Type&elf.ET_REL == elf.ET_REL

	// sanity checks on ELF data only if we've loaded the file ourselves and
	// it's not from the cartridge.
	if !fromCartridge {
		if ef.FileHeader.Machine != elf.EM_ARM {
			return nil, fmt.Errorf("dwarf: elf file is not ARM")
		}
		if ef.FileHeader.Version != elf.EV_CURRENT {
			return nil, fmt.Errorf("dwarf: elf file is of unknown version")
		}

		// big endian byte order is probably fine but we've not tested it
		if ef.FileHeader.ByteOrder != binary.LittleEndian {
			return nil, fmt.Errorf("dwarf: elf file is not little-endian")
		}

		// we do not permit relocatable ELF files unless it's been supplied by
		// the cartridge. it's not clear what a relocatable ELF file would mean
		// in this context so we just disallow it
		if relocatable {
			return nil, fmt.Errorf("dwarf: elf file is relocatable. not permitted for non-ELF cartridges")
		}
	}

	// keeping things simple. only 32bit ELF files supported. 64bit files are
	// probably fine but we've not tested them
	if ef.Class != elf.ELFCLASS32 {
		return nil, fmt.Errorf("dwarf: only 32bit ELF files are supported")
	}

	// no need to continue if ELF file does not have any DWARF data
	dwrf, err := ef.DWARF()
	if err != nil {
		return nil, fmt.Errorf("dwarf: no DWARF data in ELF file")
	}

	// origin address of the ELF .text section
	var executableOrigin uint64

	// cartridge coprocessor
	coproc := cart.GetCoProc()
	if coproc == nil {
		return nil, fmt.Errorf("dwarf: cartridge has no coprocessor to work with")
	}

	// acquire origin addresses and debugging sections according to whether the
	// cartridge is relocatable or not
	if relocatable {
		c, ok := coproc.(mapper.CartCoProcRelocatable)
		if !ok {
			return nil, fmt.Errorf("dwarf: ELF file is reloctable but the cartridge mapper does not support that")
		}
		if _, o, ok := c.ELFSection(".text"); ok {
			executableOrigin = uint64(o)
		} else {
			return nil, fmt.Errorf("dwarf: no .text section in ELF file")
		}

		// always create debugFrame and debugLoc sections even when the
		// cartridge doesn't have the corresponding sections. in the case of
		// the loclist section this is definitely needed because even without
		// .debug_loc data we use the loclistSection to help decode single
		// address descriptions (which will definitely be present)

		data, _, _ := c.ELFSection(".debug_frame")
		src.debugFrame, err = newFrameSection(data, ef.ByteOrder, src.coprocShim)
		if err != nil {
			logger.Logf("dwarf", err.Error())
		}

		data, _, _ = c.ELFSection(".debug_loc")
		src.debugLoc, err = newLoclistSection(data, ef.ByteOrder, src.coprocShim)
		if err != nil {
			logger.Logf("dwarf", err.Error())
		}
	} else {
		if c, ok := coproc.(mapper.CartCoProcNonRelocatable); ok {
			executableOrigin = uint64(c.ExecutableOrigin())
		}

		// create frame section from the raw ELF section
		src.debugFrame, err = newFrameSectionFromFile(ef, src.coprocShim)
		if err != nil {
			logger.Logf("dwarf", err.Error())
		}

		// create loclist section from the raw ELF section
		src.debugLoc, err = newLoclistSectionFromFile(ef, src.coprocShim)
		if err != nil {
			logger.Logf("dwarf", err.Error())
		}
	}

	// disassemble every word in the ELF file using the cartridge coprocessor interface
	//
	// we could traverse of the progs array of the file here but some ELF files
	// that we want to support do not have any program headers. we get the same
	// effect by traversing the Sections array and ignoring any section that
	// does not have the EXECINSTR flag
	for _, sec := range ef.Sections {
		if sec.Flags&elf.SHF_EXECINSTR != elf.SHF_EXECINSTR {
			continue
		}

		// section data
		var data []byte
		var ptr int

		data, err = sec.Data()
		if err != nil {
			return nil, fmt.Errorf("dwarf: %w", err)
		}

		// 32bit instruction handling
		var is32Bit bool
		var addr32Bit uint64

		// address of instruction
		addr := sec.Addr + executableOrigin

		for ptr < len(data) {
			opcode := ef.ByteOrder.Uint16(data[ptr:])
			ptr += 2

			if is32Bit {
				is32Bit = false
				src.Instructions[addr32Bit].opcode <<= 16
				src.Instructions[addr32Bit].opcode |= uint32(opcode)
			} else {
				disasm := arm.Disassemble(opcode)

				// size of opcode in instruction
				size := disasm.Size()
				if size != 2 && size != 4 {
					return nil, fmt.Errorf("dwarf: opcode size must be 2 or 4")
				}

				// if size is 4 then the instruction is 32bit, meaning that the
				// next 16bit instruction is treated a little differently
				if size == 4 {
					is32Bit = true
					addr32Bit = addr
				}

				// create the instruction
				src.Instructions[addr] = &SourceInstruction{
					Addr:   uint32(addr),
					opcode: uint32(opcode),
					size:   size,
					Disasm: disasm,
				}
			}

			addr += 2
		}
	}

	bld, err := newBuild(dwrf, src.debugLoc, src.debugFrame)
	if err != nil {
		return nil, fmt.Errorf("dwarf: %w", err)
	}

	// compile units are made up of many files. the files and filenames are in
	// the fields below
	r := dwrf.Reader()

	// loop through file and collate compile units
	for {
		e, err := r.Next()
		if err != nil {
			if err == io.EOF {
				break // for loop
			}
			return nil, fmt.Errorf("dwarf: %w", err)
		}
		if e == nil {
			break // for loop
		}
		if e.Offset == 0 {
			continue // for loop
		}

		switch e.Tag {
		case dwarf.TagCompileUnit:
			unit := &compileUnit{
				unit:     e,
				children: make(map[dwarf.Offset]*dwarf.Entry),
				address:  executableOrigin,
			}

			fld := e.AttrField(dwarf.AttrLowpc)
			if fld != nil {
				unit.address = executableOrigin + uint64(fld.Val.(uint64))
			}

			// assuming DWARF never has duplicate compile unit entries
			src.compileUnits = append(src.compileUnits, unit)

			r, err := dwrf.LineReader(e)
			if err != nil {
				return nil, fmt.Errorf("dwarf: %w", err)
			}

			// loop through files in the compilation unit. entry 0 is always nil
			for _, f := range r.Files()[1:] {
				if _, ok := src.Files[f.Name]; !ok {
					sf, err := readSourceFile(f.Name, pathToROM_nosymlinks, &src.AllLines)
					if err != nil {
						logger.Logf("dwarf", "%v", err)
					} else {
						src.Files[sf.Filename] = sf
						src.Filenames = append(src.Filenames, sf.Filename)

						if strings.HasPrefix(sf.Filename, pathToROM) {
							src.FilesByShortname[sf.ShortFilename] = sf
							src.ShortFilenames = append(src.ShortFilenames, sf.ShortFilename)
						}
					}
				}
			}

			// check optimisation directive
			fld = e.AttrField(dwarf.AttrProducer)
			if fld != nil {
				producer := fld.Val.(string)
				if strings.HasPrefix(producer, "GNU") {
					idx := strings.Index(producer, " -O")
					if idx > -1 {
						src.Optimised = true
					}
				}
			}

		default:
			if len(src.compileUnits) == 0 {
				return nil, fmt.Errorf("dwarf: bad data: no compile unit tag")
			}
			src.compileUnits[len(src.compileUnits)-1].children[e.Offset] = e
		}
	}

	// if no list of "short" filenames has been created then we default using
	// regular filenames
	if len(src.ShortFilenames) == 0 {
		// if there are no regular filenames either then there's nothing
		// meaningful we can do
		if len(src.Filenames) == 0 {
			return nil, fmt.Errorf("dwarf: no source files loaded")
		}

		// copy filenames to the shortfilename field
		src.ShortFilenames = append(src.ShortFilenames, src.Filenames...)
		for k, v := range src.Files {
			src.FilesByShortname[k] = v
		}

		// we also need to change the filename references in the files themselves
		for _, f := range src.Files {
			f.ShortFilename = f.Filename
		}
	}

	// log optimisation message as appropriate
	if src.Optimised {
		logger.Logf("dwarf", "source compiled with optimisation")
	}

	// build functions from DWARF data
	err = bld.buildFunctions(src, executableOrigin)
	if err != nil {
		return nil, fmt.Errorf("dwarf: %w", err)
	}

	// complete function list with stubs for functions where we don't have any
	// DWARF data (but do have symbol data)
	addFunctionStubs(src, ef)

	// sanity check of functions list
	if len(src.Functions) != len(src.FunctionNames) {
		return nil, fmt.Errorf("dwarf: unmatched function definitions")
	}

	// read source lines
	err = allocateInstructionsToSourceLines(src, dwrf, executableOrigin)
	if err != nil {
		return nil, fmt.Errorf("dwarf: %w", err)
	}

	// assign functions to every source line
	allocateFunctionsToSourceLines(src)

	// assemble sorted functions list
	for _, fn := range src.Functions {
		src.SortedFunctions.Functions = append(src.SortedFunctions.Functions, fn)
	}

	// assemble sorted source lines
	//
	// we must make sure that we don't duplicate a source line entry: src.Lines
	// is indexed by address. however, more than one address may point to a
	// single SourceLine
	//
	// to prevent adding a SourceLine more than once we keep an "observed" map
	// indexed by (and this is important) the pointer address of the SourceLine
	// and not the execution address
	observed := make(map[*SourceLine]bool)
	for _, ln := range src.LinesByAddress {
		if _, ok := observed[ln]; !ok {
			observed[ln] = true
			src.SortedLines.Lines = append(src.SortedLines.Lines, ln)
		}
	}

	// build types
	err = bld.buildTypes(src)
	if err != nil {
		return nil, fmt.Errorf("dwarf: %w", err)
	}

	// build variables
	if c, ok := coproc.(mapper.CartCoProcRelocatable); ok {
		err = bld.buildVariables(src, ef, c, executableOrigin)
	} else {
		err = bld.buildVariables(src, ef, nil, executableOrigin)
	}
	if err != nil {
		return nil, fmt.Errorf("dwarf: %w", err)
	}

	// add children to global and local variables
	addVariableChildren(src)

	// sort list of filenames and functions
	sort.Strings(src.Filenames)
	sort.Strings(src.ShortFilenames)

	// sort sorted lines
	src.SortedLines.SortByLineAndFunction(false)

	// sorted functions
	src.SortedFunctions.SortByFunction(false)
	sort.Strings(src.FunctionNames)

	// sorted variables
	sort.Sort(src.SortedGlobals)
	sort.Sort(src.SortedLocals)

	// update all variables
	src.UpdateGlobalVariables()

	// determine highest address occupied by the program
	findHighAddress(src)

	// find entry function to the program
	findEntryFunction(src)

	// log summary
	logger.Logf("dwarf", "identified %d functions in %d compile units", len(src.Functions), len(src.compileUnits))
	logger.Logf("dwarf", "%d global variables", len(src.globals))
	logger.Logf("dwarf", "%d local variable (loclists)", len(src.locals))
	logger.Logf("dwarf", "high address (%08x)", src.HighAddress)

	return src, nil
}

func allocateInstructionsToSourceLines(src *Source, dwrf *dwarf.Data, executableOrigin uint64) error {
	// find reference for every meaningful source line and link to instruction
	for _, e := range src.compileUnits {
		// the source line we're working on
		var ln *SourceLine

		// start of address range
		startAddr := executableOrigin

		// read every line in the compile unit
		r, err := dwrf.LineReader(e.unit)
		if err != nil {
			return err
		}

		var le dwarf.LineEntry
		for {
			err := r.Next(&le)
			if err != nil {
				if err == io.EOF {
					break // for loop
				}
				logger.Logf("dwarf", "%v", err)
				ln = nil
			}

			// make sure we have loaded the file previously
			if src.Files[le.File.Name] == nil {
				logger.Logf("dwarf", "file not available for linereader: %s", le.File.Name)
				continue
			}

			// make sure the number of lines in the file is sufficient for the line entry
			if le.Line-1 > src.Files[le.File.Name].Content.Len() {
				return fmt.Errorf("current source is unrelated to ELF/DWARF data (number of lines)")
			}

			// adjust address by executable origin
			endAddr := le.Address + executableOrigin

			// add breakpoint and instruction information to the source line
			if ln != nil && endAddr-startAddr > 0 {
				// whether line can have a breakpoint on it
				ln.breakable = ln.breakable || le.IsStmt

				// add address to list of break addresses
				ln.breakAddress = append(ln.breakAddress, startAddr)

				// add instruction to source line and add source line to linesByAddress
				for addr := startAddr; addr < endAddr; addr++ {
					// look for address in list of source instructions
					if ins, ok := src.Instructions[addr]; ok {
						// add instruction to the list for the source line
						ln.Instruction = append(ln.Instruction, ins)

						// link source line to instruction
						ins.Line = ln

						// add source line to list of lines by address
						src.LinesByAddress[addr] = ln

						// advance address value by opcode size. reduce value by
						// one because the loop increment advances by one
						// already (which will always apply even if there is no
						// instruction for the address)
						addr += uint64(ins.size) - 1
					}
				}
			}

			// note line entry. once we know the end address we can assign a
			// function to it etc.
			ln = src.Files[le.File.Name].Content.Lines[le.Line-1]
			startAddr = endAddr
		}
	}

	return nil
}

// add children to global and local variables
func addVariableChildren(src *Source) {
	for _, g := range src.globals {
		g.addVariableChildren(src.debugLoc)
	}

	for _, l := range src.locals {
		l.addVariableChildren(src.debugLoc)
	}
}

// assign source lines to a function
func allocateFunctionsToSourceLines(src *Source) {
	// this is a simple, maybe naive way of assigning lines to a function. the
	// alternative is to look up the function by seaching the ranges in all the
	// identified functions. however, this does not work well when functions
	// have been inlined. maybe I'm just misunderstanding something in how
	// ranges work
	//
	// it depends on functions being built with the DeclLine pointing to itself

	// NOTE: this might not make sense for languages other than C. the
	// assumption here is that global variables appear before any other
	// function and that all lines after that are inside a function. for blank
	// lines between functions this doesn't matter but any other variable
	// declarations (outside of a function) will be wrongly allocated

	stub := &SourceFunction{
		Name: stubIndicator,
	}

	for _, sf := range src.Files {
		fn := stub
		for _, ln := range sf.Content.Lines {
			if ln.Function.Name == stubIndicator {
				ln.Function = fn
			} else {
				fn = ln.Function
			}
		}
	}
}

// find entry function to the program
func findEntryFunction(src *Source) {
	// use function called "main" if it's present. we could add to this list
	// other likely names but this would depend on convention, which doesn't
	// exist yet (eg. elf_main)
	if fn, ok := src.Functions["main"]; ok {
		src.MainFunction = fn
		return
	}

	// assume the function of the first line in the source is the entry
	// function
	for _, ln := range src.SortedLines.Lines {
		if len(ln.Instruction) > 0 {
			src.MainFunction = ln.Function
			break
		}
	}
	if src.MainFunction != nil {
		return
	}

	// if no function can be found for some reason then a stub entry is created
	src.MainFunction = createStubLine(nil).Function
}

// determine highest address occupied by the program
func findHighAddress(src *Source) {
	src.HighAddress = 0

	for _, varb := range src.globals {
		a := varb.resolve().address + uint64(varb.Type.Size)
		if a > src.HighAddress {
			src.HighAddress = a
		}
	}

	for _, f := range src.Functions {
		for _, r := range f.Range {
			if r.End > src.HighAddress {
				src.HighAddress = r.End
			}
		}
	}
}

// add function stubs for functions without DWARF data. we do this *after*
// we've looked for functions in the DWARF data (via the line reader) because
// it appears that not every function will necessarily have a symbol and it's
// easier to handle the adding of stubs *after* the the line reader. it does
// mean though that we need to check that a function has not already been added
func addFunctionStubs(src *Source, ef *elf.File) error {
	// all the symbols in the ELF file
	syms, err := ef.Symbols()
	if err != nil {
		return err
	}

	type fn struct {
		name string
		rng  SourceRange
	}

	var symbolTableFunctions []fn

	// the functions from the symbol table
	for _, s := range syms {
		typ := s.Info & 0x0f
		if typ == 0x02 {
			// align address
			// TODO: this is a bit of ARM specific knowledge that should be removed
			a := uint64(s.Value & 0xfffffffe)
			symbolTableFunctions = append(symbolTableFunctions, fn{
				name: s.Name,
				rng: SourceRange{
					Start: a,
					End:   a + uint64(s.Size) - 1,
				},
			})
		}
	}

	for _, fn := range symbolTableFunctions {
		if _, ok := src.Functions[fn.name]; !ok {
			// chop off suffix from symbol table name if there is one. not sure
			// about this but it neatens things up for the cases I've seen so
			// far
			fn.name = strings.Split(fn.name, ".")[0]

			stubFn := &SourceFunction{
				Name: fn.name,
			}
			stubFn.Range = append(stubFn.Range, fn.rng)
			stubFn.DeclLine = createStubLine(stubFn)

			// add stub function to list of functions but not if the function
			// covers an area that has already been seen
			addFunction := true

			// process all addresses in range, skipping any addresses that we
			// already know about from the DWARF data
			for a := fn.rng.Start; a <= fn.rng.End; a++ {
				if _, ok := src.LinesByAddress[a]; !ok {
					src.LinesByAddress[a] = createStubLine(stubFn)
				} else {
					addFunction = false
					break
				}
			}

			if addFunction {
				if _, ok := src.Functions[stubFn.Name]; !ok {
					src.Functions[stubFn.Name] = stubFn
					src.FunctionNames = append(src.FunctionNames, stubFn.Name)
				}
			}
		}
	}

	// add driver function
	driverFn := &SourceFunction{
		Name: DriverFunctionName,
	}
	src.Functions[DriverFunctionName] = driverFn
	src.FunctionNames = append(src.FunctionNames, DriverFunctionName)
	src.driverSourceLine = createStubLine(driverFn)

	return nil
}

func (src *Source) newFrame() {
	// calling newFrame() on stats in a specific order. first the program, then
	// the functions and then the source lines.

	src.Stats.Overall.newFrame(nil, nil)
	src.Stats.VBLANK.newFrame(nil, nil)
	src.Stats.Screen.newFrame(nil, nil)
	src.Stats.Overscan.newFrame(nil, nil)
	src.Stats.ROMSetup.newFrame(nil, nil)

	for _, fn := range src.Functions {
		fn.FlatStats.Overall.newFrame(&src.Stats.Overall, nil)
		fn.FlatStats.VBLANK.newFrame(&src.Stats.VBLANK, nil)
		fn.FlatStats.Screen.newFrame(&src.Stats.Screen, nil)
		fn.FlatStats.Overscan.newFrame(&src.Stats.Overscan, nil)
		fn.FlatStats.ROMSetup.newFrame(&src.Stats.ROMSetup, nil)

		fn.CumulativeStats.Overall.newFrame(&src.Stats.Overall, nil)
		fn.CumulativeStats.VBLANK.newFrame(&src.Stats.VBLANK, nil)
		fn.CumulativeStats.Screen.newFrame(&src.Stats.Screen, nil)
		fn.CumulativeStats.Overscan.newFrame(&src.Stats.Overscan, nil)
		fn.CumulativeStats.ROMSetup.newFrame(&src.Stats.ROMSetup, nil)
	}

	// traverse the SortedLines list and update the FrameCyles values
	//
	// we prefer this over traversing the Lines list because we may hit a
	// SourceLine more than once. SortedLines contains unique entries.
	for _, ln := range src.SortedLines.Lines {
		ln.Stats.Overall.newFrame(&src.Stats.Overall, &ln.Function.FlatStats.Overall)
		ln.Stats.VBLANK.newFrame(&src.Stats.VBLANK, &ln.Function.FlatStats.VBLANK)
		ln.Stats.Screen.newFrame(&src.Stats.Screen, &ln.Function.FlatStats.Screen)
		ln.Stats.Overscan.newFrame(&src.Stats.Overscan, &ln.Function.FlatStats.Overscan)
		ln.Stats.ROMSetup.newFrame(&src.Stats.ROMSetup, &ln.Function.FlatStats.ROMSetup)
	}
}

func readSourceFile(filename string, pathToROM_nosymlinks string, all *AllSourceLines) (*SourceFile, error) {
	var err error

	fl := SourceFile{
		Filename: filename,
	}

	// read file data
	b, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	// split files into lines and parse into fragments
	var fp fragmentParser
	for i, s := range strings.Split(string(b), "\n") {
		ln := &SourceLine{
			File:       &fl,
			LineNumber: i + 1, // counting from one
			Function: &SourceFunction{
				Name: stubIndicator,
			},
			PlainContent: s,
		}
		fl.Content.Lines = append(fl.Content.Lines, ln)
		fp.parseLine(ln)

		// update max line width
		if len(s) > fl.Content.MaxLineWidth {
			fl.Content.MaxLineWidth = len(s)
		}

		if len(strings.TrimSpace(s)) > 0 {
			all.lines = append(all.lines, ln)
		}
	}

	// evaluate symbolic links for the source filename. pathToROM_nosymlinks has
	// already been processed so the comparison later should work in all
	// instance
	var filename_nosymlinks string
	filename_nosymlinks, err = filepath.EvalSymlinks(filename)
	if err != nil {
		filename_nosymlinks = filename
	}

	if strings.HasPrefix(filename_nosymlinks, pathToROM_nosymlinks) {
		fl.ShortFilename = filename_nosymlinks[len(pathToROM_nosymlinks)+1:]
	} else {
		fl.ShortFilename = filename_nosymlinks
	}

	return &fl, nil
}

func findELF(romFile string) (*elf.File, bool) {
	// try the ROM file itself. it might be an ELF file
	ef, err := elf.Open(romFile)
	if err == nil {
		return ef, true
	}

	// the file is not an ELF file so the remainder of the function will work
	// with the path component of the ROM file only
	pathToROM := filepath.Dir(romFile)

	const (
		elfFile            = "armcode.elf"
		elfFile_older      = "custom2.elf"
		elfFile_jetsetilly = "main.elf"
	)

	// current working directory
	ef, err = elf.Open(elfFile)
	if err == nil {
		return ef, false
	}

	// same directory as binary
	ef, err = elf.Open(filepath.Join(pathToROM, elfFile))
	if err == nil {
		return ef, false
	}

	// main sub-directory
	ef, err = elf.Open(filepath.Join(pathToROM, "main", elfFile))
	if err == nil {
		return ef, false
	}

	// main/bin sub-directory
	ef, err = elf.Open(filepath.Join(pathToROM, "main", "bin", elfFile))
	if err == nil {
		return ef, false
	}

	// custom/bin sub-directory. some older DPC+ sources uses this layout
	ef, err = elf.Open(filepath.Join(pathToROM, "custom", "bin", elfFile_older))
	if err == nil {
		return ef, false
	}

	// jetsetilly source tree
	ef, err = elf.Open(filepath.Join(pathToROM, "arm", elfFile_jetsetilly))
	if err == nil {
		return ef, false
	}

	return nil, false
}

// ResetStatistics resets all performance statistics.
func (src *Source) ResetStatistics() {
	for i := range src.Functions {
		src.Functions[i].Kernel = KernelAny
		src.Functions[i].FlatStats.Overall.reset()
		src.Functions[i].FlatStats.VBLANK.reset()
		src.Functions[i].FlatStats.Screen.reset()
		src.Functions[i].FlatStats.Overscan.reset()
		src.Functions[i].CumulativeStats.ROMSetup.reset()
		src.Functions[i].CumulativeStats.Overall.reset()
		src.Functions[i].CumulativeStats.VBLANK.reset()
		src.Functions[i].CumulativeStats.Screen.reset()
		src.Functions[i].CumulativeStats.Overscan.reset()
		src.Functions[i].CumulativeStats.ROMSetup.reset()
		src.Functions[i].OptimisedCallStack = false
	}
	for i := range src.LinesByAddress {
		src.LinesByAddress[i].Kernel = KernelAny
		src.LinesByAddress[i].Stats.Overall.reset()
		src.LinesByAddress[i].Stats.VBLANK.reset()
		src.LinesByAddress[i].Stats.Screen.reset()
		src.LinesByAddress[i].Stats.Overscan.reset()
		src.LinesByAddress[i].Stats.ROMSetup.reset()
	}
	src.Stats.Overall.reset()
	src.Stats.VBLANK.reset()
	src.Stats.Screen.reset()
	src.Stats.Overscan.reset()
	src.Stats.ROMSetup.reset()
}

// FindSourceLine returns line entry for the address. Returns nil if the
// address has no source line.
func (src *Source) FindSourceLine(addr uint32) *SourceLine {
	return src.LinesByAddress[uint64(addr)]
}

// UpdateGlobalVariables using the current state of the emulated coprocessor.
// Local variables are updated when coprocessor yields (see OnYield() function)
func (src *Source) UpdateGlobalVariables() {
	var touch func(varb *SourceVariable)
	touch = func(varb *SourceVariable) {
		varb.Update()
		for i := 0; i < varb.NumChildren(); i++ {
			touch(varb.Child(i))
		}
	}
	for _, varb := range src.SortedGlobals.Variables {
		touch(varb)
	}
}

// FramebaseCurrent returns the current framebase value
func (src *Source) FramebaseCurrent() (uint64, error) {
	return src.debugFrame.framebase()
}

// BorrowSource will lock the source code structure for the durction of the
// supplied function, which will be executed with the source code structure as
// an argument.
//
// May return nil.
func (dev *Developer) BorrowSource(f func(*Source)) {
	dev.sourceLock.Lock()
	defer dev.sourceLock.Unlock()
	f(dev.source)
}
