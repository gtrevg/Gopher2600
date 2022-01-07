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

package disassembly

import (
	"sort"
	"sync"

	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
	"github.com/jetsetilly/gopher2600/hardware/television"
	"github.com/jetsetilly/gopher2600/hardware/television/coords"
)

type TV interface {
	AdjCoords(adj television.Adj, amount int) coords.TelevisionCoords
}

// Disassembly is used to handle the disassembly of instructions from an
// attached cartridge that contains a coprocessor.
type Disassembly struct {
	crit sync.Mutex

	tv   TV
	cart mapper.CartCoProcBus

	disasm DisasmEntries
}

// DisasmEntries contains all the current information about the coprocessor
// disassembly, including whether disassembly is currently enabled.
type DisasmEntries struct {
	Enabled bool

	Entries map[string]mapper.CartCoProcDisasmEntry
	Keys    []string // sorted keys into the disasm map

	LastExecution        []mapper.CartCoProcDisasmEntry
	LastExecutionSummary mapper.CartCoProcDisasmSummary

	LastStart coords.TelevisionCoords
}

// NewDisassembly returns a new Coprocessor instance if cartridge implements the
// coprocessor bus.
func NewDisassembly(tv TV, cart mapper.CartCoProcBus) *Disassembly {
	if cart == nil {
		return nil
	}

	dsm := &Disassembly{
		tv:   tv,
		cart: cart,
		disasm: DisasmEntries{
			LastExecution: make([]mapper.CartCoProcDisasmEntry, 0, 1024),
		},
	}

	dsm.disasm.Entries = make(map[string]mapper.CartCoProcDisasmEntry)
	dsm.disasm.Keys = make([]string, 0, 1024)

	dsm.Enable(false)

	return dsm
}

// IsEnabled returns true if coprocessor disassembly is currently active.
func (dsm *Disassembly) IsEnabled() bool {
	dsm.crit.Lock()
	defer dsm.crit.Unlock()
	return dsm.disasm.Enabled
}

// Enable or disable coprocessor disassembly. We retain the disassembly
// (including last execution) already gathered but the LastExecution field is
// cleared on disable. The general disassembly is maintained.
func (dsm *Disassembly) Enable(enable bool) {
	dsm.crit.Lock()
	defer dsm.crit.Unlock()

	dsm.disasm.Enabled = enable
	if dsm.disasm.Enabled {
		dsm.cart.SetDisassembler(dsm)
	} else {
		dsm.cart.SetDisassembler(nil)
		dsm.disasm.LastExecution = dsm.disasm.LastExecution[:0]
	}
}

// Start implements the CartCoProcDisassembler interface.
func (dsm *Disassembly) Start() {
	dsm.crit.Lock()
	defer dsm.crit.Unlock()

	if dsm.disasm.Enabled {
		// add one clock to frame/scanline/clock values. the Reset() function will
		// have been called on the last CPU cycle of the instruction that triggers
		// the coprocessor reset. the TV will not have moved onto the beginning of
		// the next instruction yet so we must figure it out here
		dsm.disasm.LastStart = dsm.tv.AdjCoords(television.AdjCPUCycle, 1)
	}

	dsm.disasm.LastExecution = dsm.disasm.LastExecution[:0]
}

// Step implements the CartCoProcDisassembler interface.
func (dsm *Disassembly) Step(entry mapper.CartCoProcDisasmEntry) {
	dsm.crit.Lock()
	defer dsm.crit.Unlock()

	// check that coprocessor disassmebler hasn't been disabled in the period
	// while we were waiting for the critical section lock
	if !dsm.disasm.Enabled {
		return
	}

	dsm.disasm.LastExecution = append(dsm.disasm.LastExecution, entry)
}

// End implements the CartCoProcDisassembler interface.
func (dsm *Disassembly) End(summary mapper.CartCoProcDisasmSummary) {
	dsm.crit.Lock()
	defer dsm.crit.Unlock()

	dsm.disasm.LastExecutionSummary = summary

	for _, entry := range dsm.disasm.LastExecution {
		key := entry.Key()
		if _, ok := dsm.disasm.Entries[key]; !ok {
			dsm.disasm.Keys = append(dsm.disasm.Keys, key)
		}
		dsm.disasm.Entries[key] = entry
	}

	sort.Strings(dsm.disasm.Keys)
}

// BorrowDisasm will lock the DisasmEntries structure for the durction of the
// supplied function, which will be executed with the disasm structure as an
// argument.
//
// Should not be called from the emulation goroutine.
func (dsm *Disassembly) BorrowDisassembly(f func(*DisasmEntries)) {
	dsm.crit.Lock()
	defer dsm.crit.Unlock()
	f(&dsm.disasm)
}