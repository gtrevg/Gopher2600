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

package prefs

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	"github.com/jetsetilly/gopher2600/curated"
	"github.com/jetsetilly/gopher2600/resources/fs"
)

// DefaultPrefsFile is the default filename of the global preferences file.
const DefaultPrefsFile = "preferences"

// WarningBoilerPlate is inserted at the beginning of a preferences file.
const WarningBoilerPlate = "*** do not edit this file by hand ***"

// the string the separators the pref key from the value.
const keySep = " :: "

// the pref values to be stored on disk.
type entryMap map[string]pref

func (e entryMap) String() string {
	// we want to work with sorted keys. this makes it easier to test.
	sorted := make([]string, 0, len(e))
	for k := range e {
		sorted = append(sorted, k)
	}
	sort.Strings(sorted)

	s := strings.Builder{}

	for _, k := range sorted {
		v := e[k]
		s.WriteString(fmt.Sprintf("%s%s%s\n", k, keySep, v))
	}

	return s.String()
}

// Disk represents preference values as stored on disk.
type Disk struct {
	path    string
	entries entryMap
}

func (dsk Disk) String() string {
	return dsk.entries.String()
}

// NewDisk is the preferred method of initialisation for the Disk type.
func NewDisk(path string) (*Disk, error) {
	dsk := &Disk{
		path:    path,
		entries: make(entryMap),
	}

	return dsk, nil
}

// Add preference value to list of values to store/load from Disk. The id is
// used to identify the preference value on disk and should only consist of
// alphabetic characters or the period character. The program will panic if
// these constraints are not met.
func (dsk *Disk) Add(key string, p pref) error {
	for _, r := range key {
		if !(r == '.' || unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return curated.Errorf("prefs: %v", fmt.Errorf("illegal character [%c] in key string [%s]", r, key))
		}
	}

	dsk.entries[key] = p
	return nil
}

// Reset all preferences to the default values. In other words, the value they
// would have on first use before being set.
func (dsk *Disk) Reset() error {
	for _, v := range dsk.entries {
		err := v.Reset()
		if err != nil {
			return curated.Errorf("prefs: %v", err)
		}
	}

	return nil
}

// DisableSaving is useful for testing when a blanket prohibition on saving to
// disk is required.
var DisableSaving = false

// Save current preference values to disk.
func (dsk *Disk) Save() (rerr error) {
	if DisableSaving {
		return nil
	}

	// load entirity of currently saved prefs file to a temporary entryMap
	entries := make(entryMap)

	// load *all* existing entries to temporary entryMap
	_, err := load(dsk.path, &entries, false)
	if err != nil {
		return curated.Errorf("prefs: %v", err)
	}

	// copy live values to entryMap, overwriting existing entries
	// if they already exists
	for k, v := range dsk.entries {
		entries[k] = v
	}

	// create a new prefs file
	f, err := fs.Create(dsk.path)
	if err != nil {
		return curated.Errorf("prefs: %v", err)
	}
	defer func() {
		err := f.Close()
		if err != nil {
			rerr = curated.Errorf("prefs: %v", err)
		}
	}()

	// number of characters written
	var n int

	// add warning label
	n, err = fmt.Fprintf(f, "%s\n", WarningBoilerPlate)
	if err != nil {
		return curated.Errorf("prefs: %v", err)
	}
	if n != len(WarningBoilerPlate)+1 {
		return curated.Errorf("prefs: %v", "incorrect number of characters writtent to file")
	}

	// write entries (combination of old and live entries) to disk
	s := entries.String()
	n, err = fmt.Fprint(f, s)
	if err != nil {
		return curated.Errorf("prefs: %v", err)
	}
	if n != len(s) {
		return curated.Errorf("prefs: %v", "incorrect number of characters writtent to file")
	}

	return nil
}

// Load preference values from disk. The saveonFirstUse argument is useful when
// loading preferences on initialisation. It makes sure default preferences are
// saved to disk if they are not present in the preferences file.
func (dsk *Disk) Load(saveOnFirstUse bool) error {
	numLoaded, err := load(dsk.path, &dsk.entries, true)
	if err != nil {
		return err
	}

	// if the number of entries loaded by the load() function is not equal to
	// then number of entries in this Disk instance then we can say that a new
	// preference value has been added since the last save to disk. if
	// saveOnFirstUse is true then save immediately to make sure the default
	// value is on disk.
	if saveOnFirstUse && numLoaded != len(dsk.entries) {
		return dsk.Save()
	}

	return nil
}

// underlying function to load preference value froms disk. the limit boolean
// controls whether to load all valid preference values from the file or to
// ignore those values not already in the entryMap. limit=false is used by the
// save() function in order to avoid clobbering unknown entries.
//
// it returns the number of entries loaded from disk. if limit is false then
// the number returned is the total number of entries in the file.
func load(path string, entries *entryMap, limit bool) (int, error) {
	var numLoaded int

	// open existing prefs file
	f, err := fs.Open(path)
	if err != nil {
		switch err.(type) {
		case *os.PathError:
			return 0, nil
		}
		return numLoaded, curated.Errorf("prefs: %v", err)
	}
	defer f.Close()

	// new scanner - splitting on newlines
	scanner := bufio.NewScanner(f)

	// check validity of file by checking the first line
	scanner.Scan()
	if len(scanner.Text()) > 0 && scanner.Text() != WarningBoilerPlate {
		return 0, curated.Errorf("prefs: %v", fmt.Errorf("not a valid prefs file (%s)", path))
	}

	// key and value strings
	var k string
	var v string

	// loop through file until EOF
	for scanner.Scan() {
		// split line into key/value pair
		spt := strings.SplitN(scanner.Text(), keySep, 2)

		// ignore lines that haven't been split successfully
		if len(spt) != 2 {
			continue
		}

		k = spt[0]
		v = spt[1]

		// assign value to entries if key exists
		if p, ok := (*entries)[k]; ok {
			err = p.Set(v)
			if err != nil {
				return numLoaded, curated.Errorf("prefs: %v", err)
			}
			numLoaded++
		} else if !limit {
			// if this an unlimited load() and preference key is not in list of
			// dufunct values then store in entryMap
			if !isDefunct(k) {
				var dummy String
				err = dummy.Set(v)
				if err != nil {
					return numLoaded, curated.Errorf("prefs: %v", err)
				}
				(*entries)[k] = &dummy
			}
		}
	}

	return numLoaded, nil
}

// HasEntry returns false if there is no matching entry on disk and true if
// there is.
func (dsk *Disk) HasEntry(key string) (bool, error) {
	var e entryMap
	var s String
	e = make(entryMap)
	e[key] = &s

	n, err := load(dsk.path, &e, true)
	if err != nil {
		return false, err
	}

	return n != 1, nil
}
