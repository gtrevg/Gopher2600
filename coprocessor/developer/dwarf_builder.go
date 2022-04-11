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
	"io"
	"sort"

	"github.com/jetsetilly/gopher2600/curated"
)

// associates a compile unit with an individual entry. this is important
// because retreiving the file list for an entry depends very much on the
// compile unit - we need to make sure we're using the correct compile unit.
type buildEntry struct {
	compileUnit *dwarf.Entry
	entry       *dwarf.Entry
}

type build struct {
	dwrf *dwarf.Data

	subprograms        map[dwarf.Offset]buildEntry
	inlinedSubroutines map[dwarf.Offset]buildEntry
	types              map[dwarf.Offset]buildEntry
	variables          map[dwarf.Offset]buildEntry

	// the order in which we encountered the subprograms and inlined
	// subroutines is important
	order []dwarf.Offset
}

func newBuild(dwrf *dwarf.Data) (*build, error) {
	bld := &build{
		dwrf:               dwrf,
		subprograms:        make(map[dwarf.Offset]buildEntry),
		inlinedSubroutines: make(map[dwarf.Offset]buildEntry),
		types:              make(map[dwarf.Offset]buildEntry),
		variables:          make(map[dwarf.Offset]buildEntry),
		order:              make([]dwarf.Offset, 0, 100),
	}

	var compileUnit *dwarf.Entry

	r := bld.dwrf.Reader()
	for {
		entry, err := r.Next()
		if err != nil {
			if err == io.EOF {
				break // for loop
			}
			return nil, err
		}
		if entry == nil {
			break // for loop
		}
		if entry.Offset == 0 {
			continue // for loop
		}

		switch entry.Tag {
		case dwarf.TagCompileUnit:
			compileUnit = entry

		case dwarf.TagInlinedSubroutine:
			if compileUnit == nil {
				return nil, curated.Errorf("found inlined subroutine without compile unit")
			} else {
				bld.inlinedSubroutines[entry.Offset] = buildEntry{
					compileUnit: compileUnit,
					entry:       entry,
				}
				bld.order = append(bld.order, entry.Offset)
			}

		case dwarf.TagSubprogram:
			if compileUnit == nil {
				return nil, curated.Errorf("found inlined subroutine without compile unit")
			} else {
				bld.subprograms[entry.Offset] = buildEntry{
					compileUnit: compileUnit,
					entry:       entry,
				}
				bld.order = append(bld.order, entry.Offset)
			}

		case dwarf.TagBaseType:
			fallthrough
		case dwarf.TagPointerType:
			if compileUnit == nil {
				return nil, curated.Errorf("found inlined subroutine without compile unit")
			} else {
				bld.types[entry.Offset] = buildEntry{
					compileUnit: compileUnit,
					entry:       entry,
				}
				bld.order = append(bld.order, entry.Offset)
			}

		case dwarf.TagVariable:
			if compileUnit == nil {
				return nil, curated.Errorf("found inlined subroutine without compile unit")
			} else {
				bld.variables[entry.Offset] = buildEntry{
					compileUnit: compileUnit,
					entry:       entry,
				}
				bld.order = append(bld.order, entry.Offset)
			}
		}
	}

	return bld, nil
}

type foundFunction struct {
	filename string
	linenum  int64
	name     string
}

func (bld *build) findFunction(addr uint64) (*foundFunction, error) {
	var ret *foundFunction

	resolve := func(b buildEntry) (*foundFunction, error) {
		lr, err := bld.dwrf.LineReader(b.compileUnit)
		if err != nil {
			return nil, err
		}
		files := lr.Files()

		// name of entry
		fld := b.entry.AttrField(dwarf.AttrName)
		if fld == nil {
			return nil, nil
		}
		name := fld.Val.(string)

		// declaration file
		fld = b.entry.AttrField(dwarf.AttrDeclFile)
		if fld == nil {
			return nil, nil
		}
		filenum := fld.Val.(int64)

		// declaration line
		fld = b.entry.AttrField(dwarf.AttrDeclLine)
		if fld == nil {
			return nil, nil
		}
		linenum := fld.Val.(int64)

		return &foundFunction{
			filename: files[filenum].Name,
			linenum:  linenum,
			name:     name,
		}, nil
	}

	for _, off := range bld.order {
		if subp, ok := bld.subprograms[off]; ok {
			entry := subp.entry

			// check address against low/high fields. compare to
			// InlinedSubroutines where address range can be given by either
			// low/high fields OR a Range field. for Subprograms, there is
			// never a Range field.

			var low uint64
			var high uint64

			fld := entry.AttrField(dwarf.AttrLowpc)
			if fld == nil {
				// it is possible for Subprograms to have no address fields.
				// the Subprograms are abstract and will be referred to by
				// either concrete Subprograms or concrete InlinedSubroutines
				continue // for loop
			}
			low = uint64(fld.Val.(uint64))

			fld = entry.AttrField(dwarf.AttrHighpc)
			if fld == nil {
				return nil, curated.Errorf("AttrLowpc without AttrHighpc for InlinedSubroutine: %08x", addr)
			}

			switch fld.Class {
			case dwarf.ClassConstant:
				// dwarf-4
				high = low + uint64(fld.Val.(int64))
			case dwarf.ClassAddress:
				// dwarf-2
				high = uint64(fld.Val.(uint64))
			default:
				return nil, curated.Errorf("AttrLowpc without AttrHighpc for InlinedSubroutine: %08x", addr)
			}

			if addr < low || addr >= high {
				continue // for loop
			}

			fld = entry.AttrField(dwarf.AttrAbstractOrigin)
			if fld != nil {
				abstract, ok := bld.subprograms[fld.Val.(dwarf.Offset)]
				if !ok {
					return nil, curated.Errorf("found inlined subroutine without abstract: %08x", addr)
				}

				r, err := resolve(abstract)
				if err != nil {
					return nil, err
				}
				if r != nil {
					ret = r
				}
			} else {
				r, err := resolve(subp)
				if err != nil {
					return nil, err
				}
				if r != nil {
					ret = r
				}
			}
		} else if inl, ok := bld.inlinedSubroutines[off]; ok {
			entry := inl.entry
			fld := entry.AttrField(dwarf.AttrLowpc)
			if fld != nil {
				var low uint64
				var high uint64

				low = uint64(fld.Val.(uint64))

				// high PC
				fld = entry.AttrField(dwarf.AttrHighpc)
				if fld == nil {
					return nil, curated.Errorf("AttrLowpc without AttrHighpc for InlinedSubroutine: %08x", addr)
				}

				switch fld.Class {
				case dwarf.ClassConstant:
					// dwarf-4
					high = low + uint64(fld.Val.(int64))
				case dwarf.ClassAddress:
					// dwarf-2
					high = uint64(fld.Val.(uint64))
				default:
					return nil, curated.Errorf("AttrLowpc without AttrHighpc for InlinedSubroutine: %08x", addr)
				}

				if addr < low || addr >= high {
					continue // for loop
				}
			} else {
				fld = entry.AttrField(dwarf.AttrRanges)
				if fld == nil {
					continue // for loop
				}

				rngs, err := bld.dwrf.Ranges(entry)
				if err != nil {
					return nil, err
				}

				match := false
				for _, r := range rngs {
					if addr >= r[0] && addr < r[1] {
						match = true
						break
					}
				}
				if !match {
					continue // for loop
				}
			}

			fld = entry.AttrField(dwarf.AttrAbstractOrigin)
			if fld == nil {
				return nil, curated.Errorf("missing AttrAbstractOrigin: %08x", addr)
			}

			abstract, ok := bld.subprograms[fld.Val.(dwarf.Offset)]
			if !ok {
				return nil, curated.Errorf("found inlined subroutine without abstract: %08x", addr)
			}

			r, err := resolve(abstract)
			if err != nil {
				return nil, err
			}
			if r != nil {
				ret = r
			}
		}
	}

	return ret, nil
}

// buildVariables populates all variable structures in the *Source tree
func (bld *build) buildVariables(src *Source) error {
	for _, v := range bld.variables {
		var varb SourceVariable

		fld := v.entry.AttrField(dwarf.AttrName)
		if fld == nil {
			continue // for loop
		}
		varb.Name = fld.Val.(string)

		// variable type
		fld = v.entry.AttrField(dwarf.AttrType)
		if fld == nil {
			continue // for loop
		}

		t, ok := bld.types[dwarf.Offset(fld.Val.(dwarf.Offset))]
		if !ok {
			continue // for loop
		}
		fld = t.entry.AttrField(dwarf.AttrName)
		if fld == nil {
			continue // for loop
		}
		varb.Type = fld.Val.(string)

		// variable location in the source
		fld = v.entry.AttrField(dwarf.AttrDeclFile)
		if fld == nil {
			continue // for loop
		}
		declFile := fld.Val.(int64)

		fld = v.entry.AttrField(dwarf.AttrDeclLine)
		if fld == nil {
			continue // for loop
		}
		declLine := fld.Val.(int64)

		lr, err := bld.dwrf.LineReader(v.compileUnit)
		if err != nil {
			return err
		}
		files := lr.Files()

		file := src.Files[files[declFile].Name]
		varb.DeclLine = file.Lines[declLine]

		// variable address (location)
		fld = v.entry.AttrField(dwarf.AttrLocation)
		if fld == nil {
			continue // for loop
		}

		switch fld.Class {
		case dwarf.ClassLocListPtr:
			continue // for loop
		case dwarf.ClassExprLoc:
			expr := fld.Val.([]uint8)
			switch expr[0] {
			case 0x03: // constant address
				if len(expr) != 5 {
					continue // for loop
				}
				varb.Address = uint64(expr[1])
				varb.Address |= uint64(expr[2]) << 8
				varb.Address |= uint64(expr[3]) << 16
				varb.Address |= uint64(expr[4]) << 24

			default:
				continue // for loop
			}

		default:
			continue // for loop
		}

		// variables declared on lines without a parent function belong in the
		// global variable list of the declaration file
		if varb.DeclLine.Function.Name == UnknownFunction {
			varb.DeclLine.File.Globals[varb.Name] = &varb
			varb.DeclLine.File.GlobalNames = append(varb.DeclLine.File.GlobalNames, varb.Name)
		}
	}

	// sort strings
	for i := range src.Files {
		sort.Strings(src.Files[i].GlobalNames)
	}

	return nil
}
