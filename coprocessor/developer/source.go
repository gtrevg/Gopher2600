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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
	"github.com/jetsetilly/gopher2600/logger"
)

// SrcFile represents a single file of original source code. A file is made up
// of many SrcLine entries.
type SrcFile struct {
	Filename string
	Lines    []SrcLine
}

// SrcLine represents a single line of source in a SrcFile.
type SrcLine struct {
	file       *SrcFile
	LineNumber int // counting from one
	Content    string
	Asm        []SrcLineAsm

	CycleCount float32
}

// SrcLineAsm associates an asm with a block of source (which might be a single line).
type SrcLineAsm struct {
	Asm string
	src *SrcLine
}

// Source files for the currently loaded ROM. It is built through a combination
// of a binary objdump and the original source files.
type Source struct {
	Files           map[string]*SrcFile
	FilesNames      []string
	asm             map[uint32]*SrcLineAsm
	TotalCycleCount float32
}

const objFile = "armcode.obj"
const objFile_older = "custom2.obj"

func findObjDump(romDir string) *os.File {
	// current working directory
	od, err := os.Open(objFile)
	if err == nil {
		return od
	}

	// same directory as binary
	od, err = os.Open(filepath.Join(romDir, objFile))
	if err == nil {
		return od
	}

	// main sub-directory
	od, err = os.Open(filepath.Join(romDir, "main", objFile))
	if err == nil {
		return od
	}

	// main/bin sub-directory
	od, err = os.Open(filepath.Join(romDir, "main", "bin", objFile))
	if err == nil {
		return od
	}

	// custom/bin sub-directory. some older DPC+ sources uses this layout
	od, err = os.Open(filepath.Join(romDir, "custom", "bin", objFile_older))
	if err == nil {
		return od
	}

	// jetsetilly source tree
	od, err = os.Open(filepath.Join(romDir, "arm", "main.obj"))
	if err == nil {
		return od
	}

	return nil
}

func readSourceFile(filename string, pathToROM string) (*SrcFile, error) {
	// remove superfluous path direction
	filename = filepath.Clean(filename)

	fl := SrcFile{
		Filename: filename,
	}

	var err error
	var b []byte

	// try to open file. first as a path relative to the ROM and if that fails,
	// as an absolute path
	b, err = ioutil.ReadFile(filepath.Join(filepath.Dir(pathToROM), filename))
	if err != nil {
		b, err = ioutil.ReadFile(filename)
		if err != nil {
			return nil, err
		}
	}

	// split files into lines
	for i, s := range strings.Split(string(b), "\n") {
		fl.Lines = append(fl.Lines, SrcLine{
			file:       &fl,
			LineNumber: i + 1,
			Content:    s,
		})
	}

	return &fl, nil
}

// newSource loads and parses an obj file. Returns a new instance of ObjDump
// or any errors.
func newSource(pathToROM string) (*Source, error) {
	obj := &Source{
		Files:      make(map[string]*SrcFile),
		FilesNames: make([]string, 0),
		asm:        make(map[uint32]*SrcLineAsm),
	}

	// path to ROM without the filename
	romDir := filepath.Dir(pathToROM)

	// find objdump file and open it
	od := findObjDump(romDir)
	if od == nil {
		return nil, curated.Errorf("objfile: gcc .obj file not available (%s)", objFile)
	}
	defer od.Close()

	// read all data, split into lines
	data, err := io.ReadAll(od)
	if err != nil {
		return nil, curated.Errorf("objfile: processing error: %v", err)
	}
	lines := strings.Split(string(data), "\n")

	// regexes for lines in objdump file

	// lines that refer to a source fil
	fileMatch, err := regexp.Compile("^[[:print:]]+:[[:digit:]]+$")
	if err != nil {
		panic(fmt.Sprintf("objdump: %s", err.Error()))
	}

	// lines that contain the compiled ASM instructions
	asmMatch, err := regexp.Compile("[[:xdigit:]]{3}:.*$")
	if err != nil {
		panic(fmt.Sprintf("objdump: %s", err.Error()))
	}

	// map of ReadFile errors already seen, so we don't print the same error
	// over and over
	fileNotFound := make(map[string]bool)

	var currentLine *SrcLine

	// examine every line of the objdump
	for _, ol := range lines {
		if fileMatch.Match([]byte(ol)) {
			fm := strings.Split(ol, ":")
			if len(fm) != 2 {
				logger.Log("objdump", "malformed filename/linenumber entry")
				continue // for loop
			}

			// chop off path prefix
			prefix := fmt.Sprintf("%s%c", romDir, filepath.Separator)
			if strings.HasPrefix(fm[0], prefix) {
				fm[0] = fm[0][len(prefix):]
			}

			// objdump refers to a file that has not been seen before - read the source file
			if _, ok := obj.Files[fm[0]]; !ok {
				var err error
				obj.Files[fm[0]], err = readSourceFile(fm[0], pathToROM)

				if err != nil {
					delete(obj.Files, fm[0])
					if _, ok := fileNotFound[err.Error()]; !ok {
						fileNotFound[err.Error()] = true
						logger.Log("objdump", err.Error())
					}

					continue // for loop
				}

				// add filename to list of keys
				obj.FilesNames = append(obj.FilesNames, fm[0])
			}

			// parse line number directive and note current line
			ln, err := strconv.ParseUint(fm[1], 10, 32)
			if err != nil {
				logger.Log("objdump", err.Error())
				continue
			}

			// we index lines from zero but lines are counted from one in the objdump
			ln -= 1

			currentLine = &obj.Files[fm[0]].Lines[ln]

		} else if asmMatch.Match([]byte(ol)) {
			addr, err := strconv.ParseUint(strings.TrimSpace(ol[:8]), 16, 32)
			if err == nil {
				if currentLine != nil {
					asmEntry := SrcLineAsm{
						Asm: strings.TrimSpace(ol[9:]),
						src: currentLine,
					}
					obj.asm[uint32(addr)] = &asmEntry
					currentLine.Asm = append(currentLine.Asm, asmEntry)
				}
			}
		}
	}

	// sort list of filename keys
	sort.Strings(obj.FilesNames)

	return obj, nil
}

// findProgramAccess returns the program (function) label for the supplied
// address. Addresses may be in a range.
func (obj *Source) findProgramAccess(address uint32) mapper.CoProcSourceReference {
	asm := obj.asm[address]

	return mapper.CoProcSourceReference{
		Filename:   asm.src.file.Filename,
		LineNumber: asm.src.LineNumber,
		Content:    asm.src.Content,
	}
}

// dump everything to io.Writer.
func (obj *Source) dump(w io.Writer) {
	for fn, f := range obj.Files {
		w.Write([]byte(fmt.Sprintf("%s\n", fn)))
		w.Write([]byte(fmt.Sprintf("-------\n")))

		for _, ln := range f.Lines {
			w.Write([]byte(fmt.Sprintf("%d:\t%s\n", ln.LineNumber, ln.Content)))
			for _, asm := range ln.Asm {
				w.Write([]byte(fmt.Sprintf("%s\n", asm.Asm)))
			}
		}
	}
}

// execute address and increase source line count.
func (obj *Source) execute(address uint32, ct float32) {
	if a, ok := obj.asm[address]; ok {
		a.src.CycleCount += ct
		obj.TotalCycleCount += ct
	}
}
