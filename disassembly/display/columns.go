package display

import "fmt"

// Widths of DisasmInstuction entry fields.
type Widths struct {
	Location int
	Bytecode int
	Address  int
	Mnemonic int
	Operand  int
	Cycles   int
	Notes    int
}

// Fmt strings for Instruction fields. For use with fmt.Printf() and fmt.Sprintf()
type Fmt struct {
	Location string
	Bytecode string
	Address  string
	Mnemonic string
	Operand  string
	Cycles   string
	Notes    string
}

// Columns information for groups of Instructions
type Columns struct {
	Widths Widths
	Fmt    Fmt
}

// Update width and formatting information
func (col *Columns) Update(d *Instruction) {
	if len(d.Location) > col.Widths.Location {
		col.Widths.Location = len(d.Location)
	}
	if len(d.Bytecode) > col.Widths.Bytecode {
		col.Widths.Bytecode = len(d.Bytecode)
	}
	if len(d.Address) > col.Widths.Address {
		col.Widths.Address = len(d.Address)
	}
	if len(d.Mnemonic) > col.Widths.Mnemonic {
		col.Widths.Mnemonic = len(d.Mnemonic)
	}
	if len(d.Operand) > col.Widths.Operand {
		col.Widths.Operand = len(d.Operand)
	}
	if len(d.Cycles) > col.Widths.Cycles {
		col.Widths.Cycles = len(d.Cycles)
	}
	if len(d.Notes) > col.Widths.Notes {
		col.Widths.Notes = len(d.Notes)
	}

	col.Fmt.Location = fmt.Sprintf("%%%ds", col.Widths.Location)
	col.Fmt.Bytecode = fmt.Sprintf("%%%ds", col.Widths.Bytecode)
	col.Fmt.Address = fmt.Sprintf("%%%ds", col.Widths.Address)
	col.Fmt.Mnemonic = fmt.Sprintf("%%%ds", col.Widths.Mnemonic)
	col.Fmt.Operand = fmt.Sprintf("%%%ds", col.Widths.Operand)
	col.Fmt.Cycles = fmt.Sprintf("%%%ds", col.Widths.Cycles)
	col.Fmt.Notes = fmt.Sprintf("%%%ds", col.Widths.Notes)
}
