package colorterm

import (
	"gopher2600/debugger/colorterm/ansi"
	"gopher2600/debugger/colorterm/easyterm"
	"gopher2600/debugger/ui"
	"unicode"
	"unicode/utf8"
)

// UserRead is the top level input function
func (ct *ColorTerminal) UserRead(input []byte, prompt string, dbgChannel chan func()) (int, error) {

	// ctrl-c handling: currently, we put the terminal into rawmode and listen
	// for ctrl-c event using the readRune reader, rather than using the ctrl-c
	// handler on the end of the dbgChannel. we could use the ctrl-c handler
	// but it means having a return value for the channeled function which I'm
	// not prepared to add just yet. this method is okay.

	ct.RawMode()
	defer ct.CanonicalMode()

	// er is used to store encoded runes (length of 4 should be enough)
	er := make([]byte, 4)

	inputLen := 0
	cursorPos := 0
	history := len(ct.commandHistory)

	// liveBuffInput is used to store the latest input when we scroll through
	// history - we don't want to lose what we've typed in case the user wants
	// to resume where we left off
	liveBuffInput := make([]byte, cap(input))
	liveBuffInputLen := 0

	// the method for cursor placement is as follows:
	//	 for each iteration in the loop
	//		1. store current cursor position
	//		2. clear the current line
	//		3. output the prompt
	//		4. output the input buffer
	//		5. restore the cursor position
	//
	// for this to work we need to place the cursor in it's initial position
	// before we begin the loop
	ct.Print("\r%s", ansi.CursorMove(len(prompt)))

	for {
		ct.Print(ansi.CursorStore)
		ct.UserPrint(ui.Prompt, "%s%s", ansi.ClearLine, prompt)
		ct.UserPrint(ui.Input, string(input[:inputLen]))
		ct.Print(ansi.CursorRestore)

		select {
		case f := <-dbgChannel:
			ct.Print(ansi.CursorStore)
			f()
			ct.Print(ansi.CursorRestore)

		case readRune := <-ct.reader:
			if readRune.err != nil {
				return inputLen, readRune.err
			}

			switch readRune.r {
			case easyterm.KeyTab:
				if ct.tabCompleter != nil {
					s := ct.tabCompleter.GuessWord(string(input[:cursorPos]))

					// the difference in the length of the new input and the old
					// input
					d := len(s) - cursorPos

					// append everythin after the cursor to the new string and copy
					// into input array
					s += string(input[cursorPos:])
					copy(input, []byte(s))

					// advance character to end of completed word
					ct.Print(ansi.CursorMove(d))
					cursorPos += d

					// note new used-length of input array
					inputLen += d
				}

			case easyterm.KeyCtrlC:
				// CTRL-C -- note that there is a ctrl-c signal handler, set up in
				// debugger.Start(), that controls the main debugging loop. this
				// ctrl-c handler by contrast, controls the user input loop
				ct.Print("\n")
				return inputLen + 1, &ui.UserInterrupt{Message: "user interrupt: CTRL-C"}

			case easyterm.KeyCarriageReturn:
				// CARRIAGE RETURN

				// check to see if input is the same as the last history entry
				newEntry := false
				if inputLen > 0 {
					newEntry = true
					if len(ct.commandHistory) > 0 {
						lastHistoryEntry := ct.commandHistory[len(ct.commandHistory)-1].input
						if len(lastHistoryEntry) == inputLen {
							newEntry = false
							for i := 0; i < inputLen; i++ {
								if input[i] != lastHistoryEntry[i] {
									newEntry = true
									break
								}
							}
						}
					}
				}

				// if input is not the same as the last history entry then append a
				// new entry to the history list
				if newEntry {
					nh := make([]byte, inputLen)
					copy(nh, input[:inputLen])
					ct.commandHistory = append(ct.commandHistory, command{input: nh})
				}

				ct.Print("\r\n")
				return inputLen + 1, nil

			case easyterm.KeyEsc:
				// ESCAPE SEQUENCE BEGIN
				readRune = <-ct.reader
				if readRune.err != nil {
					return inputLen, readRune.err
				}
				switch readRune.r {
				case easyterm.EscCursor:
					// CURSOR KEY
					readRune = <-ct.reader
					if readRune.err != nil {
						return inputLen, readRune.err
					}

					switch readRune.r {
					case easyterm.CursorUp:
						// move up through command history
						if len(ct.commandHistory) > 0 {
							// if we're at the end of the command history then store
							// the current input in liveBuffInput for possible later editing
							if history == len(ct.commandHistory) {
								copy(liveBuffInput, input[:inputLen])
								liveBuffInputLen = inputLen
							}

							if history > 0 {
								history--
								copy(input, ct.commandHistory[history].input)
								inputLen = len(ct.commandHistory[history].input)
								ct.Print(ansi.CursorMove(inputLen - cursorPos))
								cursorPos = inputLen
							}
						}
					case easyterm.CursorDown:
						// move down through command history
						if len(ct.commandHistory) > 0 {
							if history < len(ct.commandHistory)-1 {
								history++
								copy(input, ct.commandHistory[history].input)
								inputLen = len(ct.commandHistory[history].input)
								ct.Print(ansi.CursorMove(inputLen - cursorPos))
								cursorPos = inputLen
							} else if history == len(ct.commandHistory)-1 {
								history++
								copy(input, liveBuffInput)
								inputLen = liveBuffInputLen
								ct.Print(ansi.CursorMove(inputLen - cursorPos))
								cursorPos = inputLen
							}
						}
					case easyterm.CursorForward:
						// move forward through current command input
						if cursorPos < inputLen {
							ct.Print(ansi.CursorForwardOne)
							cursorPos++
						}
					case easyterm.CursorBackward:
						// move backward through current command input
						if cursorPos > 0 {
							ct.Print(ansi.CursorBackwardOne)
							cursorPos--
						}

					case easyterm.EscDelete:
						// DELETE
						if cursorPos < inputLen {
							copy(input[cursorPos:], input[cursorPos+1:])
							inputLen--
							history = len(ct.commandHistory)
						}

						// eat the third character in the sequence
						readRune = <-ct.reader
					}
				}

			case easyterm.KeyBackspace:
				// BACKSPACE
				if cursorPos > 0 {
					copy(input[cursorPos-1:], input[cursorPos:])
					ct.Print(ansi.CursorBackwardOne)
					cursorPos--
					inputLen--
					history = len(ct.commandHistory)
				}

			default:
				if unicode.IsDigit(readRune.r) || unicode.IsLetter(readRune.r) || unicode.IsSpace(readRune.r) || unicode.IsPunct(readRune.r) || unicode.IsSymbol(readRune.r) {
					ct.Print("%c", readRune.r)
					m := utf8.EncodeRune(er, readRune.r)
					copy(input[cursorPos+m:], input[cursorPos:])
					copy(input[cursorPos:], er[:m])
					cursorPos++
					inputLen += m
					history = len(ct.commandHistory)
				}
			}
		}
	}
}
