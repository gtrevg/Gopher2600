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

//go:generate go run generate.go

package main

import (
	"encoding/csv"
	"fmt"
	"go/format"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/jetsetilly/gopher2600/hardware/cpu/instructions"
)

const definitionsCSVFile = "./instructions.csv"
const generatedGoFile = "../table.go"

const leadingBoilerPlate = "// Code generated by hardware/cpu/instructions/generator/generate.go DO NOT EDIT.\n\n" +
	"package instructions\n\n" +
	"// GetDefinitions returns the table of instruction definitions for the 6507\n" +
	"func GetDefinitions() []*Definition {\n" +
	"return []*Definition{"

const trailingBoilerPlate = "}\n}"

func parseCSV() (string, error) {
	// open file
	df, err := os.Open(definitionsCSVFile)
	if err != nil {
		return "", fmt.Errorf("error opening instruction definitions (%s)", err)
	}
	defer df.Close()

	// treat the file as a CSV file
	csvr := csv.NewReader(df)
	csvr.Comment = rune('#')
	csvr.TrimLeadingSpace = true
	csvr.ReuseRecord = true

	// instruction file can have a variable number of fields per definition.
	// instruction effect field is optional (defaulting to READ)
	csvr.FieldsPerRecord = -1

	// create new definitions table
	deftable := make(map[uint8]instructions.Definition)

	line := 0
	for {
		// loop through file until EOF is reached
		line++
		rec, err := csvr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}

		// check for valid record length
		if !(len(rec) == 5 || len(rec) == 6) {
			return "", fmt.Errorf("wrong number of fields in instruction definition (%s) [line %d]", rec, line)
		}

		// trim trailing comment from record
		rec[len(rec)-1] = strings.Split(rec[len(rec)-1], "#")[0]

		// manually trim trailing space from all fields in the record
		for i := 0; i < len(rec); i++ {
			rec[i] = strings.TrimSpace(rec[i])
		}

		newDef := instructions.Definition{}

		// field: parse opcode
		opcode := rec[0]
		if opcode[:2] == "0x" {
			opcode = opcode[2:]
		}
		opcode = strings.ToUpper(opcode)

		// store the decimal number in the new instruction definition
		// -- we'll use this for the hash key too
		n, err := strconv.ParseInt(opcode, 16, 16)
		if err != nil {
			return "", fmt.Errorf("invalid opcode (%#02x) [line %d]", opcode, line)
		}
		newDef.OpCode = uint8(n)

		// field: operator
		newDef.Operator = descToOperator(rec[1])

		// deferring cycles field until later

		// field: addressing mode
		//
		// the addressing mode also defines how many bytes an opcode
		// requires
		am := strings.ToUpper(rec[3])
		switch am {
		default:
			return "", fmt.Errorf("invalid addressing mode for %#02x (%s) [line %d]", newDef.OpCode, rec[3], line)
		case "IMPLIED":
			newDef.AddressingMode = instructions.Implied
			newDef.Bytes = 1
		case "IMMEDIATE":
			newDef.AddressingMode = instructions.Immediate
			newDef.Bytes = 2
		case "RELATIVE":
			newDef.AddressingMode = instructions.Relative
			newDef.Bytes = 2
		case "ABSOLUTE":
			newDef.AddressingMode = instructions.Absolute
			newDef.Bytes = 3
		case "ZEROPAGE":
			newDef.AddressingMode = instructions.ZeroPage
			newDef.Bytes = 2
		case "INDIRECT":
			newDef.AddressingMode = instructions.Indirect
			newDef.Bytes = 3
		case "INDEXED_INDIRECT":
			newDef.AddressingMode = instructions.IndexedIndirect
			newDef.Bytes = 2
		case "INDIRECT_INDEXED":
			newDef.AddressingMode = instructions.IndirectIndexed
			newDef.Bytes = 2
		case "ABSOLUTE_INDEXED_X":
			newDef.AddressingMode = instructions.AbsoluteIndexedX
			newDef.Bytes = 3
		case "ABSOLUTE_INDEXED_Y":
			newDef.AddressingMode = instructions.AbsoluteIndexedY
			newDef.Bytes = 3
		case "ZEROPAGE_INDEXED_X":
			newDef.AddressingMode = instructions.ZeroPageIndexedX
			newDef.Bytes = 2
		case "ZEROPAGE_INDEXED_Y":
			newDef.AddressingMode = instructions.ZeroPageIndexedY
			newDef.Bytes = 2
		}

		// field: page sensitive
		ps := strings.ToUpper(rec[4])
		switch ps {
		default:
			return "", fmt.Errorf("invalid page sensitivity switch for %#02x (%s) [line %d]", newDef.OpCode, rec[4], line)
		case "TRUE":
			newDef.PageSensitive = true
		case "FALSE":
			newDef.PageSensitive = false
		}

		// field: effect category
		if len(rec) == 5 {
			// effect field is optional. if it hasn't been included then
			// default instruction effect defaults to 'Read'
			newDef.Effect = instructions.Read
		} else {
			switch rec[5] {
			default:
				return "", fmt.Errorf("unknown category for %#02x (%s) [line %d]", newDef.OpCode, rec[5], line)
			case "READ":
				newDef.Effect = instructions.Read
			case "WRITE":
				newDef.Effect = instructions.Write
			case "RMW":
				newDef.Effect = instructions.RMW
			case "FLOW":
				newDef.Effect = instructions.Flow
			case "SUB-ROUTINE":
				newDef.Effect = instructions.Subroutine
			case "INTERRUPT":
				newDef.Effect = instructions.Interrupt
			}
		}

		// field: undocumented
		newDef.Undocumented = unicode.IsLower(rune(rec[1][0]))

		// field: cycles
		newDef.Cycles.Value, err = strconv.Atoi(rec[2])
		if err != nil {
			return "", fmt.Errorf("invalid cycle count for %#02x (%s) [line %d]", newDef.OpCode, rec[2], line)
		}
		if newDef.IsBranch() {
			newDef.Cycles.Formatted = fmt.Sprintf("%d/%d", newDef.Cycles.Value, newDef.Cycles.Value+1)
		} else {
			newDef.Cycles.Formatted = fmt.Sprintf("%d", newDef.Cycles.Value)
		}

		// add new definition to deftable, using opcode as the hash key
		if _, ok := deftable[newDef.OpCode]; ok {
			panic(fmt.Sprintf("duplicate opcode: %v", newDef))
		}
		deftable[newDef.OpCode] = newDef
	}

	printSummary(deftable)

	// output the definitions map as an array
	output := ""
	for opcode := 0; opcode < 256; opcode++ {
		def, found := deftable[uint8(opcode)]
		if found {
			output = fmt.Sprintf("%s\n&%#v,", output, def)
		} else {
			output = fmt.Sprintf("%s\nnil,", output)
		}
	}

	return output, nil
}

func printSummary(deftable map[uint8]instructions.Definition) {
	missing := make([]int, 0, 255)

	// walk deftable and note missing instructions
	for i := 0; i <= 255; i++ {
		if _, ok := deftable[uint8(i)]; !ok {
			missing = append(missing, i)
		}
	}

	// if no missing instructions were found then there is nothing more to do
	if len(missing) == 0 {
		fmt.Println("all instruction generated")
		return
	}
	fmt.Printf("cpu instructions generated (%d missing, %02.0f%% defined)\n", len(missing), float32(100*(256-len(missing))/256))

	if len(missing) < 10 {
		fmt.Printf("missing: %x\n", missing)
	}
}

func generate() (rb bool) {
	// parse definitions files
	output, err := parseCSV()
	if err != nil {
		fmt.Printf("error during instruction table generation: %s\n", err)
		return false
	}

	// we'll be putting the contents of deftable into the definition package so
	// we need to remove the expicit references to that package
	output = strings.ReplaceAll(output, "instructions.", "")

	// add boiler-plate to output
	output = fmt.Sprintf("%s%s%s", leadingBoilerPlate, output, trailingBoilerPlate)

	// format code using standard Go formatted
	formattedOutput, err := format.Source([]byte(output))
	if err != nil {
		fmt.Printf("error during instruction table generation: %s\n", err)
		return false
	}
	output = string(formattedOutput)

	// create output file (over-writing) if it already exists
	f, err := os.Create(generatedGoFile)
	if err != nil {
		fmt.Printf("error during instruction table generation: %s\n", err)
		return false
	}
	defer func() {
		err := f.Close()
		if err != nil {
			fmt.Printf("error during file close: %s\n", err)
			rb = false
		}
	}()

	_, err = f.WriteString(output)
	if err != nil {
		fmt.Printf("error during instruction table generation: %s\n", err)
		return false
	}

	return true
}

func main() {
	if !generate() {
		os.Exit(10)
	}
}

func descToOperator(operator string) instructions.Operator {
	switch operator {
	case "nop":
		return instructions.Nop
	case "adc":
		return instructions.Adc
	case "AHX":
		return instructions.AHX
	case "ANC":
		return instructions.ANC
	case "and":
		return instructions.And
	case "ARR":
		return instructions.ARR
	case "asl":
		return instructions.Asl
	case "ASR":
		return instructions.ASR
	case "AXS":
		return instructions.AXS
	case "bcc":
		return instructions.Bcc
	case "bcs":
		return instructions.Bcs
	case "beq":
		return instructions.Beq
	case "bit":
		return instructions.Bit
	case "bmi":
		return instructions.Bmi
	case "bne":
		return instructions.Bne
	case "bpl":
		return instructions.Bpl
	case "brk":
		return instructions.Brk
	case "bvc":
		return instructions.Bvc
	case "bvs":
		return instructions.Bvs
	case "clc":
		return instructions.Clc
	case "cld":
		return instructions.Cld
	case "cli":
		return instructions.Cli
	case "clv":
		return instructions.Clv
	case "cmp":
		return instructions.Cmp
	case "cpx":
		return instructions.Cpx
	case "cpy":
		return instructions.Cpy
	case "DCP":
		return instructions.DCP
	case "dec":
		return instructions.Dec
	case "dex":
		return instructions.Dex
	case "dey":
		return instructions.Dey
	case "eor":
		return instructions.Eor
	case "inc":
		return instructions.Inc
	case "inx":
		return instructions.Inx
	case "iny":
		return instructions.Iny
	case "ISC":
		return instructions.ISC
	case "jmp":
		return instructions.Jmp
	case "jsr":
		return instructions.Jsr
	case "KIL":
		return instructions.KIL
	case "LAS":
		return instructions.LAS
	case "LAX":
		return instructions.LAX
	case "lda":
		return instructions.Lda
	case "ldx":
		return instructions.Ldx
	case "ldy":
		return instructions.Ldy
	case "lsr":
		return instructions.Lsr
	case "NOP":
		return instructions.NOP
	case "ora":
		return instructions.Ora
	case "pha":
		return instructions.Pha
	case "php":
		return instructions.Php
	case "pla":
		return instructions.Pla
	case "plp":
		return instructions.Plp
	case "RLA":
		return instructions.RLA
	case "rol":
		return instructions.Rol
	case "ror":
		return instructions.Ror
	case "RRA":
		return instructions.RRA
	case "rti":
		return instructions.Rti
	case "rts":
		return instructions.Rts
	case "SAX":
		return instructions.SAX
	case "sbc":
		return instructions.Sbc
	case "SBC":
		return instructions.SBC
	case "sec":
		return instructions.Sec
	case "sed":
		return instructions.Sed
	case "sei":
		return instructions.Sei
	case "SHX":
		return instructions.SHX
	case "SHY":
		return instructions.SHY
	case "SLO":
		return instructions.SLO
	case "SRE":
		return instructions.SRE
	case "sta":
		return instructions.Sta
	case "stx":
		return instructions.Stx
	case "sty":
		return instructions.Sty
	case "TAS":
		return instructions.TAS
	case "tax":
		return instructions.Tax
	case "tay":
		return instructions.Tay
	case "tsx":
		return instructions.Tsx
	case "txa":
		return instructions.Txa
	case "txs":
		return instructions.Txs
	case "tya":
		return instructions.Tya
	case "XAA":
		return instructions.XAA
	}

	panic(fmt.Sprintf("unrecognised operator %s", operator))
}
