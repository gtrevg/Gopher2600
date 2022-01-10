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

package developer

import (
	"fmt"
	"sort"
	"sync"

	"github.com/jetsetilly/gopher2600/hardware/memory/cartridge/mapper"
	"github.com/jetsetilly/gopher2600/hardware/television"
	"github.com/jetsetilly/gopher2600/logger"
)

// Developer implements the CartCoProcDeveloper interface.
type Developer struct {
	cart mapper.CartCoProcBus

	// obj dump for binary (if available)
	source     *Source
	sourceLock sync.Mutex

	// illegal accesses already encountered. duplicate accesses will not be logged.
	illegalAccess     IllegalAccess
	illegalAccessLock sync.Mutex
}

// NewDeveloper is the preferred method of initialisation for the Developer type.
func NewDeveloper(pathToROM string, cart mapper.CartCoProcBus) *Developer {
	if cart == nil {
		return nil
	}

	var err error

	dev := &Developer{
		cart: cart,
		illegalAccess: IllegalAccess{
			entries: make(map[string]IllegalAccessEntry),
			Log:     make([]IllegalAccessEntry, 0),
		},
	}

	dev.cart.SetDeveloper(dev)

	dev.source, err = newSource(pathToROM)
	if err != nil {
		logger.Logf("developer", err.Error())
	}

	return dev
}

// IllegalAccess implements the CartCoProcDeveloper interface.
func (dev *Developer) IllegalAccess(event string, pc uint32, addr uint32) string {
	dev.sourceLock.Lock()
	defer dev.sourceLock.Unlock()

	accessKey := fmt.Sprintf("%08x%08x", addr, pc)
	if _, ok := dev.illegalAccess.entries[accessKey]; ok {
		return ""
	}

	e := IllegalAccessEntry{
		Event:      event,
		PC:         pc,
		AccessAddr: addr,
	}

	if dev.source != nil {
		e.Source = dev.source.findProgramAccess(pc)
	}

	dev.illegalAccess.entries[accessKey] = e
	dev.illegalAccess.Log = append(dev.illegalAccess.Log, e)

	if e.Source == nil {
		return "<unknown source line>"
	}

	function := ""
	if e.Source.Function != "" {
		function = fmt.Sprintf(": %s()", e.Source.Function)
	} else {
		function = "<unknown function>"
	}

	return fmt.Sprintf("%s%s\n%s", e.Source.String(), function, e.Source.Content)
}

// ExecutionProfile implements the CartCoProcDeveloper interface.
func (dev *Developer) ExecutionProfile(addr map[uint32]float32) {
	dev.sourceLock.Lock()
	defer dev.sourceLock.Unlock()

	if dev.source != nil {
		for k, v := range addr {
			dev.source.execute(k, v)
		}

		sort.Sort(dev.source.ExecutedLines)
	}
}

// BorrowSource will lock the source code structure for the durction of the
// supplied function, which will be executed with the source code structure as
// an argument.
//
// May return nil.
func (dev *Developer) BorrowSource(f func(*Source)) {
	dev.sourceLock.Lock()
	defer dev.sourceLock.Unlock()
	f(dev.source)
}

// BorrowIllegalAccess will lock the illegal access log for the duration of the
// supplied fucntion, which will be executed with the illegal access log as an
// argument.
func (dev *Developer) BorrowIllegalAccess(f func(*IllegalAccess)) {
	dev.illegalAccessLock.Lock()
	defer dev.illegalAccessLock.Unlock()
	f(&dev.illegalAccess)
}

// NewFrame implements the television.FrameTrigger interface.
func (dev *Developer) NewFrame(_ television.FrameInfo) error {
	dev.sourceLock.Lock()
	defer dev.sourceLock.Unlock()

	if dev.source == nil {
		return nil
	}

	for _, s := range dev.source.ExecutedLines.Lines {
		s.FrameCycles = s.nextFrameCycles
		s.nextFrameCycles = 0
	}

	dev.source.FrameCycles = dev.source.nextFrameCycles
	dev.source.nextFrameCycles = 0

	return nil
}
