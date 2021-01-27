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
	"sync"

	"github.com/jetsetilly/gopher2600/cartridgeloader"
	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/disassembly/coprocessor"
	"github.com/jetsetilly/gopher2600/hardware"
	"github.com/jetsetilly/gopher2600/hardware/cpu"
	"github.com/jetsetilly/gopher2600/hardware/cpu/execution"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
	"github.com/jetsetilly/gopher2600/hardware/memory/memorymap"
	"github.com/jetsetilly/gopher2600/symbols"
)

// Disassembly represents the annotated disassembly of a 6507 binary.
type Disassembly struct {
	Prefs *Preferences

	// reference to running hardware
	vcs *hardware.VCS

	// the cartridge to which the disassembly refers
	cart *cartridge.Cartridge

	// symbols used to format disassembly output
	Symbols *symbols.Symbols

	// indexed by bank and address. address should be masked with memorymap.CartridgeBits before access
	entries [][]*Entry

	// any cartridge coprocessor that we find
	Coprocessor *coprocessor.Coprocessor

	// critical sectioning. the iteration functions in particular may be called
	// from a different goroutine. entries in the (disasm array) will likely be
	// updating more or less constantly with ExecuteEntry() so it's important
	// we enforce the critical sections
	//
	// experiments with gochannel driven disassembly service proved too slow
	// for iterating. this is because waiting for the result from any disasm
	// service goroutine is inherently slow.
	//
	// whether a sync.Mutex is the best low level synchronisation method is
	// another question.
	crit sync.Mutex
}

func NewDisassembly(vcs *hardware.VCS) (*Disassembly, error) {
	dsm := &Disassembly{vcs: vcs}

	var err error

	dsm.Prefs, err = newPreferences(dsm)
	if err != nil {
		return nil, curated.Errorf("disassembly: %v", err)
	}

	return dsm, nil
}

// FromCartridge initialises a new partial emulation and returns a disassembly
// from the supplied cartridge filename. Useful for one-shot disassemblies,
// like the gopher2600 "disasm" mode.
func FromCartridge(cartload cartridgeloader.Loader) (*Disassembly, error) {
	dsm, err := NewDisassembly(nil)
	if err != nil {
		return nil, err
	}

	cart := cartridge.NewCartridge(nil)

	err = cart.Attach(cartload)
	if err != nil {
		return nil, curated.Errorf("disassembly: %v", err)
	}

	// ignore errors caused by loading of symbols table - we always get a
	// standard symbols table even in the event of an error
	symbols, _ := symbols.ReadSymbolsFile(cart)

	// do disassembly
	err = dsm.FromMemory(cart, symbols)
	if err != nil {
		return nil, curated.Errorf("disassembly: %v", err)
	}

	return dsm, nil
}

// FromMemory disassembles an existing instance of cartridge memory using a
// cpu with no flow control. Unlike the FromCartridge() function this function
// requires an existing instance of Disassembly
//
// cartridge will finish in its initialised state.
func (dsm *Disassembly) FromMemory(cart *cartridge.Cartridge, symbols *symbols.Symbols) error {
	// record cartridge if it is not nil
	if cart != nil {
		dsm.cart = cart
	}

	// an nil value for the cart argument indicates the disassembly is do be
	// redone. it is important therefore that no reference is made to
	// cart. use dsm.cart instead.

	if symbols != nil {
		dsm.Symbols = symbols
	}

	// allocate memory for disassembly. the GUI may find itself trying to
	// iterate through disassembly at the same time as we're doing this.
	dsm.crit.Lock()
	dsm.entries = make([][]*Entry, dsm.cart.NumBanks())
	for b := 0; b < len(dsm.entries); b++ {
		dsm.entries[b] = make([]*Entry, memorymap.CartridgeBits+1)
	}
	dsm.crit.Unlock()

	// exit early if cartridge memory self reports as being ejected
	if dsm.cart.IsEjected() {
		return nil
	}

	// create new memory
	mem := &disasmMemory{}

	// create a new NoFlowControl CPU to help disassemble memory
	mc := cpu.NewCPU(nil, mem)
	mc.NoFlowControl = true

	// disassemble cartridge binary
	err := dsm.disassemble(mc, mem)
	if err != nil {
		return curated.Errorf("disassembly: %v", err)
	}

	// try added coprocessor disasm support
	dsm.Coprocessor = coprocessor.Add(dsm.vcs, dsm.cart)

	return nil
}

// GetEntryByAddress returns the disassembly entry at the specified
// bank/address. a returned value of nil indicates the entry is not in the
// cartridge; this will usually mean the address is in main VCS RAM.
//
// also returns whether cartridge is currently working from another source
// meaning that the disassembly entry might not be reliable.
func (dsm *Disassembly) GetEntryByAddress(address uint16) (*Entry, bool) {
	bank := dsm.cart.GetBank(address)

	if bank.NonCart {
		// !!TODO: attempt to decode instructions not in cartridge
		return nil, bank.ExecutingCoprocessor
	}

	return dsm.entries[bank.Number][address&memorymap.CartridgeBits], bank.ExecutingCoprocessor
}

// ExecutedEntry creates an Entry from a cpu result that has actually been
// executed. When appropriate, the newly created Entry replaces the previous
// equivalent entry in the disassembly.
//
// If the execution.Result was from an instruction in RAM (cartridge RAM or VCS
// RAM) then the newly created entry is returned but not stored anywhere in the
// Disassembly.
func (dsm *Disassembly) ExecutedEntry(bank mapper.BankInfo, result execution.Result, nextAddr uint16) (*Entry, error) {
	// not touching any result which is not in cartridge space. we are noting
	// execution results from cartridge RAM. the banks.Details field in the
	// disassembly entry notes whether execution was from RAM
	if bank.NonCart {
		return dsm.FormatResult(bank, result, EntryLevelExecuted)
	}

	if bank.Number >= len(dsm.entries) {
		return dsm.FormatResult(bank, result, EntryLevelExecuted)
	}

	idx := result.Address & memorymap.CartridgeBits

	// get entry at address
	e := dsm.entries[bank.Number][idx]

	// updating an origin can happen at the same time as iteration which is
	// probably being run from a different goroutine. acknowledge the critical
	// section
	dsm.crit.Lock()
	defer dsm.crit.Unlock()

	// check for opcode reliability. this can happen when it is expected
	// (bank.ExecutingCoProcess is true) or when it is unexpected.
	if bank.ExecutingCoprocessor || e.Result.Defn.OpCode != result.Defn.OpCode {
		// in either instance we want to return the formatted result of the
		// actual execution
		ne, err := dsm.FormatResult(bank, result, EntryLevelExecuted)
		if err != nil {
			return nil, curated.Errorf("disassembly: %v", err)
		}

		return ne, nil
	}

	// opcode is reliable update disasm entry in the normal way
	if e == nil {
		// we're not decoded this bank/address before. note this shouldn't even happen
		var err error
		dsm.entries[bank.Number][idx], err = dsm.FormatResult(bank, result, EntryLevelExecuted)
		if err != nil {
			return nil, curated.Errorf("disassembly: %v", err)
		}
	} else if e.Level < EntryLevelExecuted {
		// we have seen this entry before but it's not been executed. update
		// entry to reflect results
		e.updateExecutionEntry(result)
	}

	// bless next entry in case it was missed by the original decoding. there's
	// no guarantee that the bank for the next address will be the same as the
	// current bank, so we have to call the GetBank() function.
	//
	// !!TODO: maybe make sure next entry has been disassembled in it's current form
	bank = dsm.cart.GetBank(nextAddr)
	ne := dsm.entries[bank.Number][nextAddr&memorymap.CartridgeBits]
	if ne.Level < EntryLevelBlessed {
		ne.Level = EntryLevelBlessed
	}

	return e, nil
}
