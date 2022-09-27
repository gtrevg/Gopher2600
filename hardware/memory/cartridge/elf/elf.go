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

package elf

import (
	"debug/dwarf"
	"debug/elf"
	"encoding/binary"
	"fmt"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/hardware/instance"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/arm"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
	"github.com/jetsetilly/gopher2600/hardware/memory/cpubus"
	"github.com/jetsetilly/gopher2600/hardware/memory/memorymap"
	"github.com/jetsetilly/gopher2600/logger"
)

// Elf implements the mapper.CartMapper interface.
type Elf struct {
	instance *instance.Instance
	dev      mapper.CartCoProcDeveloper

	version   string
	pathToROM string

	arm *arm.ARM
	mem *elfMemory

	// the relocated dwarf data
	dwarf *dwarf.Data

	// parallelARM is true whenever the address bus is not a cartridge address (ie.
	// a TIA or RIOT address). this means that the arm is running unhindered
	// and will not have yielded for that colour clock
	parallelARM bool

	// armState is a copy of the ARM's state at the moment of the most recent
	// Snapshot. it's used only suring a Plumb() operation
	armState *arm.ARMState
}

// NewElf is the preferred method of initialisation for the Elf type.
func NewElf(instance *instance.Instance, pathToROM string) (mapper.CartMapper, error) {
	f, err := elf.Open(pathToROM)
	if err != nil {
		return nil, curated.Errorf("ELF: %v", err)
	}
	defer f.Close()

	// sanity checks on ELF data
	if f.FileHeader.Machine != elf.EM_ARM {
		return nil, curated.Errorf("ELF: is not ARM")
	}
	if f.FileHeader.ByteOrder != binary.LittleEndian {
		return nil, curated.Errorf("ELF: is not little-endian")
	}
	if f.FileHeader.Version != elf.EV_CURRENT {
		return nil, curated.Errorf("ELF: unknown version")
	}
	if f.FileHeader.Type != elf.ET_REL {
		return nil, curated.Errorf("ELF: is not relocatable")
	}

	cart := &Elf{
		instance:  instance,
		pathToROM: pathToROM,
	}

	cart.mem, err = newElfMemory(f)
	if err != nil {
		return nil, err
	}

	cart.arm = arm.NewARM(arm.ARMv7_M, arm.MAMfull, cart.mem.model, cart.instance.Prefs.ARM, cart.mem, cart)
	cart.mem.Plumb(cart.arm)

	cart.mem.busStuffingInit()

	// defer reset until the VCS tries to read the cpubus.Reset address

	cart.dwarf, err = f.DWARF()
	if err != nil {
		logger.Logf("ELF", "error retrieving DWARF data: %s", err.Error())
	}

	return cart, nil
}

// MappedBanks implements the mapper.CartMapper interface.
func (cart *Elf) MappedBanks() string {
	return fmt.Sprintf("Bank: none")
}

// ID implements the mapper.CartMapper interface.
func (cart *Elf) ID() string {
	return "ELF"
}

// Snapshot implements the mapper.CartMapper interface.
func (cart *Elf) Snapshot() mapper.CartMapper {
	n := *cart

	// taking a snapshot of ARM state via the ARM itself can cause havoc if
	// this instance of the cart is not current (because the ARM pointer itself
	// may be stale or pointing to another emulation)
	if cart.armState == nil {
		n.armState = cart.arm.Snapshot()
	} else {
		n.armState = cart.armState.Snapshot()
	}

	n.mem = cart.mem.Snapshot()
	return &n
}

// Plumb implements the mapper.CartMapper interface.
func (cart *Elf) PlumbFromDifferentEmulation() {
	if cart.armState == nil {
		panic("cannot plumb this ELF instance because the ARM state is nil")
	}
	cart.arm = arm.NewARM(arm.ARMv7_M, arm.MAMfull, cart.mem.model, cart.instance.Prefs.ARM, cart.mem, cart)
	cart.mem.Plumb(cart.arm)
	cart.arm.Plumb(cart.armState, cart.mem, cart)
	cart.armState = nil
}

// Plumb implements the mapper.CartMapper interface.
func (cart *Elf) Plumb() {
	if cart.armState == nil {
		panic("cannot plumb this ELF instance because the ARM state is nil")
	}
	cart.mem.Plumb(cart.arm)
	cart.arm.Plumb(cart.armState, cart.mem, cart)
	cart.armState = nil
}

// Reset implements the mapper.CartMapper interface.
func (cart *Elf) Reset() {
}

// reset is distinct from Reset(). this reset function is implied by the
// reading of the cpubus.Reset address.
func (cart *Elf) reset() {
	cart.mem.setStrongArmFunction(vcsEmulationInit)

	// set arguments for initial execution of ARM program
	cart.mem.args[argAddrSystemType-argOrigin] = argSystemType_NTSC
	cart.mem.args[argAddrClockHz-argOrigin] = 0xef
	cart.mem.args[argAddrClockHz-argOrigin+1] = 0xbe
	cart.mem.args[argAddrClockHz-argOrigin+2] = 0xad
	cart.mem.args[argAddrClockHz-argOrigin+3] = 0xde
	cart.arm.SetInitialRegisters(argOrigin)
}

// Read implements the mapper.CartMapper interface.
func (cart *Elf) Read(addr uint16, passive bool) (uint8, error) {
	if passive {
		cart.Listen(addr|memorymap.OriginCart, 0x00)
	}
	return cart.mem.gpio.B[fromArm_Opcode], nil
}

// Write implements the mapper.CartMapper interface.
func (cart *Elf) Write(addr uint16, data uint8, passive bool, poke bool) error {
	if passive || poke {
		return nil
	}

	return nil
}

// NumBanks implements the mapper.CartMapper interface.
func (cart *Elf) NumBanks() int {
	return 1
}

// GetBank implements the mapper.CartMapper interface.
func (cart *Elf) GetBank(_ uint16) mapper.BankInfo {
	return mapper.BankInfo{Number: 0, IsRAM: false}
}

// Patch implements the mapper.CartMapper interface.
func (cart *Elf) Patch(_ int, _ uint8) error {
	return curated.Errorf("ELF: patching unsupported")
}

func (cart *Elf) runARM() {
	if cart.dev != nil {
		cart.dev.StartProfiling()
		defer cart.dev.ProcessProfiling()
	}
	cart.arm.Run()
}

// try to run strongarm function. returns success.
func (cart *Elf) runStrongarm(addr uint16, data uint8) bool {
	if cart.mem.strongarm.running.function != nil {
		cart.mem.gpio.B[toArm_data] = data
		cart.mem.gpio.A[toArm_address] = uint8(addr)
		cart.mem.gpio.A[toArm_address+1] = uint8(addr >> 8)
		cart.mem.strongarm.running.function(cart.mem)

		if cart.mem.strongarm.running.function == nil {
			cart.runARM()
			if cart.mem.strongarm.running.function != nil {
				cart.mem.strongarm.running.function(cart.mem)
			}
		}

		// if the most recently run strongarm function has instructed the ARM
		// emulation to resume immediately then we loop until we encounter one
		// which wants to yield to the VCS
		for cart.mem.resumeARMimmediately {
			cart.mem.resumeARMimmediately = false
			cart.runARM()
			if cart.mem.strongarm.running.function != nil {
				cart.mem.strongarm.running.function(cart.mem)
			}
		}

		return true
	}
	return false
}

// Listen implements the mapper.CartMapper interface.
func (cart *Elf) Listen(addr uint16, data uint8) {
	// if memory access is not a cartridge address (ie. a TIA or RIOT address)
	// then the ARM is running in parallel (ie. no synchronisation)
	cart.parallelARM = (addr&memorymap.OriginCart != memorymap.OriginCart)

	// if address is the reset address then trigger the reset procedure
	if (addr&memorymap.CartridgeBits)|memorymap.OriginCart == cpubus.Reset {
		cart.reset()
	}

	if cart.runStrongarm(addr, data) {
		return
	}

	// set data first and continue once. this seems to be necessary to allow
	// the PlusROM exit rountine to work correctly
	cart.mem.gpio.B[toArm_data] = data

	cart.runARM()
	if cart.runStrongarm(addr, data) {
		return
	}

	// set address and continue
	cart.mem.gpio.A[toArm_address] = uint8(addr)
	cart.mem.gpio.A[toArm_address+1] = uint8(addr >> 8)

	cart.runARM()
	if cart.runStrongarm(addr, data) {
		return
	}

	cart.runARM()
	if cart.runStrongarm(addr, data) {
		return
	}

	cart.runARM()

	// we must understand that the above synchronisation is almost certainly
	// "wrong" in the general sense. it works for the examples seen so far but
	// that means nothing
}

// Step implements the mapper.CartMapper interface.
func (cart *Elf) Step(clock float32) {
	cart.arm.Step(clock)
}

// IterateBank implements the mapper.CartMapper interface.
func (cart *Elf) CopyBanks() []mapper.BankContent {
	return nil
}

// implements arm.CartridgeHook interface.
func (cart *Elf) ARMinterrupt(addr uint32, val1 uint32, val2 uint32) (arm.ARMinterruptReturn, error) {
	return arm.ARMinterruptReturn{}, nil
}

// BusStuff implements the mapper.CartBusStuff interface.
func (cart *Elf) BusStuff() (uint8, bool) {
	return cart.mem.busStuffData, cart.mem.busStuff
}

// CoProcID implements the mapper.CartCoProc interface.
func (cart *Elf) CoProcID() string {
	return cart.arm.CoProcID()
}

// SetDisassembler implements the mapper.CartCoProc interface.
func (cart *Elf) SetDisassembler(disasm mapper.CartCoProcDisassembler) {
	cart.arm.SetDisassembler(disasm)
}

// SetDeveloper implements the mapper.CartCoProc interface.
func (cart *Elf) SetDeveloper(dev mapper.CartCoProcDeveloper) {
	cart.dev = dev
	cart.arm.SetDeveloper(dev)
}

// DWARF implements the mapper.CartCoProc interface.
func (cart *Elf) DWARF() *dwarf.Data {
	return cart.dwarf
}

// ELFSection implements the mapper.CartCoProc interface.
func (cart *Elf) ELFSection(name string) (uint32, bool) {
	if sec, ok := cart.mem.sections[name]; ok {
		return sec.origin, true
	}
	return 0, false
}

// CoProcState implements the mapper.CartCoProc interface.
func (cart *Elf) CoProcState() mapper.CoProcState {
	if cart.parallelARM {
		return mapper.CoProcParallel
	}
	return mapper.CoProcStrongARMFeed
}

// BreakpointHasTriggered implements the mapper.CartCoProc interface.
func (cart *Elf) BreakpointHasTriggered() bool {
	return false
}

// ResumeAfterBreakpoint implements the mapper.CartCoProc interface.
func (cart *Elf) ResumeAfterBreakpoint() error {
	return nil
}

// BreakpointsDisable implements the mapper.CartCoProc interface.
func (cart *Elf) BreakpointsDisable(disable bool) {
	cart.arm.BreakpointsDisable(disable)
}
