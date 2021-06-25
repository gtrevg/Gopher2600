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

package symbols

import (
	"fmt"
	"sort"
	"sync"

	"github.com/jetsetilly/gopher2600/hardware/memory/memorymap"
)

// Symbols contains the all currently defined symbols.
type Symbols struct {
	// the master table is made up of three sub-tables
	label []*table
	read  *table
	write *table

	crit sync.Mutex
}

// newSymbols is the preferred method of initialisation for the Symbols type. In
// many instances however, ReadSymbolsFile() might be more appropriate.
func (sym *Symbols) initialise(numBanks int) {
	sym.label = make([]*table, numBanks)
	for i := range sym.label {
		sym.label[i] = newTable()
	}

	sym.read = newTable()
	sym.write = newTable()

	sym.canonise(nil)
}

func (sym *Symbols) resort() {
	for _, l := range sym.label {
		sort.Sort(l)
	}
	sort.Sort(sym.read)
	sort.Sort(sym.write)
}

// LabelWidth returns the maximum number of characters required by a label in
// the label table.
func (sym *Symbols) LabelWidth() int {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	max := 0
	for _, l := range sym.label {
		if l.maxWidth > max {
			max = l.maxWidth
		}
	}
	return max
}

// SymbolWidth returns the maximum number of characters required by a symbol in
// the read/write table.
func (sym *Symbols) SymbolWidth() int {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	if sym.read.maxWidth > sym.write.maxWidth {
		return sym.read.maxWidth
	}
	return sym.write.maxWidth
}

// Get symbol from label table.
//
// The problem with this function is that it can't handle getting labels if a
// JMP for example, triggers a bankswtich at the same time. We can see this in
// E7 type cartridges. For example the second instruction of HeMan JMPs from
// bank 7 to bank 5. Short of having a copy of the label in every bank, or more
// entwined knowledge of how cartridge mappers work, there's not a lot we can
// do about this.
func (sym *Symbols) GetLabel(bank int, addr uint16) (string, bool) {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	if bank >= len(sym.label) {
		return "", false
	}

	addr, _ = memorymap.MapAddress(addr, true)

	if v, ok := sym.label[bank].byAddr[addr]; ok {
		return v, ok
	}

	return "", false
}

// Add symbol to label table. Symbol will be modified so that it is unique in
// the label table.
func (sym *Symbols) AddLabel(bank int, addr uint16, symbol string) bool {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	if bank >= len(sym.label) {
		return false
	}

	addr, _ = memorymap.MapAddress(addr, true)

	return sym.label[bank].add(addr, symbol)
}

// Add symbol to label table using a symbols created from the address information.
func (sym *Symbols) AddLabelAuto(bank int, addr uint16) bool {
	return sym.AddLabel(bank, addr, fmt.Sprintf("L%04X", addr))
}

// Remove label from label table. Symbol will be modified so that it is unique
// in the label table.
func (sym *Symbols) RemoveLabel(bank int, addr uint16) bool {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	addr, _ = memorymap.MapAddress(addr, true)

	return sym.label[bank].remove(addr)
}

// Update symbol in label table. Returns success.
func (sym *Symbols) UpdateLabel(bank int, addr uint16, oldLabel string, newLabel string) bool {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	if bank >= len(sym.label) {
		return false
	}

	addr, _ = memorymap.MapAddress(addr, true)

	return sym.label[bank].update(addr, oldLabel, newLabel)
}

// Getsymbol from read/write table.
//
// The read argument selects the table: true -> read table, false -> write table.
func (sym *Symbols) GetSymbol(addr uint16, read bool) (string, bool) {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	addr, _ = memorymap.MapAddress(addr, true)

	if read {
		return sym.read.get(addr)
	}

	return sym.write.get(addr)
}

// AddSymbol to read/write table. Symbol will be modified so that it is unique
// in the selected table.
//
// The read argument selects the table: true -> read table, false -> write table.
func (sym *Symbols) AddSymbol(addr uint16, symbol string, read bool) bool {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	addr, _ = memorymap.MapAddress(addr, true)

	if read {
		return sym.read.add(addr, symbol)
	}

	return sym.write.add(addr, symbol)
}

// RemoveSymbol from read/write table.
//
// The read argument selects the table: true -> read table, false -> write table.
func (sym *Symbols) RemoveSymbol(addr uint16, read bool) bool {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	addr, _ = memorymap.MapAddress(addr, true)

	if read {
		return sym.read.remove(addr)
	}

	return sym.write.remove(addr)
}

// UpdateSymbol in read/write table. Symbol will be modified so that it is
// unique in the selected table
//
// The read argument selects the table: true -> read table, false -> write table.
func (sym *Symbols) UpdateSymbol(addr uint16, oldLabel string, newLabel string, read bool) bool {
	sym.crit.Lock()
	defer sym.crit.Unlock()

	addr, _ = memorymap.MapAddress(addr, true)

	if read {
		return sym.read.update(addr, oldLabel, newLabel)
	}

	return sym.write.update(addr, oldLabel, newLabel)
}
