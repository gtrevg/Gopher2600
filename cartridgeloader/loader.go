// Package cartridgeloader represents cartridge data when not attached to the
// VCS. When a reference to a cartridge is required functions expect an
// instance of cartridgeloader.Loader.
//
//	cl := cartridgeloader.Loader{
//		Filename: "roms/Pitfall.bin",
//	}
//
// When the cartridge is ready to be loaded the emulator calls the Load()
// function. This function currently handles files (specified with Filename)
// that are stored locally and also over http. Other protocols could easily be
// added. A good improvement would be to allow loading from zip or tar files.
package cartridgeloader

import (
	"gopher2600/errors"
	"net/http"
	"os"
	"path"
	"strings"
)

// Loader is used to specify the cartridge to use when Attach()ing to
// the VCS. it also permits the called to specify the format of the cartridge
// (if necessary. fingerprinting is pretty good)
type Loader struct {
	Filename string

	// empty string or "AUTO" indicates automatic fingerprinting
	Format string

	// expected hash of the loaded cartridge. empty string indicates that the
	// hash is unknown and need not be validated
	Hash string

	data []byte
}

// ShortName returns a shortened version of the CartridgeLoader filename
func (cl Loader) ShortName() string {
	shortCartName := path.Base(cl.Filename)
	shortCartName = strings.TrimSuffix(shortCartName, path.Ext(cl.Filename))
	return shortCartName
}

// HasLoaded returns true if Load() has been successfully called
func (cl Loader) HasLoaded() bool {
	return len(cl.data) > 0
}

// Load the cartridge
func (cl Loader) Load() ([]byte, error) {
	if len(cl.data) > 0 {
		return cl.data, nil
	}

	var err error

	if strings.HasPrefix(cl.Filename, "http://") {
		var resp *http.Response

		resp, err = http.Get(cl.Filename)
		if err != nil {
			return nil, errors.New(errors.CartridgeLoader, cl.Filename)
		}
		defer resp.Body.Close()

		size := resp.ContentLength

		cl.data = make([]byte, size)
		_, err = resp.Body.Read(cl.data)
		if err != nil {
			return nil, errors.New(errors.CartridgeLoader, cl.Filename)
		}
	} else {
		var f *os.File
		f, err = os.Open(cl.Filename)
		if err != nil {
			return nil, errors.New(errors.CartridgeLoader, cl.Filename)
		}
		defer f.Close()

		// get file info
		cfi, err := f.Stat()
		if err != nil {
			return nil, errors.New(errors.CartridgeLoader, cl.Filename)
		}
		size := cfi.Size()

		cl.data = make([]byte, size)
		_, err = f.Read(cl.data)
		if err != nil {
			return nil, errors.New(errors.CartridgeLoader, cl.Filename)
		}
	}

	return cl.data, nil
}
