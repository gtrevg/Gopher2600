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
//
// *** NOTE: all historical versions of this file, as found in any
// git repository, are also covered by the licence, even when this
// notice is not present ***

package digest

import (
	"crypto/sha1"
	"fmt"

	"github.com/jetsetilly/gopher2600/errors"
	"github.com/jetsetilly/gopher2600/television"
)

// Video is an implementation of the television.PixelRenderer interface with an
// embedded television for convenience. It generates a SHA-1 value of the image
// every frame. it does not display the image anywhere.
//
// Note that the use of SHA-1 is fine for this application because this is not
// a cryptographic task.
type Video struct {
	television.Television
	digest   [sha1.Size]byte
	pixels   []byte
	frameNum int
}

const pixelDepth = 3

// NewVideo initialises a new instance of DigestTV. For convenience, the
// television argument can be nil, in which case an instance of
// StellaTelevision will be created.
func NewVideo(tv television.Television) (*Video, error) {
	// set up digest tv
	dig := &Video{Television: tv}

	// register ourselves as a television.Renderer
	dig.AddPixelRenderer(dig)

	// length of pixels array contains enough room for the previous frames
	// digest value
	l := len(dig.digest)

	// alloscate enough pixels for entire frame
	l += ((television.HorizClksScanline + 1) * (dig.GetSpec().ScanlinesTotal + 1) * pixelDepth)
	dig.pixels = make([]byte, l)

	return dig, nil
}

// Hash implements digest.Digest interface
func (dig Video) Hash() string {
	return fmt.Sprintf("%x", dig.digest)
}

// ResetDigest implements digest.Digest interface
func (dig *Video) ResetDigest() {
	for i := range dig.digest {
		dig.digest[i] = 0
	}
}

// Resize implements television.PixelRenderer interface
//
// Note that Resize() does nothing in this implementation because we always
// work on the entire frame.
//
// that said, we could record resize events by having a flag bit in the pixel
// array. this additional bit (or byte) will then be included in the hashing
// process.
//
// !!TODO: consider resize flag bit for digest.Video
func (dig *Video) Resize(_, _ int) error {
	return nil
}

// NewFrame implements television.PixelRenderer interface
func (dig *Video) NewFrame(frameNum int) error {
	// chain fingerprints by copying the value of the last fingerprint
	// to the head of the video data
	n := copy(dig.pixels, dig.digest[:])
	if n != len(dig.digest) {
		return errors.New(errors.VideoDigest, fmt.Sprintf("digest error during new frame"))
	}
	dig.digest = sha1.Sum(dig.pixels)
	dig.frameNum = frameNum
	return nil
}

// NewScanline implements television.PixelRenderer interface
func (dig *Video) NewScanline(scanline int) error {
	return nil
}

// SetPixel implements television.PixelRenderer interface
func (dig *Video) SetPixel(x, y int, red, green, blue byte, vblank bool) error {
	// preserve the first few bytes for a chained fingerprint
	i := len(dig.digest)
	i += television.HorizClksScanline * y * pixelDepth
	i += x * pixelDepth

	if i <= len(dig.pixels)-pixelDepth {
		// setting every pixel regardless of vblank value
		dig.pixels[i] = red
		dig.pixels[i+1] = green
		dig.pixels[i+2] = blue
	}

	return nil
}

// SetAltPixel implements television.PixelRenderer interface
func (dig *Video) SetAltPixel(x, y int, red, green, blue byte, vblank bool) error {
	return nil
}

// EndRendering implements television.PixelRenderer interface
func (dig *Video) EndRendering() error {
	return nil
}
