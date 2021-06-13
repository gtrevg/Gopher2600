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

package main

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/jetsetilly/gopher2600/cartridgeloader"
	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/debugger"
	"github.com/jetsetilly/gopher2600/debugger/terminal"
	"github.com/jetsetilly/gopher2600/debugger/terminal/colorterm"
	"github.com/jetsetilly/gopher2600/debugger/terminal/plainterm"
	"github.com/jetsetilly/gopher2600/disassembly"
	"github.com/jetsetilly/gopher2600/gui"
	"github.com/jetsetilly/gopher2600/gui/sdlimgui"
	"github.com/jetsetilly/gopher2600/hardware/television"
	"github.com/jetsetilly/gopher2600/hiscore"
	"github.com/jetsetilly/gopher2600/logger"
	"github.com/jetsetilly/gopher2600/modalflag"
	"github.com/jetsetilly/gopher2600/paths"
	"github.com/jetsetilly/gopher2600/performance"
	"github.com/jetsetilly/gopher2600/playmode"
	"github.com/jetsetilly/gopher2600/recorder"
	"github.com/jetsetilly/gopher2600/regression"
	"github.com/jetsetilly/gopher2600/statsview"
	"github.com/jetsetilly/gopher2600/wavwriter"
)

const defaultInitScript = "debuggerInit"

// communication between the main goroutine and the launch goroutine.
type mainSync struct {
	state chan stateRequest

	// a created GUI will communicate throught these channels
	gui      chan guiControl
	guiError chan error
}

// the stateRequest sent through the state channel in mainSync
type stateReq string

// list of valid stateReq values
const (
	// main thread should end as soon as possible.
	//
	// takes optional int argument, indicating the status code.
	reqQuit stateReq = "QUIT"

	// reset interrupt signal handling. used when an alternative
	// handler is more appropriate. for example, the playMode and Debugger
	// package provide a mode specific handler.
	//
	// takes no arguments.
	reqNoIntSig stateReq = "NOINTSIG"

	// the gui creation function to run in the main goroutine. this is for GUIs
	// that *need* to be run in the main OS thead (SDL, etc.)
	//
	// the only argument must be a guiCreate reference
	reqCreateGUI stateReq = "CREATEGUI"
)

type stateRequest struct {
	req  stateReq
	args interface{}
}

// the gui create function. paired with reqCreateGUI state request
type guiCreate func() (guiControl, error)

// guiControl defines the functions that a guiControl implementation must implement to be
// usable from the main goroutine.
type guiControl interface {
	// cleanup resources used by the gui
	Destroy(io.Writer)

	// Service() should not pause or loop longer than necessary (if at all). It
	// MUST ONLY by called as part of a larger loop from the main thread. It
	// should service all gui events that are not safe to do in sub-threads.
	//
	// If the GUI framework does not require this sort of thread safety then
	// there is no need for the Service() function to do anything.
	Service()
}

func main() {
	sync := &mainSync{
		state:    make(chan stateRequest),
		gui:      make(chan guiControl),
		guiError: make(chan error),
	}

	// the value to use with os.Exit(). can be changed with reqQuit
	// stateRequest
	exitVal := 0

	// #ctrlc default handler. can be turned off with reqNoIntSig request
	intChan := make(chan os.Signal, 1)
	signal.Notify(intChan, os.Interrupt)

	// launch program as a go routine. further communication is  through
	// the mainSync instance
	go launch(sync)

	// loop until done is true. every iteration of the loop we listen for:
	//
	//  1. interrupt signals
	//  2. new gui creation functions
	//  3. state requests
	//  3. anything in the Service() function of the most recently created GUI
	//
	done := false
	var gui guiControl
	for !done {
		select {
		case <-intChan:
			fmt.Println("\r")
			done = true

		case state := <-sync.state:
			switch state.req {
			case reqQuit:
				done = true
				if gui != nil {
					gui.Destroy(os.Stderr)
				}

				if state.args != nil {
					if v, ok := state.args.(int); ok {
						exitVal = v
					} else {
						panic(fmt.Sprintf("cannot convert %s arguments into int", reqQuit))
					}
				}

			case reqNoIntSig:
				signal.Reset(os.Interrupt)
				if state.args != nil {
					panic(fmt.Sprintf("%s does not accept any arguments", reqNoIntSig))
				}

			case reqCreateGUI:
				var err error

				// destroy existing gui
				if gui != nil {
					gui.Destroy(os.Stderr)
				}

				gui, err = state.args.(guiCreate)()
				if err != nil {
					sync.guiError <- err

					// gui is a variable of type interface. nil doesn't work as you
					// might expect with interfaces. for instance, even though the
					// following outputs "<nil>":
					//
					//	fmt.Println(gui)
					//
					// the following equation print false:
					//
					//	fmt.Println(gui == nil)
					//
					// as to the reason why gui does not equal nil, even though
					// the creator() function returns nil? well, you tell me.
					gui = nil
				} else {
					sync.gui <- gui
				}

			}

		default:
			// if an instance of gui.Events has been sent to us via sync.events
			// then call Service()
			if gui != nil {
				gui.Service()
			}
		}
	}

	fmt.Print("\r")
	os.Exit(exitVal)
}

// launch is called from main() as a goroutine. uses mainSync instance to
// indicate gui creation and to quit.
func launch(sync *mainSync) {
	// we generate random numbers in some places. seed the generator with the
	// current time
	rand.Seed(int64(time.Now().Nanosecond()))

	md := &modalflag.Modes{Output: os.Stdout}
	md.NewArgs(os.Args[1:])
	md.NewMode()
	md.AddSubModes("RUN", "PLAY", "DEBUG", "DISASM", "PERFORMANCE", "REGRESS", "HISCORE")

	p, err := md.Parse()
	switch p {
	case modalflag.ParseHelp:
		sync.state <- stateRequest{req: reqQuit}
		return

	case modalflag.ParseError:
		fmt.Printf("* error: %v\n", err)
		// 10
		sync.state <- stateRequest{req: reqQuit, args: 10}
		return
	}

	switch md.Mode() {
	case "RUN":
		fallthrough

	case "PLAY":
		err = play(md, sync)

	case "DEBUG":
		err = debug(md, sync)

	case "DISASM":
		err = disasm(md)

	case "PERFORMANCE":
		err = perform(md, sync)

	case "REGRESS":
		err = regress(md, sync)

	case "HISCORE":
		err = hiscoreServer(md)
	}

	if err != nil {
		fmt.Printf("* error in %s mode: %s\n", md.String(), err)
		sync.state <- stateRequest{req: reqQuit, args: 20}
		return
	}

	sync.state <- stateRequest{req: reqQuit}
}

func play(md *modalflag.Modes, sync *mainSync) error {
	md.NewMode()

	mapping := md.AddString("mapping", "AUTO", "force use of cartridge mapping")
	spec := md.AddString("tv", "AUTO", "television specification: NTSC, PAL, PAL60")
	fullScreen := md.AddBool("fullscreen", false, "start in fullscreen mode")
	fpsCap := md.AddBool("fpscap", true, "cap fps to specification")
	record := md.AddBool("record", false, "record user input to a file")
	wav := md.AddString("wav", "", "record audio to wav file")
	patchFile := md.AddString("patch", "", "patch file to apply (cartridge args only)")
	hiscore := md.AddBool("hiscore", false, "contact hiscore server [EXPERIMENTAL]")
	log := md.AddBool("log", false, "echo debugging log to stdout")
	useSavekey := md.AddBool("savekey", false, "use savekey in player 1 port")
	profile := md.AddString("profile", "none", "run performance check with profiling: command separated CPU, MEM, TRACE or ALL")

	stats := &[]bool{false}[0]
	if statsview.Available() {
		stats = md.AddBool("statsview", false, fmt.Sprintf("run stats server (%s)", statsview.Address))
	}

	p, err := md.Parse()
	if err != nil || p != modalflag.ParseContinue {
		return err
	}

	// set debugging log echo
	if *log {
		logger.SetEcho(os.Stdout)
	} else {
		logger.SetEcho(nil)
	}

	if *stats {
		statsview.Launch(os.Stdout)
	}

	switch len(md.RemainingArgs()) {
	case 0:
		return fmt.Errorf("2600 cartridge required for %s mode", md)
	case 1:
		cartload := cartridgeloader.NewLoader(md.GetArg(0), *mapping)
		defer cartload.Close()

		tv, err := television.NewTelevision(*spec)
		if err != nil {
			return err
		}
		defer tv.End()

		// add wavwriter mixer if wav argument has been specified
		if *wav != "" {
			aw, err := wavwriter.New(*wav)
			if err != nil {
				return err
			}
			tv.AddAudioMixer(aw)
		}

		// create gui
		sync.state <- stateRequest{req: reqCreateGUI,
			args: guiCreate(func() (guiControl, error) {
				return sdlimgui.NewSdlImgui(tv)
			}),
		}

		// wait for creator result
		var scr gui.GUI
		select {
		case g := <-sync.gui:
			scr = g.(gui.GUI)

		case err := <-sync.guiError:
			return err
		}

		// set fps cap
		tv.SetFPSCap(*fpsCap)
		scr.SetFeature(gui.ReqVSync, *fpsCap)

		// set full screen
		scr.SetFeature(gui.ReqFullScreen, *fullScreen)

		// turn off fallback ctrl-c handling. this so that the playmode can
		// end playback recordings gracefully
		sync.state <- stateRequest{req: reqNoIntSig}

		// check for profiling options
		p, err := performance.ParseProfileString(*profile)
		if err != nil {
			return err
		}

		// set up a running function
		playLaunch := func() error {
			err = playmode.Play(tv, scr, *record, cartload, *patchFile, *hiscore, *useSavekey)
			if err != nil {
				return err
			}
			return nil
		}

		if p == performance.ProfileNone {
			err = playLaunch()
			if err != nil {
				return err
			}
		} else {
			// if profile generation has been requested then pass the
			// playLaunch() function prepared above, through the RunProfiler()
			// function
			err := performance.RunProfiler(p, "play", playLaunch)
			if err != nil {
				return err
			}
		}

		if *record {
			fmt.Println("! recording completed")
		}

		// set ending state
		err = scr.SetFeature(gui.ReqState, gui.StateEnding)
		if err != nil {
			return err
		}

	default:
		return fmt.Errorf("too many arguments for %s mode", md)
	}

	return nil
}

func debug(md *modalflag.Modes, sync *mainSync) error {
	md.NewMode()

	defInitScript, err := paths.ResourcePath("", defaultInitScript)
	if err != nil {
		return err
	}

	mapping := md.AddString("mapping", "AUTO", "force use of cartridge mapping")
	spec := md.AddString("tv", "AUTO", "television specification: NTSC, PAL, PAL60")
	termType := md.AddString("term", "IMGUI", "terminal type to use in debug mode: IMGUI, COLOR, PLAIN")
	initScript := md.AddString("initscript", defInitScript, "script to run on debugger start")
	useSavekey := md.AddBool("savekey", false, "use savekey in player 1 port")
	profile := md.AddString("profile", "none", "run performance check with profiling: command separated CPU, MEM, TRACE or ALL")

	stats := &[]bool{false}[0]
	if statsview.Available() {
		stats = md.AddBool("statsview", false, fmt.Sprintf("run stats server (%s)", statsview.Address))
	}

	p, err := md.Parse()
	if err != nil || p != modalflag.ParseContinue {
		return err
	}

	if *stats {
		statsview.Launch(os.Stdout)
	}

	// cartridge loader. note that there is no deferred cartload.Close(). the
	// debugger type itself will handle this.
	cartload := cartridgeloader.NewLoader(md.GetArg(0), *mapping)

	tv, err := television.NewTelevision(*spec)
	if err != nil {
		return err
	}
	defer tv.End()

	var term terminal.Terminal
	var scr gui.GUI

	// create gui
	if *termType == "IMGUI" {
		sync.state <- stateRequest{req: reqCreateGUI,
			args: guiCreate(func() (guiControl, error) {
				return sdlimgui.NewSdlImgui(tv)
			}),
		}

		// wait for creator result
		select {
		case g := <-sync.gui:
			scr = g.(gui.GUI)
		case err := <-sync.guiError:
			return err
		}

		// if gui implements the terminal.Broker interface use that terminal
		// as a preference
		if b, ok := scr.(terminal.Broker); ok {
			term = b.GetTerminal()
		}
	} else {
		scr = gui.Stub{}
	}

	// if the GUI does not supply a terminal then use a color or plain terminal
	// as a fallback
	if term == nil {
		switch strings.ToUpper(*termType) {
		default:
			fmt.Printf("! unknown terminal type (%s) defaulting to plain\n", *termType)
			fallthrough
		case "PLAIN":
			term = &plainterm.PlainTerminal{}
		case "COLOR":
			term = &colorterm.ColorTerminal{}
		}
	}

	// turn off fallback ctrl-c handling. this so that the debugger can handle
	// quit events with a confirmation request. it also allows the debugger to
	// use ctrl-c events to interrupt execution of the emulation without
	// quitting the debugger itself
	sync.state <- stateRequest{req: reqNoIntSig}

	// prepare new debugger instance
	dbg, err := debugger.NewDebugger(tv, scr, term, *useSavekey)
	if err != nil {
		return err
	}

	switch len(md.RemainingArgs()) {
	case 0:
		return fmt.Errorf("2600 cartridge required for %s mode", md)

	case 1:
		// check for profiling options
		p, err := performance.ParseProfileString(*profile)
		if err != nil {
			return err
		}

		// set up a launch function
		dbgLaunch := func() error {
			err := dbg.Start(*initScript, cartload)
			if err != nil {
				return err
			}
			return nil
		}

		if p == performance.ProfileNone {
			// no profile required so run dbgLaunch() function as normal
			err := dbgLaunch()
			if err != nil {
				return err
			}
		} else {
			// if profile generation has been requested then pass the dbgLaunch()
			// function prepared above, through the RunProfiler() function
			err := performance.RunProfiler(p, "debugger", dbgLaunch)
			if err != nil {
				return err
			}
		}

	default:
		return fmt.Errorf("too many arguments for %s mode", md)
	}

	// set ending state
	err = scr.SetFeature(gui.ReqState, gui.StateEnding)
	if err != nil {
		return err
	}

	return nil
}

func disasm(md *modalflag.Modes) error {
	md.NewMode()

	mapping := md.AddString("mapping", "AUTO", "force use of cartridge mapping")
	bytecode := md.AddBool("bytecode", false, "include bytecode in disassembly")
	bank := md.AddInt("bank", -1, "show disassembly for a specific bank")

	p, err := md.Parse()
	if err != nil || p != modalflag.ParseContinue {
		return err
	}

	switch len(md.RemainingArgs()) {
	case 0:
		return fmt.Errorf("2600 cartridge required for %s mode", md)
	case 1:
		attr := disassembly.ColumnAttr{
			ByteCode: *bytecode,
			Label:    true,
			Cycles:   true,
		}

		cartload := cartridgeloader.NewLoader(md.GetArg(0), *mapping)
		defer cartload.Close()

		dsm, err := disassembly.FromCartridge(cartload)
		if err != nil {
			// print what disassembly output we do have
			if dsm != nil {
				// ignore any further errors
				_ = dsm.Write(md.Output, attr)
			}
			return err
		}

		// output entire disassembly or just a specific bank
		if *bank < 0 {
			err = dsm.Write(md.Output, attr)
		} else {
			err = dsm.WriteBank(md.Output, attr, *bank)
		}

		if err != nil {
			return err
		}
	default:
		return fmt.Errorf("too many arguments for %s mode", md)
	}

	return nil
}

func perform(md *modalflag.Modes, sync *mainSync) error {
	md.NewMode()

	mapping := md.AddString("mapping", "AUTO", "force use of cartridge mapping")
	spec := md.AddString("tv", "AUTO", "television specification: NTSC, PAL, PAL60")
	display := md.AddBool("display", false, "display TV output")
	fpsCap := md.AddBool("fpscap", true, "cap FPS to specification")
	duration := md.AddString("duration", "5s", "run duration (note: there is a 2s overhead)")
	profile := md.AddString("profile", "NONE", "run performance check with profiling: command separated CPU, MEM, TRACE or ALL")
	log := md.AddBool("log", false, "echo debugging log to stdout")

	p, err := md.Parse()
	if err != nil || p != modalflag.ParseContinue {
		return err
	}

	// set debugging log echo
	if *log {
		logger.SetEcho(os.Stdout)
	} else {
		logger.SetEcho(nil)
	}

	switch len(md.RemainingArgs()) {
	case 0:
		return fmt.Errorf("2600 cartridge required for %s mode", md)
	case 1:
		cartload := cartridgeloader.NewLoader(md.GetArg(0), *mapping)
		defer cartload.Close()

		tv, err := television.NewTelevision(*spec)
		if err != nil {
			return err
		}
		defer tv.End()

		// fpscap for tv (see below for gui vsync option)
		tv.SetFPSCap(*fpsCap)

		// GUI instance if required
		var scr gui.GUI

		if *display {
			// create gui
			sync.state <- stateRequest{req: reqCreateGUI,
				args: guiCreate(func() (guiControl, error) {
					return sdlimgui.NewSdlImgui(tv)
				}),
			}

			// wait for creator result
			select {
			case g := <-sync.gui:
				scr = g.(gui.GUI)
			case err := <-sync.guiError:
				return err
			}

			// fpscap for gui (see above for tv option)
			scr.SetFeature(gui.ReqVSync, *fpsCap)
		}

		// check for profiling options
		p, err := performance.ParseProfileString(*profile)
		if err != nil {
			return err
		}

		// run performance check
		err = performance.Check(md.Output, p, false, tv, scr, cartload, *duration)
		if err != nil {
			return err
		}

		// deliberately not saving gui preferences because we don't want any
		// changes to the performance window impacting the play mode

	default:
		return fmt.Errorf("too many arguments for %s mode", md)
	}

	return nil
}

func regress(md *modalflag.Modes, sync *mainSync) error {
	md.NewMode()
	md.AddSubModes("RUN", "LIST", "DELETE", "ADD", "REDUX")

	p, err := md.Parse()
	if err != nil || p != modalflag.ParseContinue {
		return err
	}

	switch md.Mode() {
	case "RUN":
		md.NewMode()

		// no additional arguments
		verbose := md.AddBool("verbose", false, "output more detail (eg. error messages)")

		p, err := md.Parse()
		if err != nil || p != modalflag.ParseContinue {
			return err
		}

		// turn off default sigint handling
		sync.state <- stateRequest{req: reqNoIntSig}

		err = regression.RegressRun(md.Output, *verbose, md.RemainingArgs())
		if err != nil {
			return err
		}

	case "LIST":
		md.NewMode()

		// no additional arguments

		p, err := md.Parse()
		if err != nil || p != modalflag.ParseContinue {
			return err
		}

		switch len(md.RemainingArgs()) {
		case 0:
			err := regression.RegressList(md.Output)
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("no additional arguments required for %s mode", md)
		}

	case "DELETE":
		md.NewMode()

		answerYes := md.AddBool("yes", false, "answer yes to confirmation")

		p, err := md.Parse()
		if err != nil || p != modalflag.ParseContinue {
			return err
		}

		switch len(md.RemainingArgs()) {
		case 0:
			return fmt.Errorf("database key required for %s mode", md)
		case 1:

			// use stdin for confirmation unless "yes" flag has been sent
			var confirmation io.Reader
			if *answerYes {
				confirmation = &yesReader{}
			} else {
				confirmation = os.Stdin
			}

			err := regression.RegressDelete(md.Output, confirmation, md.GetArg(0))
			if err != nil {
				return err
			}
		default:
			return fmt.Errorf("only one entry can be deleted at at time")
		}

	case "ADD":
		return regressAdd(md)

	case "REDUX":
		md.NewMode()

		answerYes := md.AddBool("yes", false, "always answer yes to confirmation")

		p, err := md.Parse()
		if err != nil || p != modalflag.ParseContinue {
			return err
		}

		var confirmation io.Reader
		if *answerYes {
			confirmation = &yesReader{}
		} else {
			confirmation = os.Stdin
		}

		return regression.RegressRedux(md.Output, confirmation)
	}

	return nil
}

func regressAdd(md *modalflag.Modes) error {
	md.NewMode()

	mode := md.AddString("mode", "", "type of regression entry")
	notes := md.AddString("notes", "", "additional annotation for the database")
	mapping := md.AddString("mapping", "AUTO", "force use of cartridge mapping [non-playback]")
	spec := md.AddString("tv", "AUTO", "television specification: NTSC, PAL, PAL60 [non-playback]")
	numframes := md.AddInt("frames", 10, "number of frames to run [non-playback]")
	state := md.AddString("state", "", "record emulator state at every CPU step [non-playback]")
	log := md.AddBool("log", false, "echo debugging log to stdout")

	md.AdditionalHelp(
		`The regression test to be added can be the path to a cartridge file or a previously
recorded playback file. For playback files, the flags marked [non-playback] do not make
sense and will be ignored.

Available modes are VIDEO, PLAYBACK and LOG. If not mode is explicitly given then
VIDEO will be used for ROM files and PLAYBACK will be used for playback recordings.

Value for the -state flag can be one of TV, PORTS, TIMER, CPU and can be used
with the default VIDEO mode.

The -log flag intructs the program to echo the log to the console. Do not confuse this
with the LOG mode. Note that asking for log output will suppress regression progress meters.`)

	p, err := md.Parse()
	if err != nil || p != modalflag.ParseContinue {
		return err
	}

	// set debugging log echo
	if *log {
		logger.SetEcho(os.Stdout)
		md.Output = &nopWriter{}
	} else {
		logger.SetEcho(nil)
	}

	switch len(md.RemainingArgs()) {
	case 0:
		return fmt.Errorf("2600 cartridge or playback file required for %s mode", md)
	case 1:
		var reg regression.Regressor

		if *mode == "" {
			if err := recorder.IsPlaybackFile(md.GetArg(0)); err == nil {
				*mode = "PLAYBACK"
			} else if !curated.Is(err, recorder.NotAPlaybackFile) {
				return err
			} else {
				*mode = "VIDEO"
			}
		}

		switch strings.ToUpper(*mode) {
		case "VIDEO":
			cartload := cartridgeloader.NewLoader(md.GetArg(0), *mapping)
			defer cartload.Close()

			statetype, err := regression.NewStateType(*state)
			if err != nil {
				return err
			}

			reg = &regression.VideoRegression{
				CartLoad:  cartload,
				TVtype:    strings.ToUpper(*spec),
				NumFrames: *numframes,
				State:     statetype,
				Notes:     *notes,
			}
		case "PLAYBACK":
			// check and warn if unneeded arguments have been specified
			md.Visit(func(flg string) {
				if flg == "frames" {
					fmt.Printf("! ignored %s flag when adding playback entry\n", flg)
				}
			})

			reg = &regression.PlaybackRegression{
				Script: md.GetArg(0),
				Notes:  *notes,
			}
		case "LOG":
			cartload := cartridgeloader.NewLoader(md.GetArg(0), *mapping)
			defer cartload.Close()

			reg = &regression.LogRegression{
				CartLoad:  cartload,
				TVtype:    strings.ToUpper(*spec),
				NumFrames: *numframes,
				Notes:     *notes,
			}
		}

		err := regression.RegressAdd(md.Output, reg)
		if err != nil {
			// using carriage return (without newline) at beginning of error
			// message because we want to overwrite the last output from
			// RegressAdd()
			return fmt.Errorf("\rerror adding regression test: %v", err)
		}
	default:
		return fmt.Errorf("regression tests can only be added one at a time")
	}

	return nil
}

func hiscoreServer(md *modalflag.Modes) error {
	md.NewMode()
	md.AddSubModes("ABOUT", "SETSERVER", "LOGIN", "LOGOFF")
	md.AdditionalHelp("Hiscore server support is EXPERIMENTAL")

	p, err := md.Parse()
	if err != nil || p != modalflag.ParseContinue {
		return err
	}

	switch md.Mode() {
	case "ABOUT":
		fmt.Println("The hiscore server is experimental and is not currently fully functioning")

	case "LOGIN":
		md.NewMode()
		p, err := md.Parse()
		if err != nil || p != modalflag.ParseContinue {
			return err
		}

		username := ""
		args := md.RemainingArgs()

		switch len(args) {
		case 0:
			// an empty string is okay
		case 1:
			username = args[0]
		default:
			return fmt.Errorf("too many arguments for %s", md)
		}

		err = hiscore.Login(os.Stdin, os.Stdout, username)
		if err != nil {
			return err
		}

	case "LOGOFF":
		err = hiscore.Logoff()
		if err != nil {
			return err
		}

	case "SETSERVER":
		md.NewMode()
		p, err := md.Parse()
		if err != nil || p != modalflag.ParseContinue {
			return err
		}

		server := ""
		args := md.RemainingArgs()

		switch len(args) {
		case 0:
			// an empty string is okay
		case 1:
			server = args[0]
		default:
			return fmt.Errorf("too many arguments for %s", md)
		}

		err = hiscore.SetServer(os.Stdin, os.Stdout, server)
		if err != nil {
			return err
		}
	}

	return nil
}

// nopWriter is an empty writer.
type nopWriter struct{}

func (*nopWriter) Write(p []byte) (n int, err error) {
	return 0, nil
}

// yesReader always returns 'y' when it is read.
type yesReader struct{}

func (*yesReader) Read(p []byte) (n int, err error) {
	p[0] = 'y'
	return 1, nil
}
