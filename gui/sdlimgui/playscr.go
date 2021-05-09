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

package sdlimgui

import (
	"fmt"
	"time"

	"github.com/go-gl/gl/v3.2-core/gl"
	"github.com/inkyblackness/imgui-go/v4"
	"github.com/jetsetilly/gopher2600/gui/sdlimgui/fonts"
)

// note that values from the lazy package will not be updated in the service
// loop when the emulator is in playmode. nothing in winPlayScr() therefore
// should rely on any lazy value.

type playScr struct {
	img *SdlImgui

	// reference to screen data
	scr *screen

	// textures
	screenTexture uint32

	// (re)create textures on next render()
	createTextures bool

	// the tv screen has captured mouse input
	isCaptured bool

	imagePosMin imgui.Vec2
	imagePosMax imgui.Vec2

	// the basic amount by which the image should be scaled. this value is
	// applie to the vertical axis directly. horizontal scaling is scaled by
	// pixelWidth and aspectBias also. use horizScaling() for that.
	scaling float32

	// fps
	fpsOpen  bool
	fpsPulse *time.Ticker
	fps      string

	// controller alert
	controllerAlertLeft  controllerAlert
	controllerAlertRight controllerAlert
}

// controllerAlert will appear on the screen to indicate a new controller in
// the player port.
type controllerAlert struct {
	frames int
	desc   string
}

func (ca *controllerAlert) open(desc string) {
	ca.desc = desc
	ca.frames = 60
}

func (ca *controllerAlert) isOpen() bool {
	if ca.frames == 0 {
		return false
	}
	ca.frames--
	return true
}

func newPlayScr(img *SdlImgui) *playScr {
	win := &playScr{
		img:      img,
		scr:      img.screen,
		scaling:  2.0,
		fpsPulse: time.NewTicker(time.Second),
	}

	// set texture, creation of textures will be done after every call to resize()
	gl.GenTextures(1, &win.screenTexture)
	gl.BindTexture(gl.TEXTURE_2D, win.screenTexture)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.NEAREST)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.NEAREST)

	// set scale and padding on startup. scale and padding will be recalculated
	// on window resize and textureRenderer.resize()
	win.scr.crit.section.Lock()
	win.setScaling()
	win.scr.crit.section.Unlock()

	return win
}

func (win *playScr) draw() {
	dl := imgui.BackgroundDrawList()
	dl.AddImage(imgui.TextureID(win.screenTexture), win.imagePosMin, win.imagePosMax)

	if win.fpsOpen {
		// update fps
		select {
		case <-win.fpsPulse.C:
			win.fps = fmt.Sprintf("%03.1f fps", win.img.tv.GetActualFPS())
		default:
		}

		imgui.SetNextWindowPos(imgui.Vec2{0, 0})

		imgui.PushStyleColor(imgui.StyleColorWindowBg, win.img.cols.Transparent)
		imgui.PushStyleColor(imgui.StyleColorBorder, win.img.cols.Transparent)

		imgui.BeginV("##playscrfps", &win.fpsOpen, imgui.WindowFlagsAlwaysAutoResize|
			imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoDecoration)

		imgui.Text(win.fps)

		imgui.PopStyleColorV(2)
		imgui.End()
	}

	dimen := win.img.plt.displaySize()
	if win.controllerAlertLeft.isOpen() {
		win.drawControllerAlert("##controlleralertleft", win.controllerAlertLeft.desc, imgui.Vec2{0, dimen[1]}, false)
	}

	if win.controllerAlertRight.isOpen() {
		win.drawControllerAlert("##controlleralertright", win.controllerAlertRight.desc, imgui.Vec2{dimen[0], dimen[1]}, true)
	}
}

// pos should be the coordinate of the *extreme* bottom left or bottom right of
// the playscr window. the values will be adjusted according to whether we're
// display an icon or text.
func (win *playScr) drawControllerAlert(id string, description string, pos imgui.Vec2, rightJustify bool) {
	useIcon := false

	switch description {
	case "Stick":
		useIcon = true
		description = fmt.Sprintf("%c %s", fonts.Stick, description)
	case "Paddle":
		useIcon = true
		description = fmt.Sprintf("%c %s", fonts.Paddle, description)
	case "Keyboard":
		useIcon = true
		description = fmt.Sprintf("%c %s", fonts.Keyboard, description)
	default:
		// we don't recognise the controller so we'll just print the text
		pos.Y -= imgui.FrameHeightWithSpacing() * 1.2
		if rightJustify {
			pos.X -= imguiTextWidth(len(description))
		}
	}

	if useIcon {
		imgui.PushFont(win.img.glsl.gopher2600Icons)
		defer imgui.PopFont()
		pos.Y -= win.img.glsl.gopher2600IconsSize * 1.5
		if rightJustify {
			pos.X -= win.img.glsl.gopher2600IconsSize * 1.5
		}
	}

	imgui.SetNextWindowPos(pos)
	imgui.PushStyleColor(imgui.StyleColorWindowBg, win.img.cols.Transparent)
	imgui.PushStyleColor(imgui.StyleColorBorder, win.img.cols.Transparent)

	imgui.BeginV(id, &win.fpsOpen, imgui.WindowFlagsAlwaysAutoResize|
		imgui.WindowFlagsNoScrollbar|imgui.WindowFlagsNoTitleBar|imgui.WindowFlagsNoDecoration)

	imgui.Text(description)

	imgui.PopStyleColorV(2)
	imgui.End()
}

// resize() implements the textureRenderer interface.
func (win *playScr) resize() {
	win.createTextures = true
	win.setScaling()
}

// render() implements the textureRenderer interface.
//
// render is called by service loop (via screen.render()). acquires it's own
// crit.section lock.
func (win *playScr) render() {
	win.scr.crit.section.Lock()
	defer win.scr.crit.section.Unlock()

	pixels := win.scr.crit.cropPixels

	gl.PixelStorei(gl.UNPACK_ROW_LENGTH, int32(pixels.Stride)/4)
	defer gl.PixelStorei(gl.UNPACK_ROW_LENGTH, 0)

	if win.createTextures {
		win.createTextures = false

		// (re)create textures
		gl.BindTexture(gl.TEXTURE_2D, win.screenTexture)
		gl.TexImage2D(gl.TEXTURE_2D, 0,
			gl.RGBA, int32(pixels.Bounds().Size().X), int32(pixels.Bounds().Size().Y), 0,
			gl.RGBA, gl.UNSIGNED_BYTE,
			gl.Ptr(pixels.Pix))
	} else if win.scr.crit.isStable {
		gl.BindTexture(gl.TEXTURE_2D, win.screenTexture)
		gl.TexSubImage2D(gl.TEXTURE_2D, 0,
			0, 0, int32(pixels.Bounds().Size().X), int32(pixels.Bounds().Size().Y),
			gl.RGBA, gl.UNSIGNED_BYTE,
			gl.Ptr(pixels.Pix))
	}

	// unlike dbgscr, there is no need to call setScaling() every render()
}

// must be called from with a critical section.
func (win *playScr) setScaling() {
	sz := win.img.plt.displaySize()
	dim := imgui.Vec2{sz[0], sz[1]}

	winAspectRatio := dim.X / dim.Y

	w := float32(win.scr.crit.cropPixels.Bounds().Size().X)
	h := float32(win.scr.crit.cropPixels.Bounds().Size().Y)
	w *= pixelWidth * win.scr.aspectBias
	aspectRatio := w / h

	if aspectRatio < winAspectRatio {
		win.scaling = dim.Y / h
		win.imagePosMin = imgui.Vec2{X: float32(int((dim.X - (w * win.scaling)) / 2))}
	} else {
		win.scaling = dim.X / w
		win.imagePosMin = imgui.Vec2{Y: float32(int((dim.Y - (h * win.scaling)) / 2))}
	}

	win.imagePosMax = dim.Minus(win.imagePosMin)
}

// must be called from with a critical section.
func (win *playScr) scaledWidth() float32 {
	// we always use cropped pixels on the playscreen
	return float32(win.scr.crit.cropPixels.Bounds().Size().X) * win.horizScaling()
}

// must be called from with a critical section.
func (win *playScr) scaledHeight() float32 {
	return float32(win.scr.crit.cropPixels.Bounds().Size().Y) * win.scaling
}

// for vertical scaling simply refer to the scaling field.
func (win *playScr) horizScaling() float32 {
	return pixelWidth * win.scr.aspectBias * win.scaling
}
