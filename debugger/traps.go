// traps are used to halt execution of the emulator when the target *changes*
// from its current value to any other value. compare to breakpoints which halt
// execution when the target is *changed to* a specific value.

package debugger

import (
	"fmt"
	"gopher2600/debugger/commandline"
	"gopher2600/debugger/console"
	"gopher2600/errors"
	"strings"
)

// traps keeps track of all the currently defined trappers
type traps struct {
	dbg   *Debugger
	traps []trapper
}

// trapper defines a specific trap
type trapper struct {
	target    target
	origValue interface{}
}

// newTraps is the preferred method of initialisation for traps
func newTraps(dbg *Debugger) *traps {
	tr := new(traps)
	tr.dbg = dbg
	tr.clear()
	return tr
}

func (tr *traps) clear() {
	tr.traps = make([]trapper, 0, 10)
}

func (tr *traps) drop(num int) error {
	if len(tr.traps)-1 < num {
		return errors.NewFormattedError(errors.CommandError, fmt.Errorf("trap #%d is not defined", num))
	}

	h := tr.traps[:num]
	t := tr.traps[num+1:]
	tr.traps = make([]trapper, len(h)+len(t), cap(tr.traps))
	copy(tr.traps, h)
	copy(tr.traps[len(h):], t)

	return nil
}

// check compares the current state of the emulation with every trap condition.
// it returns a string listing every condition that applies
func (tr *traps) check(previousResult string) string {
	checkString := strings.Builder{}
	checkString.WriteString(previousResult)
	for i := range tr.traps {
		trapValue := tr.traps[i].target.Value()

		if trapValue != tr.traps[i].origValue {
			checkString.WriteString(fmt.Sprintf("trap on %s [%v->%v]\n", tr.traps[i].target.ShortLabel(), tr.traps[i].origValue, trapValue))
			tr.traps[i].origValue = trapValue
		}
	}
	return checkString.String()
}

func (tr traps) list() {
	if len(tr.traps) == 0 {
		tr.dbg.print(console.Feedback, "no traps")
	} else {
		for i := range tr.traps {
			tr.dbg.print(console.Feedback, "% 2d: %s", i, tr.traps[i].target.ShortLabel())
		}
	}
}

func (tr *traps) parseTrap(tokens *commandline.Tokens) error {
	_, present := tokens.Peek()
	for present {
		tgt, err := parseTarget(tr.dbg, tokens)
		if err != nil {
			return err
		}

		addNewTrap := true
		for _, t := range tr.traps {
			if t.target == tgt {
				addNewTrap = false
				tr.dbg.print(console.Feedback, "trap already exists")
				break // for loop
			}
		}

		if addNewTrap {
			tr.traps = append(tr.traps, trapper{target: tgt, origValue: tgt.Value()})
		}

		_, present = tokens.Peek()
	}

	return nil
}
