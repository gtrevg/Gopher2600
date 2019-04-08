package debugger

import (
	"fmt"
	"gopher2600/debugger/commandline"
	"gopher2600/debugger/console"
	"gopher2600/debugger/monitor"
	"gopher2600/disassembly"
	"gopher2600/errors"
	"gopher2600/gui"
	"gopher2600/hardware"
	"gopher2600/hardware/cpu/definitions"
	"gopher2600/hardware/cpu/result"
	"gopher2600/symbols"
	"os"
	"os/signal"
	"strings"
)

const defaultOnHalt = "CPU; TV"
const defaultOnStep = "LAST"

// Debugger is the basic debugging frontend for the emulation
type Debugger struct {
	vcs    *hardware.VCS
	disasm *disassembly.Disassembly
	gui    gui.GUI

	// whether the debugger is to continue with the debugging loop
	// set to false only when debugger is to finish
	running bool

	// continue emulation until a halt condition is encountered
	runUntilHalt bool

	// interface to the vcs memory with additional debugging functions
	// -- access to vcs memory from the debugger (eg. peeking and poking) is
	// most fruitfully performed through this structure
	dbgmem *memoryDebug

	// system monitor is a very low level mechanism for monitoring the state of
	// the cpu and of memory. it is checked every video cycle and interesting
	// changes noted and recorded.
	sysmon *monitor.SystemMonitor

	// halt conditions
	breakpoints *breakpoints
	traps       *traps
	watches     *watches

	// note that the UI probably allows the user to manually break or trap at
	// will, with for example, ctrl-c

	// we accumulate break, trap and watch messsages until we can service them
	// if the strings are empty then no break/trap/watch event has occurred
	breakMessages string
	trapMessages  string
	watchMessages string

	// any error from previous emulation step
	lastStepError bool

	// single-fire step traps. these are used for the STEP command, allowing
	// things like "STEP FRAME".
	stepTraps *traps

	// step command to use when input is empty
	defaultStepCommand string

	// commandOnHalt says whether an sequence of commands should run automatically
	// when emulation halts. commandOnHaltPrev is the stored command sequence
	// used when ONHALT is called with no arguments
	// halt is a breakpoint or user intervention (ie. ctrl-c)
	commandOnHalt       string
	commandOnHaltStored string

	// similarly, commandOnStep is the sequence of commands to run afer every
	// cpu/video cycle
	commandOnStep       string
	commandOnStepStored string

	// machineInfoVerbose controls the verbosity of commands that echo machine state
	machineInfoVerbose bool

	// input loop fields. we're storing these here because inputLoop can be
	// called from within another input loop (via a video step callback) and we
	// want these properties to persist (when a video step input loop has
	// completed and we're back into the main input loop)
	inputloopHalt bool // whether to halt the current execution loop
	inputloopNext bool // execute a step once user input has returned a result

	// granularity of single stepping - every cpu instruction or every video cycle
	// -- also affects when emulation will halt on breaks, traps and watches.
	// if inputeveryvideocycle is true then the halt may occur mid-cpu-cycle
	inputEveryVideoCycle bool

	// the last result from vcs.Step() - could be a complete result or an
	// intermediate result when video-stepping
	lastResult *result.Instruction

	// console interface
	console       console.UserInterface
	consoleSilent bool // controls whether UI is to remain silent

	// buffer for user input
	input []byte

	// channel for communicating with the debugger from the ctrl-c goroutine
	interruptChannel chan os.Signal

	// channel for communicating with the debugger from the gui goroutine
	guiChannel chan gui.Event

	// capture user input to a script file. no capture taking place if nil
	capture *captureScript
}

// NewDebugger creates and initialises everything required for a new debugging
// session. Use the Start() method to actually begin the session.
func NewDebugger(tv gui.GUI) (*Debugger, error) {
	var err error

	dbg := new(Debugger)

	dbg.console = new(console.PlainTerminal)

	// prepare hardware
	dbg.gui = tv
	dbg.gui.SetFeature(gui.ReqSetAllowDebugging, true)

	// create a new VCS instance
	dbg.vcs, err = hardware.NewVCS(dbg.gui)
	if err != nil {
		return nil, fmt.Errorf("error preparing VCS: %s", err)
	}

	// create instance of disassembly -- the same base structure is used
	// for disassemblies subseuquent to the first one.
	dbg.disasm = &disassembly.Disassembly{}

	// set up debugging interface to memory
	dbg.dbgmem = &memoryDebug{mem: dbg.vcs.Mem, symtable: &dbg.disasm.Symtable}

	// set up system monitor
	dbg.sysmon = &monitor.SystemMonitor{Mem: dbg.vcs.Mem, MC: dbg.vcs.MC, Rec: dbg.vcs.TV}

	// set up breakpoints/traps
	dbg.breakpoints = newBreakpoints(dbg)
	dbg.traps = newTraps(dbg)
	dbg.watches = newWatches(dbg)
	dbg.stepTraps = newTraps(dbg)
	dbg.defaultStepCommand = "STEP"

	// default ONHALT command sequence
	dbg.commandOnHaltStored = defaultOnHalt

	// default ONSTEP command sequnce
	dbg.commandOnStep = defaultOnStep
	dbg.commandOnStepStored = dbg.commandOnStep

	// allocate memory for user input
	dbg.input = make([]byte, 255)

	// make synchronisation channels
	dbg.interruptChannel = make(chan os.Signal, 2)
	dbg.guiChannel = make(chan gui.Event, 2)

	// connect debugger to gui
	dbg.gui.SetEventChannel(dbg.guiChannel)

	return dbg, nil
}

// Start the main debugger sequence
func (dbg *Debugger) Start(iface console.UserInterface, filename string, initScript string) error {
	// prepare user interface
	if iface != nil {
		dbg.console = iface
	}

	err := dbg.console.Initialise()
	if err != nil {
		return err
	}
	defer dbg.console.CleanUp()

	dbg.console.RegisterTabCompleter(commandline.NewTabCompletion(debuggerCommands))

	err = dbg.loadCartridge(filename)
	if err != nil {
		return err
	}

	dbg.running = true

	// create interrupt channel
	dbg.interruptChannel = make(chan os.Signal, 1)
	signal.Notify(dbg.interruptChannel, os.Interrupt)

	// run initialisation script
	if initScript != "" {
		spt, err := dbg.loadScript(initScript)
		if err != nil {
			dbg.print(console.Error, "error running debugger initialisation script: %s\n", err)
		}

		err = dbg.inputLoop(spt, false)
		if err != nil {
			return err
		}
	}

	// prepare and run main input loop. inputLoop will not return until
	// debugger is to exit
	err = dbg.inputLoop(dbg.console, false)
	if err != nil {
		return err
	}
	return nil
}

// loadCartridge makes sure that the cartridge loaded into vcs memory and the
// available disassembly/symbols are in sync.
//
// NEVER call vcs.AttachCartridge except through this funtion
//
// this is the glue that hold the cartridge and disassembly packages
// together
func (dbg *Debugger) loadCartridge(cartridgeFilename string) error {
	err := dbg.vcs.AttachCartridge(cartridgeFilename)
	if err != nil {
		return err
	}

	symtable, err := symbols.ReadSymbolsFile(cartridgeFilename)
	if err != nil {
		dbg.print(console.Error, "%s", err)
		symtable = symbols.StandardSymbolTable()
	}

	err = dbg.disasm.FromMemory(dbg.vcs.Mem.Cart, symtable)
	if err != nil {
		return err
	}

	err = dbg.vcs.TV.Reset()
	if err != nil {
		return err
	}

	return nil
}

// videoCycle() to be used with vcs.Step()
func (dbg *Debugger) videoCycle(result *result.Instruction) error {
	// note result as lastResult immediately
	dbg.lastResult = result

	// because we call this callback mid-instruction, the program counter
	// maybe in its non-final state - we don't want to break or trap in those
	// instances when the final effect of the instruction changes the program
	// counter to some other value (ie. a flow, subroutine or interrupt
	// instruction)
	if !result.Final && result.Defn != nil {
		if result.Defn.Effect == definitions.Flow || result.Defn.Effect == definitions.Subroutine || result.Defn.Effect == definitions.Interrupt {
			return nil
		}
	}

	dbg.breakMessages = dbg.breakpoints.check(dbg.breakMessages)
	dbg.trapMessages = dbg.traps.check(dbg.trapMessages)
	dbg.watchMessages = dbg.watches.check(dbg.watchMessages)

	return dbg.sysmon.Check()
}

// inputLoop has two modes, defined by the videoCycleInput argument.
// when videoCycleInput is true then user will be prompted every video cycle,
// as opposed to only every cpu instruction.
//
// inputter is an instance of type UserInput. this will usually be dbg.ui but
// it could equally be an instance of debuggingScript.
func (dbg *Debugger) inputLoop(inputter console.UserInput, videoCycleInput bool) error {
	var err error

	// videoCycleWithInput() to be used with vcs.Step() instead of videoCycle()
	// when in video-step mode
	videoCycleWithInput := func(result *result.Instruction) error {
		dbg.videoCycle(result)
		if dbg.commandOnStep != "" {
			_, err := dbg.parseInput(dbg.commandOnStep, false)
			if err != nil {
				dbg.print(console.Error, "%s", err)
			}
		}
		return dbg.inputLoop(inputter, true)
	}

	for {
		dbg.checkInterruptsAndEvents()
		if !dbg.running {
			break // for loop
		}

		// this extra test is to prevent the video input loop from continuing
		// if step granularity has been switched to every cpu instruction - the
		// input loop will unravel and execution will continue in the main
		// inputLoop
		if videoCycleInput && !dbg.inputEveryVideoCycle && dbg.inputloopNext {
			return nil
		}

		// check for step-traps
		stepTrapMessage := dbg.stepTraps.check("")
		if stepTrapMessage != "" {
			dbg.stepTraps.clear()
		}

		// check for breakpoints and traps
		dbg.breakMessages = dbg.breakpoints.check(dbg.breakMessages)
		dbg.trapMessages = dbg.traps.check(dbg.trapMessages)
		dbg.watchMessages = dbg.watches.check(dbg.watchMessages)

		// check for halt conditions
		dbg.inputloopHalt = stepTrapMessage != "" || dbg.breakMessages != "" || dbg.trapMessages != "" || dbg.watchMessages != "" || dbg.lastStepError

		// reset last step error
		dbg.lastStepError = false

		// if commandOnHalt is defined and if run state is correct then run
		// commandOnHalt command(s)
		if dbg.commandOnHalt != "" {
			if (dbg.inputloopNext && !dbg.runUntilHalt) || dbg.inputloopHalt {
				_, err = dbg.parseInput(dbg.commandOnHalt, false)
				if err != nil {
					dbg.print(console.Error, "%s", err)
				}
			}
		}

		// print and reset accumulated break and trap messages
		dbg.print(console.Feedback, dbg.breakMessages)
		dbg.print(console.Feedback, dbg.trapMessages)
		dbg.print(console.Feedback, dbg.watchMessages)
		dbg.breakMessages = ""
		dbg.trapMessages = ""
		dbg.watchMessages = ""

		// expand inputloopHalt to include step-once/many flag
		dbg.inputloopHalt = dbg.inputloopHalt || !dbg.runUntilHalt

		// enter halt state
		if dbg.inputloopHalt {
			// pause tv when emulation has halted
			err = dbg.gui.SetFeature(gui.ReqSetPause, true)
			if err != nil {
				return err
			}

			dbg.runUntilHalt = false

			// decide which address value to use
			var disasmAddress uint16
			var disasmBank int

			if dbg.lastResult == nil || dbg.lastResult.Final {
				disasmAddress = dbg.vcs.MC.PC.ToUint16()
			} else {
				// if we're in the middle of an instruction then use the
				// addresss in lastResult - in video-stepping mode we want the
				// prompt to report the instruction that we're working on, not
				// the next one to be stepped into.
				disasmAddress = dbg.lastResult.Address
			}
			disasmBank = dbg.vcs.Mem.Cart.Bank

			// build prompt
			var prompt string
			if r, ok := dbg.disasm.Get(disasmBank, disasmAddress); ok {
				prompt = strings.Trim(r.GetString(dbg.disasm.Symtable, result.StyleBrief), " ")
				prompt = fmt.Sprintf("[ %s ] > ", prompt)
			} else {
				// incomplete disassembly, prepare witchspace prompt
				prompt = fmt.Sprintf("[witchspace (%d, %#04x)] > ", disasmBank, disasmAddress)
			}

			// - additional annotation if we're not showing the prompt in the main loop
			if videoCycleInput && !dbg.lastResult.Final {
				prompt = fmt.Sprintf("+ %s", prompt)
			}

			// get user input
			n, err := inputter.UserRead(dbg.input, prompt, dbg.guiChannel, dbg.guiEventHandler)
			if err != nil {
				switch err := err.(type) {

				case errors.FormattedError:
					switch err.Errno {
					case errors.UserInterrupt:
						dbg.running = false
						fallthrough
					case errors.ScriptEnd:
						if !videoCycleInput {
							dbg.print(console.Feedback, err.Error())
						}
						return nil
					}
				}

				return err
			}

			dbg.checkInterruptsAndEvents()
			if !dbg.running {
				break // for loop
			}

			// parse user input
			dbg.inputloopNext, err = dbg.parseInput(string(dbg.input[:n-1]), inputter.IsInteractive())
			if err != nil {
				dbg.print(console.Error, "%s", err)
			}

			// prepare for next loop
			dbg.inputloopHalt = false

			// make sure tv is unpaused if emulation is about to resume
			if dbg.inputloopNext {
				err = dbg.gui.SetFeature(gui.ReqSetPause, false)
				if err != nil {
					return err
				}
			}
		}

		// move emulation on one step if user has requested/implied it
		if dbg.inputloopNext {
			if !videoCycleInput {
				if dbg.inputEveryVideoCycle {
					_, dbg.lastResult, err = dbg.vcs.Step(videoCycleWithInput)
				} else {
					_, dbg.lastResult, err = dbg.vcs.Step(dbg.videoCycle)
				}

				if err != nil {
					switch err := err.(type) {
					case errors.FormattedError:
						// do not exit input loop when error is a gopher error
						// set lastStepError instead and allow emulation to
						// halt
						dbg.lastStepError = true

						// print gopher error message
						dbg.print(console.Error, "%s", err)
					default:
						return err
					}
				} else {
					// check validity of instruction result
					if dbg.lastResult.Final {
						err := dbg.lastResult.IsValid()
						if err != nil {
							dbg.print(console.Error, "%s", dbg.lastResult.Defn)
							dbg.print(console.Error, "%s", dbg.lastResult)
							panic(err)
						}
					}
				}

				if dbg.commandOnStep != "" {
					_, err := dbg.parseInput(dbg.commandOnStep, false)
					if err != nil {
						dbg.print(console.Error, "%s", err)
					}
				}
			} else {
				return nil
			}
		}
	}

	return nil
}

// parseInput splits the input into individual commands. each command is then
// passed to parseCommand for final processing
//
// interactive argument should be true only for input that has immediately come from
// the user. only interactive input will be added to a capture file.
//
// returns "step" status - whether or not the input should cause the emulation
// to continue at least one step (a command in the input may have set the
// runUntilHalt flag)
func (dbg *Debugger) parseInput(input string, interactive bool) (bool, error) {
	var result parseCommandResult
	var err error
	var step bool

	// whether or not we should capture the input to a file
	captureInput := dbg.capture != nil && interactive

	// ignore comments
	if strings.HasPrefix(input, "#") {
		return false, nil
	}

	// divide input if necessary
	commands := strings.Split(input, ";")
	for i := 0; i < len(commands); i++ {
		// parse command. format of command[i] wil be normalised
		result, err = dbg.parseCommand(&commands[i])
		if err != nil {
			return false, err
		}

		// the result from parseCommand() tells us what to do next
		switch result {
		case doNothing:
			// most commands don't require us to do anything
			break
		case stepContinue:
			// emulation should continue to next step
			step = true
		case emptyInput:
			// input was empty. if this was an interactive input then try the
			// default step command
			if interactive {
				return dbg.parseInput(dbg.defaultStepCommand, interactive)
			}
			return false, nil
		case setDefaultStep:
			// command has reset what the default step command shoudl be
			dbg.defaultStepCommand = commands[i]
			step = true
		case captureStarted:
			// command has caused input capture to begin. we don't want to
			// record this command in the capture file
			captureInput = false
		case captureEnded:
			// command has caused input capture to end. we don't want to record
			// this command in the capture file
			captureInput = false
		}

		// record command in capture file if required
		if captureInput {
			dbg.capture.add(commands[i])
		}
	}

	return step, nil
}
