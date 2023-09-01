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
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jetsetilly/gopher2600/coprocessor"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/arm/architecture"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/arm/fpu"
	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/arm/peripherals"
	"github.com/jetsetilly/gopher2600/hardware/preferences"
	"github.com/jetsetilly/gopher2600/logger"
)

// it is sometimes convenient to dissassemble every instruction and to print it
// to stdout for inspection. we most likely need this during the early stages of
// debugging of a new cartridge type
const disassembleToStdout = false

// core register names
const (
	rSB = 9 + iota // static base
	rSL            // stack limit
	rFP            // frame pointer
	rIP            // intra-procedure-call scratch register
	rSP
	rLR
	rPC
	NumCoreRegisters
)

// the maximum number of cycles allowed in a single ARM program execution.
// no idea if this value is sufficient.
//
// 03/02/2022 - raised to 1000000 to accomodate CDFJBoulderDash development
// 17/09/2022 - raised to 1500000 for marcoj's RPG game
const cycleLimit = 1500000

// the maximum number of instructions to execute. like cycleLimit but for when
// running in immediate mode
const instructionsLimit = 1300000

// stepFunction variations are a result of different ARM architectures
type stepFunction func(opcode uint16, memIdx int)

// decodeFunction represents one of the functions that decodes a specific group
// of ARM instructions
//
// decodeFunctions can be called in one of two ways:
//
//	(1) when the ARM argument is *not* nil it indicates that the function should
//	   affect the registers and memory of the emulated ARM as appropriate
//	(2) when the argument *is* nil it indicates that the function should decode
//	   the opcode only so far as is required to produce a DisasmEntry
//
// the return value for decodeFunction can be an instance of DisasmEntry or nil.
// in the case of (1) it is always nil but in the case of (2) nil indicates an
// error
type decodeFunction func() *DisasmEntry

type ARMState struct {
	// ARM registers
	registers [NumCoreRegisters]uint32

	// see note about status register in the documentation of the type
	status status

	mam    mam
	rng    peripherals.RNG
	timer  peripherals.Timer
	timer2 peripherals.Timer2
	fpu    fpu.FPU

	// the PC of the opcode being processed and the PC of the instruction being
	// executed
	//
	// when this emulation was Thumb (16bit only) there was no distiniction
	// between these two concepts and there was only executingPC. with 32bit
	// instructions we need to know about both
	//
	// executingPC will be equal to instructionPC in the case of 16bit
	// instructions but will be different in the case of 32bit instructions
	executingPC   uint32
	instructionPC uint32

	// was the most recent instruction a result of a branch or another
	// instruction that has altered the program counter in some way
	//
	// this flag affects the number of cycles consumed by an instruction and
	// also how we treat breakpoints
	branchedExecution bool

	// the current stack frame of the execution
	stackFrame uint32

	// the yield reason explains the reason for why the ARM execution ended
	yield coprocessor.CoProcYield

	// the area the PC covers. once assigned we'll assume that the program
	// never reads outside this area. the value is assigned on reset()
	programMemory *[]uint8

	// address limits for program memory
	programMemoryOrigin uint32
	programMemoryMemtop uint32

	// currentExecutionCache records the function that implements the instruction group for each
	// opcode in program memory. must be reset every time programMemory is reassigned
	//
	// note that when executing from RAM (which isn't normal) it's possible for
	// code to be modified (ie. self-modifying code). in that case currentExecutionCache
	// may be unreliable.
	currentExecutionCache []decodeFunction

	// if developer information is available then the emulation's stack protection will try to
	// defend against the stack colliding with the top of variable memory
	protectVariableMemTop bool
	variableMemtop        uint32

	// once the stack has been found to have collided there are no more attempts
	// to protect the stack
	stackHasCollided bool

	// cycle counting

	// the last cycle to be triggered, used to decide whether to merge I-S cycles
	lastCycle cycleType

	// the type of cycle next prefetch (the main PC increment in the Run()
	// loop) should be. either N or S type. never I type.
	prefetchCycle cycleType

	// total number of cycles for the entire program
	cyclesTotal float32

	// number of cycles with CLKLEN modulation applied
	stretchedCycles float32

	// record the order in which cycles happen for a single instruction
	// - required for disasm only
	cycleOrder cycleOrder

	// whether a branch has used the branch trail latches or not
	// - required for disasm only
	branchTrail BranchTrail

	// whether an I cycle that is followed by an S cycle has been merged
	// - required for disasm only
	mergedIS bool

	// clocks

	// the number of cycles left over from the previous clock tick
	accumulatedClocks float32

	// 32bit instructions

	// these two flags work as a pair:
	// . is the current instruction a 32bit instruction
	// . was the most recent instruction decoded a 32bit instruction
	instruction32bitDecoding  bool
	instruction32bitResolving bool

	// the first 16bits of the most recent 32bit instruction
	instruction32bitOpcodeHi uint16
}

// Snapshort makes a copy of the ARMState.
func (s *ARMState) Snapshot() *ARMState {
	n := *s
	return &n
}

// ARM implements the ARM7TDMI-S LPC2103 processor.
type ARM struct {
	prefs *preferences.ARMPreferences
	mmap  architecture.Map
	mem   SharedMemory
	hook  CartridgeHook

	// the binary interface for reading data returned by SharedMemory interface.
	// defaults to LittleEndian
	byteOrder binary.ByteOrder

	// the function that is called on every step of the cycle. can change
	// depending on the architecture
	stepFunction stepFunction

	// state of the ARM. saveable and restorable
	state *ARMState

	// updating the preferences every time run() is executed can be slow
	// (because the preferences need to be synchronised between tasks). the
	// prefsPulse ticker slows the rate at which updatePrefs() is called
	prefsPulse *time.Ticker

	// whether to foce an error on illegal memory access. set from ARM.prefs at
	// the start of every arm.Run()
	abortOnMemoryFault bool

	// the speed at which the arm is running at and the required stretching for
	// access to flash memory. speed is in MHz. Access latency of Flash memory is
	// 50ns which is 20MHz. Rounding up, this means that the clklen (clk stretching
	// amount) is 4.
	//
	// "The pipelined nature of the ARM7TDMI-S processor bus interface means that
	// there is a distinction between clock cycles and bus cycles. CLKEN can be
	// used to stretch a bus cycle, so that it lasts for many clock cycles. The
	// CLKEN input extends the timing of bus cycles in increments of of complete
	// CLK cycles"
	//
	// Access speed of SRAM is 10ns which is fast enough not to require stretching.
	// MAM also requires no stretching.
	//
	// updated from prefs on every Run() invocation
	Clk         float32
	clklenFlash float32

	// collection of functionMap instances. indexed by programMemoryOffset to
	// retrieve a functionMap
	//
	// allocated in NewARM() and added to in checkProgramMemory() if an entry
	// does not exist
	executionCache map[uint32][]decodeFunction

	// only decode an instruction do not execute
	decodeOnly bool

	// interface to an optional disassembler
	disasm coprocessor.CartCoProcDisassembler

	// the summary of the most recent disassembly
	disasmSummary DisasmSummary

	// interface to an option development package
	dev coprocessor.CartCoProcDeveloper

	// whether cycle count or not. set from ARM.prefs at the start of every arm.Run()
	//
	// used to cut out code that is required only for cycle counting. See
	// Icycle, Scycle and Ncycle fields which are called so frequently we
	// forego checking the immediateMode flag each time and have preset a stub
	// function if required
	immediateMode bool

	// rather than call the cycle counting functions directly, we assign the
	// functions to these fields. in this way, we can use stubs when executing
	// in immediate mode (when cycle counting isn't necessary)
	//
	// other aspects of cycle counting are not expensive and can remain
	Icycle func()
	Scycle func(bus busAccess, addr uint32)
	Ncycle func(bus busAccess, addr uint32)

	// profiler for executed instructions. measures cycles counts
	profiler *coprocessor.CartCoProcProfiler

	// enable breakpoint checking
	breakpointsEnabled bool
}

// NewARM is the preferred method of initialisation for the ARM type.
func NewARM(mmap architecture.Map, prefs *preferences.ARMPreferences, mem SharedMemory, hook CartridgeHook) *ARM {
	arm := &ARM{
		prefs:          prefs,
		mmap:           mmap,
		mem:            mem,
		hook:           hook,
		byteOrder:      binary.LittleEndian,
		executionCache: make(map[uint32][]decodeFunction),

		// updated on every updatePrefs(). these are reasonable defaults
		Clk:         70.0,
		clklenFlash: 4.0,

		state: &ARMState{},
	}

	// disassembly printed to stdout
	if disassembleToStdout {
		arm.disasm = &coprocessor.CartCoProcDisassemblerStdout{}
	}

	// slow prefs update by 100ms
	arm.prefsPulse = time.NewTicker(time.Millisecond * 100)

	switch arm.mmap.ARMArchitecture {
	case architecture.ARM7TDMI:
		arm.stepFunction = arm.stepARM7TDMI
	case architecture.ARMv7_M:
		arm.stepFunction = arm.stepARM7_M
	default:
		panic(fmt.Sprintf("unhandled ARM architecture: cannot set %s", arm.mmap.ARMArchitecture))
	}

	arm.state.mam = newMam(arm.prefs, arm.mmap)
	arm.state.rng = peripherals.NewRNG(arm.mmap)
	arm.state.timer = peripherals.NewTimer(arm.mmap)
	arm.state.timer2 = peripherals.NewTimer2(arm.mmap)

	// by definition the ARM starts in a program ended state
	arm.state.yield.Type = coprocessor.YieldProgramEnded

	// clklen for flash based on flash latency setting
	latencyInMhz := (1 / (arm.mmap.FlashLatency / 1000000000)) / 1000000
	arm.clklenFlash = float32(math.Ceil(float64(arm.Clk) / latencyInMhz))

	arm.resetPeripherals()
	arm.resetRegisters()
	arm.updatePrefs()

	return arm
}

// SetByteOrder changes the binary interface used to read memory returned by the
// SharedMemory interface
func (arm *ARM) SetByteOrder(o binary.ByteOrder) {
	arm.byteOrder = o
}

// ProcessorID implements the coprocessor.CartCoProc interface. Names the type
// of ARM being emulated
func (arm *ARM) ProcessorID() string {
	return string(arm.mmap.ARMArchitecture)
}

// ImmediateMode returns whether the most recent execution was in immediate mode
// or not.
func (arm *ARM) ImmediateMode() bool {
	return arm.immediateMode
}

// SetDisassembler implements the coprocessor.CartCoProc interface.
func (arm *ARM) SetDisassembler(disasm coprocessor.CartCoProcDisassembler) {
	arm.disasm = disasm
}

// SetDeveloper implements the coprocessor.CartCoProc interface.
func (arm *ARM) SetDeveloper(dev coprocessor.CartCoProcDeveloper) {
	arm.dev = dev
}

// Snapshort makes a copy of the ARM state.
func (arm *ARM) Snapshot() *ARMState {
	return arm.state.Snapshot()
}

// Plumb should be used to update the shared memory reference.
// Useful when used in conjunction with the rewind system.
//
// The ARMState argument can be nil as a special case. If it is nil then the
// existing state does not change. For some cartridge mappers this is acceptable
// and more convenient
func (arm *ARM) Plumb(state *ARMState, mem SharedMemory, hook CartridgeHook) {
	arm.mem = mem
	arm.hook = hook

	// always clear caches on a plumb event
	arm.ClearCaches()

	if state != nil {
		arm.state = state

		// if we're plumbing in a new state then we *must* reevaluate the
		// pointer the program memory
		arm.checkProgramMemory(true)
	}
}

// ClearCaches should be used very rarely. It empties the instruction and
// disassembly caches.
func (arm *ARM) ClearCaches() {
	arm.executionCache = make(map[uint32][]decodeFunction)
}

// resetPeripherals in the ARM package.
func (arm *ARM) resetPeripherals() {
	if arm.mmap.HasRNG {
		arm.state.rng.Reset()
	}
	if arm.mmap.HasTIMER {
		arm.state.timer.Reset()
	}
	if arm.mmap.HasTIM2 {
		arm.state.timer2.Reset()
	}
}

// resetRegisters of ARM. does not reset peripherals.
func (arm *ARM) resetRegisters() {
	arm.state.status.reset()

	for i := 0; i < rSP; i++ {
		arm.state.registers[i] = 0x00000000
	}

	preResetPC := arm.state.registers[rPC]
	arm.state.registers[rSP], arm.state.registers[rLR], arm.state.registers[rPC] = arm.mem.ResetVectors()
	arm.state.stackFrame = arm.state.registers[rSP]

	// set executingPC to be two behind the current value in the PC register
	arm.state.executingPC = arm.state.registers[rPC] - 2

	// if the PC value has changed then the reset procedure is treated like a branch
	arm.state.branchedExecution = preResetPC != arm.state.registers[rPC]

	// reset prefectch cycle value
	arm.state.prefetchCycle = S
}

// updatePrefs should be called periodically to ensure that the current
// preference values are being used in the ARM emulation. see also the
// prefsPulse ticker
func (arm *ARM) updatePrefs() {
	// update clock value from preferences
	arm.Clk = float32(arm.prefs.Clock.Get().(float64))

	arm.state.mam.updatePrefs()

	// set cycle counting functions
	arm.immediateMode = arm.prefs.Immediate.Get().(bool)
	if arm.immediateMode {
		arm.Icycle = arm.iCycleStub
		arm.Scycle = arm.sCycleStub
		arm.Ncycle = arm.nCycleStub
	} else {
		arm.Icycle = arm.iCycle
		arm.Scycle = arm.sCycle
		arm.Ncycle = arm.nCycle
	}

	// memory fault handling
	arm.abortOnMemoryFault = arm.prefs.AbortOnMemoryFault.Get().(bool)
}

func (arm *ARM) String() string {
	s := strings.Builder{}
	for i, r := range arm.state.registers {
		if i > 0 {
			if i%4 == 0 {
				s.WriteString("\n")
			} else {
				s.WriteString("\t\t")
			}
		}
		s.WriteString(fmt.Sprintf("R%-2d: %08x", i, r))
	}
	return s.String()
}

// Step moves the ARM on one cycle. Currently, the timer will only step forward
// when Step() is called and not during the Run() process. This might cause
// problems in some instances with some ARM programs.
func (arm *ARM) Step(vcsClock float32) {
	// the ARM timer ticks forward once every ARM cycle. the best we can do to
	// accommodate this is to tick the counter forward by the the appropriate
	// fraction every VCS cycle. Put another way: an NTSC spec VCS, for
	// example, will tick forward every 58-59 ARM cycles.
	arm.clock(arm.Clk / vcsClock)
}

func (arm *ARM) clock(cycles float32) {
	// incoming clock for TIM2 is half the frequency of the processor
	cycles *= arm.mmap.ClkDiv

	// add accumulated cycles (ClkDiv has already been applied to this
	// additional value)
	cycles += arm.state.accumulatedClocks

	// isolate integer and fractional part and save fraction for next clock()
	c := uint32(cycles)
	arm.state.accumulatedClocks = cycles - float32(c)

	if arm.mmap.HasTIMER {
		arm.state.timer.Step(c)
	}
	if arm.mmap.HasTIM2 {
		arm.state.timer2.Step(c)
	}
}

func (arm *ARM) logYield() {
	if arm.state.yield.Type.Normal() {
		return
	}
	logger.Logf(fmt.Sprintf("ARM7: %s", arm.state.yield.Type.String()), arm.state.yield.Error.Error())
	for _, d := range arm.state.yield.Detail {
		logger.Logf(fmt.Sprintf("ARM7: %s", arm.state.yield.Type.String()), d.Error())
	}
}

func (arm *ARM) extendedMemoryErrorLogging(memIdx int) {
	// add extended memory logging to yield detail
	if arm.prefs.ExtendedMemoryFaultLogging.Get().(bool) {
		df := arm.state.currentExecutionCache[memIdx]
		if df == nil {
			arm.state.yield.Detail = append(arm.state.yield.Detail, fmt.Errorf("no instruction cached for this memory address"))
		} else {
			entry := *df()
			arm.state.yield.Detail = append(arm.state.yield.Detail,
				errors.New(entry.String()),
				errors.New(arm.disasmVerbose(entry)),
			)
		}
	}
}

// SetInitialRegisters is intended to be called after creation but before the
// first call to Run().
//
// The optional arguments are used to initialise the registers in order
// starting with R0. The remaining options will be set to their default values
// (SP, LR and PC set according to the ResetVectors() via the SharedMemory
// interface).
//
// Note that you don't need to use this to set the initial values for SP, LR or
// PC. Those registers are initialised via the ResetVectors() function of the
// SharedMemory interface. The function will return with an error if those
// registers are attempted to be initialised.
//
// The emulated ARM will be left with a yield state of YieldSyncWithVCS
func (arm *ARM) SetInitialRegisters(args ...uint32) error {
	arm.resetRegisters()

	if len(args) >= rSP {
		return fmt.Errorf("ARM7: trying to set registers SP, LR or PC")
	}

	for i := range args {
		arm.state.registers[i] = args[i]
	}

	// fill the pipeline before yielding. this ensures that the PC is
	// correct on the first call to Run()
	arm.state.registers[rPC] += 2

	// making sure that yield is not of type YieldProgramEnded. that would cause
	// the registers to be immediately reset on the next call to Run()
	arm.state.yield.Type = coprocessor.YieldSyncWithVCS

	return nil
}

// StartProfiling starts a profiling session
func (arm *ARM) StartProfiling() {
	if arm.dev != nil {
		arm.dev.StartProfiling()
	}
}

// ProcessProfiling ends a profiling session
func (arm *ARM) ProcessProfiling() {
	if arm.dev != nil {
		arm.dev.ProcessProfiling()
	}
}

// Run will execute an ARM program from the current PC address, unless the
// previous execution ran to completion (ie. was uninterrupted).
//
// Returns the yield reason, the number of ARM cycles consumed.
func (arm *ARM) Run() (coprocessor.CoProcYield, float32) {
	if arm.dev != nil {
		defer func() {
			// make sure TIM2 is up to date
			if arm.mmap.HasTIM2 {
				arm.state.timer2.ResolveDeferredCycles()
			}

			// breakpoints handle OnYield slightly differently
			if arm.state.yield.Type != coprocessor.YieldBreakpoint {
				arm.logYield()
				arm.dev.OnYield(arm.state.registers[rPC], arm.state.yield)
			}
		}()
	}

	// only reset registers if the previous yield was one that indicated the end
	// of the program execution
	if arm.state.yield.Type == coprocessor.YieldProgramEnded {
		arm.resetRegisters()
	}

	// reset cycles count
	arm.state.cyclesTotal = 0

	// arm.state.prefetchCycle reset in reset() function. we don't want to change
	// the value if we're resuming from a yield

	// fill pipeline cannot happen immediately after resetRegisters()
	if arm.state.yield.Type == coprocessor.YieldProgramEnded {
		arm.state.registers[rPC] += 2
	}

	// reset disassembly as approprite for the previous yield type
	if arm.disasm != nil {
		// start of program execution
		arm.disasmSummary.I = 0
		arm.disasmSummary.N = 0
		arm.disasmSummary.S = 0
		if arm.state.yield.Type.Normal() {
			arm.disasm.Start()
		}

		defer func() {
			// wrapping disasmEnd because we don't want to capture disasmSummary
			// too early (because the deferred func() is invoked as part of the
			// declaration any arguments to the function will be captured at
			// that point. wrapping the call to disasm.End() prevents
			// disasmSummary being captured)
			arm.disasm.End(arm.disasmSummary)
		}()
	}

	// get developer information. this probably hasn't changed since ARM
	// creation but you never know
	if arm.dev != nil {
		arm.profiler = arm.dev.Profiling()
		arm.state.variableMemtop = arm.dev.HighAddress()
	}
	arm.state.protectVariableMemTop = arm.dev != nil

	// reset yield. we do this as late as possible because we want to use
	// information about the previous yield during the above preparations
	arm.state.yield.Type = coprocessor.YieldRunning
	arm.state.yield.Error = nil
	arm.state.yield.Detail = arm.state.yield.Detail[:0]

	// make sure program memory is correct
	arm.checkProgramMemory(false)
	if arm.state.yield.Type != coprocessor.YieldRunning {
		return arm.state.yield, 0
	}

	return arm.run()
}

// Interrupt indicates that the ARM execution should cease after the current
// instruction has been executed. The ARM will then yield with the reson
// YieldSyncWithVCS.
func (arm *ARM) Interrupt() {
	arm.state.yield.Type = coprocessor.YieldSyncWithVCS
}

// StackFrame implements the coprocess.CartCoProc interface
func (arm *ARM) StackFrame() uint32 {
	return arm.state.stackFrame
}

// BreakpointsEnable implements the coprocessor.CartCoProc interface. Enables
// breakpoint checking for the duration that disable is true.
func (arm *ARM) BreakpointsEnable(enable bool) {
	arm.breakpointsEnabled = enable
}

func (arm *ARM) checkProgramMemory(force bool) {
	// the address to use for program memory lookup
	//
	// the plus one to the executingPC value is intended to make sure that we're
	// not jumping to the very last byte of a memory block, if we did then 16bit
	// instruction lookup would fail
	//
	// important: some implementations of the SharedMemory interface will be
	// sensitive to the address value used with MapAddress(). therefore, how the
	// addr value is determined should never change - it may work with some
	// mappers but will fail with others
	addr := arm.state.executingPC + 1

	if !force {
		if arm.state.programMemory != nil &&
			addr >= arm.state.programMemoryOrigin && addr <= arm.state.programMemoryMemtop {
			return
		}
	}

	var origin uint32
	arm.state.programMemory, origin = arm.mem.MapAddress(addr, false)
	if arm.state.programMemory == nil {
		arm.state.yield.Type = coprocessor.YieldMemoryAccessError
		arm.state.yield.Error = fmt.Errorf("can't find program memory (PC %08x)", addr)
		return
	}

	if !arm.mem.IsExecutable(addr) {
		arm.state.programMemory = nil
		arm.state.yield.Type = coprocessor.YieldMemoryAccessError
		arm.state.yield.Error = fmt.Errorf("program memory is not executable (PC %08x)", addr)
		return
	}

	arm.state.programMemoryOrigin = origin
	arm.state.programMemoryMemtop = origin + uint32(len(*arm.state.programMemory)) - 1

	if m, ok := arm.executionCache[arm.state.programMemoryOrigin]; ok {
		arm.state.currentExecutionCache = m
	} else {
		arm.executionCache[arm.state.programMemoryOrigin] = make([]decodeFunction, len(*arm.state.programMemory))
		arm.state.currentExecutionCache = arm.executionCache[arm.state.programMemoryOrigin]
	}

	arm.stackProtectCheckProgramMemory()
}

func (arm *ARM) run() (coprocessor.CoProcYield, float32) {
	select {
	case <-arm.prefsPulse.C:
		arm.updatePrefs()
	default:
	}

	// number of iterations. only used when in immediate mode
	var iterations int

	// loop through instructions until we reach an exit condition
	for arm.state.yield.Type == coprocessor.YieldRunning {
		// program counter to execute:
		//
		// from "7.6 Data Operations" in "ARM7TDMI-S Technical Reference Manual r4p1", page 1-2
		//
		// "The program counter points to the instruction being fetched rather than to the instruction
		// being executed. This is important because it means that the Program Counter (PC)
		// value used in an executing instruction is always two instructions ahead of the address."
		arm.state.executingPC = arm.state.registers[rPC] - 2

		arm.checkBreakpoints()
		if arm.state.yield.Type == coprocessor.YieldRunning {

			// check program memory and continue if it's fine
			arm.checkProgramMemory(false)
			if arm.state.yield.Type == coprocessor.YieldRunning {
				memIdx := int(arm.state.executingPC - arm.state.programMemoryOrigin)

				// opcode for executed instruction
				opcode := arm.byteOrder.Uint16((*arm.state.programMemory)[memIdx:])

				// bump PC counter for prefetch. actual prefetch is done after execution
				arm.state.registers[rPC] += 2

				// expectedPC is used to decide whether to add cycles due to pipeline filling
				expectedPC := arm.state.registers[rPC]

				// expectedLR is used to change the stack frame information
				expectedLR := arm.state.registers[rLR]

				// expectedSP is used to decide whether to check the stack pointer
				// for collision with other memory errors
				expectedSP := arm.state.registers[rSP]

				// execute instruction
				arm.stepFunction(opcode, memIdx)

				// if program counter is not what we expect then that means we have hit a branch
				arm.state.branchedExecution = expectedPC != arm.state.registers[rPC]

				// if arm.state.branchedExecution && arm.state.function32bitDecoding {
				// 	panic("ARM7: impossible condition")
				// }

				if !arm.immediateMode {
					// add additional cycles required to fill pipeline before next iteration
					if arm.state.branchedExecution {
						arm.fillPipeline()
					}

					// prefetch cycle for next instruction is associated with and counts
					// towards the total of the current instruction. most prefetch cycles
					// are S cycles but store instructions require an N cycle
					if arm.state.prefetchCycle == N {
						arm.Ncycle(prefetch, arm.state.registers[rPC])
					} else {
						arm.Scycle(prefetch, arm.state.registers[rPC])
					}

					// default to an S cycle for prefetch unless an instruction explicitly
					// says otherwise
					arm.state.prefetchCycle = S

					// increases total number of program cycles by the stretched cycles for this instruction
					arm.state.cyclesTotal += arm.state.stretchedCycles

					// update clock
					arm.clock(arm.state.stretchedCycles)
				} else {
					// update clock with nominal number of cycles
					arm.clock(1.1)
				}

				// stack frame has changed if LR register has changed
				if expectedLR != arm.state.registers[rLR] {
					arm.state.stackFrame = expectedSP
				}

				// disassemble if appropriate
				if arm.disasm != nil {
					if !arm.state.instruction32bitDecoding {
						df := arm.state.currentExecutionCache[memIdx]
						if df != nil {
							arm.decodeOnly = true
							e := df()
							arm.decodeOnly = false
							if e != nil {
								arm.completeDisasmEntry(e, opcode, true)

								// update disasm summary
								arm.disasmSummary.ImmediateMode = arm.immediateMode
								arm.disasmSummary.add(arm.state.cycleOrder)

								// executed the Step() function of the attached disassembler
								arm.disasm.Step(*e)

								// print additional information output for stdout
								if _, ok := arm.disasm.(*coprocessor.CartCoProcDisassemblerStdout); ok {
									fmt.Println(arm.disasmVerbose(*e))
								}
							}
						}
					}
				}

				// accumulate cycle counts for profiling
				if arm.profiler != nil {
					arm.profiler.Entries = append(arm.profiler.Entries, coprocessor.CartCoProcProfileEntry{
						Addr:   arm.state.instructionPC,
						Cycles: arm.state.stretchedCycles,
					})
				}

				// reset cycle information
				if !arm.immediateMode {
					arm.state.branchTrail = BranchTrailNotUsed
					arm.state.mergedIS = false
					arm.state.stretchedCycles = 0

					// reset cycle order if we're not currently decoding a 32bit
					// instruction
					if !arm.state.instruction32bitDecoding {
						arm.state.cycleOrder.reset()
					}

					// limit the number of cycles used by the ARM program
					if arm.state.cyclesTotal >= cycleLimit {
						logger.Logf("ARM7", "reached cycle limit of %d", cycleLimit)
						panic("cycle limit")
					}
				} else {
					iterations++
					if iterations > instructionsLimit {
						logger.Logf("ARM7", "reached instructions limit of %d", instructionsLimit)
						panic("instruction limit")
					}
				}

				// check for stack errors
				if arm.state.yield.Type == coprocessor.YieldStackError {
					arm.extendedMemoryErrorLogging(memIdx)
					if !arm.abortOnMemoryFault {
						arm.logYield()
						arm.state.yield.Type = coprocessor.YieldRunning
						arm.state.yield.Error = nil
						arm.state.yield.Detail = arm.state.yield.Detail[:0]
					}
				} else {
					if !arm.state.yield.Type.Normal() {
						if arm.state.registers[rSP] != expectedSP {
							arm.stackProtectCheckSP()
							if arm.state.yield.Type == coprocessor.YieldStackError {
								arm.extendedMemoryErrorLogging(memIdx)
								if !arm.abortOnMemoryFault {
									arm.logYield()
									arm.state.yield.Type = coprocessor.YieldRunning
									arm.state.yield.Error = nil
									arm.state.yield.Detail = arm.state.yield.Detail[:0]
								}
							}
						}
					}
				}

				// handle memory access yields. we don't these want these to bleed out
				// of the ARM unless the abort preference is set
				if arm.state.yield.Type == coprocessor.YieldMemoryAccessError {
					// add extended memory logging to yield detail
					arm.extendedMemoryErrorLogging(memIdx)

					// if illegal memory accesses are to be ignored then we must log the
					// yield information now before reset the yield type
					if !arm.abortOnMemoryFault {
						arm.logYield()
						arm.state.yield.Type = coprocessor.YieldRunning
						arm.state.yield.Error = nil
						arm.state.yield.Detail = arm.state.yield.Detail[:0]
					}
				}
			} // check program memory
		} // check breakpoint
	}

	return arm.state.yield, arm.state.cyclesTotal
}

func (arm *ARM) checkBreakpoints() {
	// check breakpoints unless they are disabled. we also don't want to match
	// if we're in the middle of decoding a 32bit instruction
	if arm.dev != nil && arm.breakpointsEnabled && !arm.state.instruction32bitDecoding {
		var addr uint32

		if arm.state.branchedExecution {
			addr = arm.state.registers[rPC] - 2
		} else {
			addr = arm.state.executingPC
		}

		if arm.dev.CheckBreakpoint(addr) {
			arm.state.yield.Type = coprocessor.YieldBreakpoint
			arm.state.yield.Error = fmt.Errorf("%08x", addr)

			// we call OnYield here with the address you used to check for the
			// breakpoint not sure if this is correct or whether we should
			// simply call OnYield() in the normal way (at the end of the Run()
			// function)
			arm.dev.OnYield(addr, arm.state.yield)
		}
	}
}

func (arm *ARM) stepARM7TDMI(opcode uint16, memIdx int) {
	df := arm.state.currentExecutionCache[memIdx]
	if df == nil {
		df = arm.decodeThumb(opcode)
		arm.state.currentExecutionCache[memIdx] = df
	}

	// while the ARM7TDMI/Thumb instruction doesn't have 32bit instructions, in
	// practice the BL instruction can/should be treated like a 32bit instruction
	// for disassembly purposes
	if arm.state.instruction32bitDecoding {
		arm.state.instruction32bitResolving = true
		arm.state.instruction32bitDecoding = false
	} else {
		arm.state.instructionPC = arm.state.executingPC
		arm.state.instruction32bitResolving = false
		if is32BitThumb2(opcode) {
			arm.state.instruction32bitDecoding = true
			arm.state.instruction32bitOpcodeHi = opcode
		}
	}

	df()
}

func (arm *ARM) stepARM7_M(opcode uint16, memIdx int) {
	// decode function to execute
	var df decodeFunction

	// process a 32bit or 16bit instruction as appropriate
	if arm.state.instruction32bitDecoding {
		arm.state.instruction32bitDecoding = false
		arm.state.instruction32bitResolving = true
		df = arm.state.currentExecutionCache[memIdx]
		if df == nil {
			df = arm.decode32bitThumb2(arm.state.instruction32bitOpcodeHi, opcode)
			arm.state.currentExecutionCache[memIdx] = df
		}
	} else {
		// the opcode is either a 16bit instruction or the first halfword for a
		// 32bit instruction. either way we're not resolving a 32bit
		// instruction, by defintion
		arm.state.instruction32bitResolving = false
		arm.state.instruction32bitOpcodeHi = 0x0

		arm.state.instructionPC = arm.state.executingPC
		if is32BitThumb2(opcode) {
			arm.state.instruction32bitDecoding = true
			arm.state.instruction32bitOpcodeHi = opcode
		} else {
			df = arm.state.currentExecutionCache[memIdx]
			if df == nil {
				df = arm.decodeThumb2(opcode)
				arm.state.currentExecutionCache[memIdx] = df
			}
		}
	}

	// new 32bit functions always execute
	// if the opcode indicates that this is a 32bit thumb instruction
	// then we need to resolve that regardless of any IT block
	if arm.state.status.itMask != 0b0000 && !arm.state.instruction32bitDecoding {
		r, _ := arm.state.status.condition(arm.state.status.itCond)

		if r {
			if df != nil {
				df()
			}
		} else {
			// "A7.3.2: Conditional execution of undefined instructions
			//
			// If an undefined instruction fails a condition check in Armv7-M, the instruction
			// behaves as a NOP and does not cause an exception"
			//
			// page A7-179 of the "ARMv7-M Architecture Reference Manual"
		}

		// update IT conditions only if the opcode is not a 32bit opcode
		// update LSB of IT condition by copying the MSB of the IT mask
		arm.state.status.itCond &= 0b1110
		arm.state.status.itCond |= (arm.state.status.itMask >> 3)

		// shift IT mask
		arm.state.status.itMask = (arm.state.status.itMask << 1) & 0b1111
	} else if df != nil {
		df()
	}
}
