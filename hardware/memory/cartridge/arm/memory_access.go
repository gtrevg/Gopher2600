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

package arm

import (
	"github.com/jetsetilly/gopher2600/logger"
)

func (arm *ARM) illegalAccess(event string, addr uint32) {
	logger.Logf("ARM7", "%s: unrecognised address %08x (PC: %08x)", event, addr, arm.state.executingPC)
	if arm.dev == nil {
		return
	}
	log := arm.dev.IllegalAccess(event, arm.state.executingPC, addr)
	if log == "" {
		return
	}
	logger.Logf("ARM7", "%s: %s", event, log)
}

// nullAccess is a special condition of illegalAccess()
func (arm *ARM) nullAccess(event string, addr uint32) {
	logger.Logf("ARM7", "%s: probable null pointer dereference of %08x (PC: %08x)", event, addr, arm.state.executingPC)
	if arm.dev == nil {
		return
	}
	log := arm.dev.NullAccess(event, arm.state.executingPC, addr)
	if log == "" {
		return
	}
	logger.Logf("ARM7", "%s: %s", event, log)
}

func (arm *ARM) read8bit(addr uint32) uint8 {
	if !arm.stackHasCollided && addr < arm.nullAccessBoundary {
		arm.nullAccess("Read 8bit", addr)
	}

	var mem *[]uint8

	mem, addr = arm.mem.MapAddress(addr, false)
	if mem == nil {
		if v, ok, comment := arm.state.timer2.read(addr); ok {
			arm.disasmExecutionNotes = comment
			return uint8(v)
		}
		if v, ok, comment := arm.state.timer.read(addr); ok {
			arm.disasmExecutionNotes = comment
			return uint8(v)
		}
		if v, ok := arm.state.mam.read(addr); ok {
			return uint8(v)
		}

		arm.memoryError = arm.abortOnIllegalMem

		if !arm.stackHasCollided {
			arm.illegalAccess("Read 8bit", addr)
		}

		return 0
	}

	return (*mem)[addr]
}

func (arm *ARM) write8bit(addr uint32, val uint8) {
	if !arm.stackHasCollided && addr < arm.nullAccessBoundary {
		arm.nullAccess("Write 8bit", addr)
	}

	var mem *[]uint8

	mem, addr = arm.mem.MapAddress(addr, true)
	if mem == nil {
		// timer2 cannot be written with 8bit access

		if ok, comment := arm.state.timer.write(addr, uint32(val)); ok {
			arm.disasmExecutionNotes = comment
			return
		}
		if ok := arm.state.mam.write(addr, uint32(val)); ok {
			return
		}

		arm.memoryError = arm.abortOnIllegalMem

		if !arm.stackHasCollided {
			arm.illegalAccess("Write 8bit", addr)
		}

		return
	}

	(*mem)[addr] = val
}

func (arm *ARM) read16bit(addr uint32, requiresAlignment bool) uint16 {
	if !arm.stackHasCollided && addr < arm.nullAccessBoundary {
		arm.nullAccess("Read 16bit", addr)
	}

	// check 16 bit alignment
	misaligned := addr&0x01 != 0x00
	if misaligned && (requiresAlignment || arm.state.unalignTrap) {
		logger.Logf("ARM7", "misaligned 16 bit read (%08x) (PC: %08x)", addr, arm.state.registers[rPC])
		return 0
	}

	var mem *[]uint8

	mem, addr = arm.mem.MapAddress(addr, false)
	if mem == nil {
		if v, ok, comment := arm.state.timer2.read(addr); ok {
			arm.disasmExecutionNotes = comment
			return uint16(v)
		}
		if v, ok, comment := arm.state.timer.read(addr); ok {
			arm.disasmExecutionNotes = comment
			return uint16(v)
		}
		if v, ok := arm.state.mam.read(addr); ok {
			return uint16(v)
		}

		arm.memoryError = arm.abortOnIllegalMem

		if !arm.stackHasCollided {
			arm.illegalAccess("Read 16bit", addr)
		}

		return 0
	}

	return uint16((*mem)[addr]) | (uint16((*mem)[addr+1]) << 8)
}

func (arm *ARM) write16bit(addr uint32, val uint16, requiresAlignment bool) {
	if !arm.stackHasCollided && addr < arm.nullAccessBoundary {
		arm.nullAccess("Write 16bit", addr)
	}

	// check 16 bit alignment
	misaligned := addr&0x01 != 0x00
	if misaligned && (requiresAlignment || arm.state.unalignTrap) {
		logger.Logf("ARM7", "misaligned 16 bit write (%08x) (PC: %08x)", addr, arm.state.registers[rPC])
		return
	}

	var mem *[]uint8

	mem, addr = arm.mem.MapAddress(addr, true)
	if mem == nil {
		if ok, comment := arm.state.timer2.write(addr, uint32(val)); ok {
			arm.disasmExecutionNotes = comment
			return
		}
		if ok, comment := arm.state.timer.write(addr, uint32(val)); ok {
			arm.disasmExecutionNotes = comment
			return
		}
		if ok := arm.state.mam.write(addr, uint32(val)); ok {
			return
		}

		arm.memoryError = arm.abortOnIllegalMem

		if !arm.stackHasCollided {
			arm.illegalAccess("Write 16bit", addr)
		}

		return
	}

	(*mem)[addr] = uint8(val)
	(*mem)[addr+1] = uint8(val >> 8)
}

func (arm *ARM) read32bit(addr uint32, requiresAlignment bool) uint32 {
	if !arm.stackHasCollided && addr < arm.nullAccessBoundary {
		arm.nullAccess("Read 32bit", addr)
	}

	// check 32 bit alignment
	misaligned := addr&0x03 != 0x00
	if misaligned && (requiresAlignment || arm.state.unalignTrap) {
		logger.Logf("ARM7", "misaligned 32 bit read (%08x) (PC: %08x)", addr, arm.state.registers[rPC])
		return 0
	}

	var mem *[]uint8

	mem, addr = arm.mem.MapAddress(addr, false)
	if mem == nil {
		if v, ok, comment := arm.state.timer2.read(addr); ok {
			arm.disasmExecutionNotes = comment
			return v
		}
		if v, ok, comment := arm.state.timer.read(addr); ok {
			arm.disasmExecutionNotes = comment
			return v
		}
		if v, ok := arm.state.mam.read(addr); ok {
			return v
		}

		arm.memoryError = arm.abortOnIllegalMem

		if !arm.stackHasCollided {
			arm.illegalAccess("Read 32bit", addr)
		}

		return 0
	}

	return uint32((*mem)[addr]) | (uint32((*mem)[addr+1]) << 8) | (uint32((*mem)[addr+2]) << 16) | uint32((*mem)[addr+3])<<24
}

func (arm *ARM) write32bit(addr uint32, val uint32, requiresAlignment bool) {
	if !arm.stackHasCollided && addr < arm.nullAccessBoundary {
		arm.nullAccess("Write 32bit", addr)
	}

	// check 32 bit alignment
	misaligned := addr&0x03 != 0x00
	if misaligned && (requiresAlignment || arm.state.unalignTrap) {
		logger.Logf("ARM7", "misaligned 32 bit write (%08x) (PC: %08x)", addr, arm.state.registers[rPC])
		return
	}

	var mem *[]uint8

	mem, addr = arm.mem.MapAddress(addr, true)
	if mem == nil {
		if ok, comment := arm.state.timer2.write(addr, val); ok {
			arm.disasmExecutionNotes = comment
			return
		}
		if ok, comment := arm.state.timer.write(addr, val); ok {
			arm.disasmExecutionNotes = comment
			return
		}
		if ok := arm.state.mam.write(addr, val); ok {
			return
		}

		arm.memoryError = arm.abortOnIllegalMem

		if !arm.stackHasCollided {
			arm.illegalAccess("Write 32bit", addr)
		}

		return
	}

	(*mem)[addr] = uint8(val)
	(*mem)[addr+1] = uint8(val >> 8)
	(*mem)[addr+2] = uint8(val >> 16)
	(*mem)[addr+3] = uint8(val >> 24)
}
