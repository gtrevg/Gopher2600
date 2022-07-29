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
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/arm"
	"github.com/jetsetilly/gopher2600/hardware/memory/memorymap"
)

// signature of a strongarm function. a pointer to an instance of elfMemory is
// passed as an argument, rather than the function being a memory of elfMemory.
// this makes the Plumb() function far simpler.
type strongArmFunction func(*elfMemory)

type strongArmFunctionState struct {
	function  strongArmFunction
	state     int
	registers [arm.NumRegisters]uint32

	// the vcsCopyOverblankToRiotRam() function is a loop. we need to keep
	// track of the loop counter and sub-state in addition to the normal state
	// value
	//
	// the mechanism can be used for other looping functions
	counter    int
	subCounter int
}

// state of the strongarm emulation. not all ELF binaries make uses of the
// strongarm functions, in those instances strongArmState will be unused
type strongArmState struct {
	running strongArmFunctionState
	pushed  strongArmFunctionState

	// the expected next 6507 address to be working with
	nextRomAddress uint16

	// bus stuffing
	lowMask          uint8
	correctionMaskHi uint8
	correctionMaskLo uint8

	opcodeLookup [256]uint8
	modeLookup   [256]uint8
}

// strongARM functions need to return to the main program with a branch exchange
var strongArmStub = []byte{
	0x70, 0x47, // BX LR
	0x00, 0x00,
}

func (mem *elfMemory) setNextRomAddress(addr uint16) {
	mem.strongarm.nextRomAddress = addr & memorymap.Memtop
}

func (mem *elfMemory) injectRomByte(v uint8) bool {
	addrIn := uint16(mem.gpio.A[toArm_address])
	addrIn |= uint16(mem.gpio.A[toArm_address+1]) << 8
	addrIn &= memorymap.Memtop

	if addrIn != mem.strongarm.nextRomAddress {
		return false
	}

	mem.gpio.B[fromArm_Opcode] = v
	mem.strongarm.nextRomAddress++

	return true
}

func (mem *elfMemory) yieldDataBus(addr uint16) bool {
	addrIn := uint16(mem.gpio.A[toArm_address])
	addrIn |= uint16(mem.gpio.A[toArm_address+1]) << 8
	addrIn &= memorymap.Memtop

	if addrIn != addr {
		return false
	}

	return true
}

// void vcsWrite3(uint8_t ZP, uint8_t data)
func vcsWrite3(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	data := uint8(mem.strongarm.running.registers[1])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(mem.strongarm.opcodeLookup[data]) {
			mem.strongarm.running.state++
		}
	case 1:
		mem.busStuff = true
		mem.busStuffData = data
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.yieldDataBus(uint16(zp)) {
			mem.busStuff = false
			mem.endStrongArmFunction()
		}
	}
}

// void vcsJmp3()
func vcsJmp3(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x4c) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(0x00) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(0x10) {
			mem.endStrongArmFunction()
			mem.setNextRomAddress(0x1000)
		}
	}
}

// void vcsLda2(uint8_t data)
func vcsLda2(mem *elfMemory) {
	data := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0xa9) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(data) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsSta3(uint8_t ZP)
func vcsSta3(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x85) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.yieldDataBus(uint16(zp)) {
			mem.endStrongArmFunction()
		}
	}
}

// uint8_t snoopDataBus(uint16_t address)
func snoopDataBus(mem *elfMemory) {
	addrIn := uint16(mem.gpio.A[toArm_address])
	addrIn |= uint16(mem.gpio.A[toArm_address+1]) << 8
	addrIn &= memorymap.Memtop

	switch mem.strongarm.running.state {
	case 0:
		if addrIn == mem.strongarm.nextRomAddress {
			mem.strongarm.running.registers[0] = uint32(mem.gpio.B[toArm_data])
			mem.arm.SetRegisters(mem.strongarm.running.registers)
			mem.endStrongArmFunction()
		}
	}

	// note that this implementation of snoopDataBus is missing the "give
	// peripheral time to respond" loop that we see in the real vcsLib
}

// uint8_t vcsRead4(uint16_t address)
func vcsRead4(mem *elfMemory) {
	address := uint16(mem.strongarm.running.registers[0])
	address &= memorymap.Memtop

	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0xad) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(uint8(address)) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(uint8(address >> 8)) {
			mem.setStrongArmFunction(snoopDataBus)
			mem.strongarm.running.function(mem)
		}
	}
}

// void vcsStartOverblank()
func vcsStartOverblank(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x4c) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(0x80) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(0x00) {
			mem.strongarm.running.state++
		}
	case 3:
		if mem.yieldDataBus(uint16(0x0080)) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsEndOverblank()
func vcsEndOverblank(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		mem.setNextRomAddress(0x1fff)
		if mem.injectRomByte(0x00) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.yieldDataBus(uint16(0x00ac)) {
			mem.setNextRomAddress(0x1000)
			mem.endStrongArmFunction()
		}
	}
}

// void vcsLdaForBusStuff2()
func vcsLdaForBusStuff2(mem *elfMemory) {
	mem.setStrongArmFunction(vcsLda2, 0xff)
	mem.strongarm.running.function(mem)
}

// void vcsLdxForBusStuff2()
func vcsLdxForBusStuff2(mem *elfMemory) {
	mem.setStrongArmFunction(vcsLdx2, 0xff)
	mem.strongarm.running.function(mem)
}

// void vcsLdyForBusStuff2()
func vcsLdyForBusStuff2(mem *elfMemory) {
	mem.setStrongArmFunction(vcsLdy2, 0xff)
	mem.strongarm.running.function(mem)
}

// void vcsWrite5(uint8_t ZP, uint8_t data)
func vcsWrite5(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	data := uint8(mem.strongarm.running.registers[1])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0xa9) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(data) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(0x85) {
			mem.strongarm.running.state++
		}
	case 3:
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 4:
		if mem.yieldDataBus(uint16(zp)) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsLdx2(uint8_t data)
func vcsLdx2(mem *elfMemory) {
	data := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0xa2) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(data) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsLdy2(uint8_t data)
func vcsLdy2(mem *elfMemory) {
	data := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0xa0) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(data) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsSta4(uint8_t ZP)
func vcsSta4(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x8d) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(0) {
			mem.strongarm.running.state++
		}
	case 3:
		if mem.yieldDataBus(uint16(zp)) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsStx3(uint8_t ZP)
func vcsStx3(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x86) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.yieldDataBus(uint16(zp)) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsStx4(uint8_t ZP)
func vcsStx4(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x8e) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(0) {
			mem.strongarm.running.state++
		}
	case 3:
		if mem.yieldDataBus(uint16(zp)) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsSty3(uint8_t ZP)
func vcsSty3(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x84) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.yieldDataBus(uint16(zp)) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsSty4(uint8_t ZP)
func vcsSty4(mem *elfMemory) {
	zp := uint8(mem.strongarm.running.registers[0])
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x8c) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(zp) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(0) {
			mem.strongarm.running.state++
		}
	case 3:
		if mem.yieldDataBus(uint16(zp)) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsTxs2()
func vcsTxs2(mem *elfMemory) {
	panic("vcsTxs2")
}

// void vcsJsr6(uint16_t target)
func vcsJsr6(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0x20) {
			mem.strongarm.running.state++
		}
	case 1:
		if mem.injectRomByte(uint8(mem.strongarm.running.registers[0])) {
			mem.strongarm.running.state++
		}
	case 2:
		if mem.injectRomByte(uint8(mem.strongarm.running.registers[0] >> 8)) {
			mem.gpio.A[toArm_address] = uint8(mem.strongarm.running.registers[0])
			mem.gpio.A[toArm_address+1] = uint8(mem.strongarm.running.registers[0] >> 8)
			mem.gpio.A[toArm_address+2] = uint8(mem.strongarm.running.registers[0] >> 16)
			mem.gpio.A[toArm_address+3] = uint8(mem.strongarm.running.registers[0] >> 24)

			mem.endStrongArmFunction()
		}
	}
}

// void vcsNop2()
func vcsNop2(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0xea) {
			mem.endStrongArmFunction()
		}
	}
}

// void vcsNop2n(uint16_t n)
func vcsNop2n(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		if mem.injectRomByte(0xea) {
			mem.strongarm.nextRomAddress += uint16(mem.strongarm.running.registers[0]) - 1
			mem.endStrongArmFunction()
		}
	}
}

// void vcsCopyOverblankToRiotRam()
func vcsCopyOverblankToRiotRam(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		if mem.strongarm.running.counter >= len(overblank) {
			mem.endStrongArmFunction()
			return
		}
		mem.strongarm.running.state++
		mem.strongarm.running.subCounter = 0
		fallthrough
	case 1:
		switch mem.strongarm.running.subCounter {
		case 0:
			if mem.injectRomByte(0xa9) {
				mem.strongarm.running.subCounter++
			}
		case 1:
			if mem.injectRomByte(overblank[mem.strongarm.running.counter]) {
				mem.strongarm.running.subCounter++
			}
		case 2:
			if mem.injectRomByte(0x85) {
				mem.strongarm.running.subCounter++
			}
		case 3:
			if mem.injectRomByte(uint8(0x80 + mem.strongarm.running.counter)) {
				mem.strongarm.running.subCounter++
			}
		case 4:
			if mem.yieldDataBus(uint16(0x80 + mem.strongarm.running.counter)) {
				mem.strongarm.running.counter++
				mem.strongarm.running.state = 0
			}
		}
	}
}

func vcsEmulationInit(mem *elfMemory) {
	switch mem.strongarm.running.state {
	case 0:
		mem.gpio.B[fromArm_Opcode] = 0x00
		mem.strongarm.running.state++
	case 1:
		mem.strongarm.running.state++
	case 2:
		mem.strongarm.running.state++
	case 3:
		mem.gpio.B[fromArm_Opcode] = 0x10
		mem.setNextRomAddress(0x1000)
		mem.endStrongArmFunction()
	}
}

func (str *strongArmState) updateLookupTables() {
	for i := 0; i < 256; i++ {
		if uint8(i)&str.correctionMaskHi == str.correctionMaskHi {
			if uint8(i)&str.correctionMaskLo == str.correctionMaskLo {
				str.opcodeLookup[i] = 0x84
			} else {
				str.opcodeLookup[i] = 0x86
			}
		} else {
			if uint8(i)&str.correctionMaskLo == str.correctionMaskLo {
				str.opcodeLookup[i] = 0x85
			} else {
				str.opcodeLookup[i] = 0x87
			}
		}

		mode := uint8(i) ^ str.lowMask

		// never drive the bits that get corrected by opcodes above
		mode &= ^str.correctionMaskLo
		mode &= ^str.correctionMaskHi

		str.modeLookup[i] = ((mode & 0x80) << 7) |
			((mode & 0x40) << 6) |
			((mode & 0x20) << 5) |
			((mode & 0x10) << 4) |
			((mode & 0x08) << 3) |
			((mode & 0x04) << 2) |
			((mode & 0x02) << 1) |
			(mode & 0x01)
	}
}

// initialise state rady for bus stuffing. we know bus stuffing is used if the
// vcsWrite3() function has been detected (during relocation).
func (mem *elfMemory) busStuffingInit() {
	if !mem.usesBusStuffing {
		return
	}

	mem.strongarm.lowMask = 0xff
	mem.strongarm.correctionMaskHi = 0x00
	mem.strongarm.correctionMaskLo = 0x00
	mem.strongarm.updateLookupTables()
}

// setStrongArmFunction initialises the next function to run. it takes a copy
// of the ARM registers at that point of initialisation. the register values
// are used to supply arguments to the strongArmFunction, as many as the
// function requires (up to 32). any arguments provided to the function will
// be used instead of the corresponding register value (numbered from 0 to 31)
func (mem *elfMemory) setStrongArmFunction(f strongArmFunction, args ...uint32) {
	mem.strongarm.running.function = f
	mem.strongarm.running.state = 0
	mem.strongarm.running.registers = mem.arm.Registers()

	for i, arg := range args {
		mem.strongarm.running.registers[i] = arg
	}
}

// a strongArmFunction should always end with a call to endFunction() no matter
// how many execution states it has.
func (mem *elfMemory) endStrongArmFunction() {
	mem.strongarm.running = mem.strongarm.pushed
	mem.strongarm.pushed.function = nil
}

// pushStrongArmFunction is like setStrongArmFunction except that it saves, or
// pushes, the current state of the running function. the pushed function will
// resume on the next call to endStrongArmFunction
//
// calling setStrongArmFunction when a previous function has been pushed is
// okay. it just means that the pushed function will resume after the most
// recent function ends
//
// calling pushStrongArmFunction when there is already a pushed function will
// cause the application to panic
func (mem *elfMemory) pushStrongArmFunction(f strongArmFunction, args ...uint32) {
	if mem.strongarm.pushed.function != nil {
		panic("pushing an already pushed function")
	}

	mem.strongarm.pushed = mem.strongarm.running
	mem.setStrongArmFunction(f, args...)
}
