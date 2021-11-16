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
	"math"

	"github.com/inkyblackness/imgui-go/v4"
	"github.com/jetsetilly/gopher2600/gui/fonts"
	"github.com/jetsetilly/gopher2600/hardware/television/signal"
	"github.com/jetsetilly/gopher2600/hardware/tia/audio/mix"
)

const winOscilloscopeID = "Oscilloscope"

type oscilloscopeData struct {
	mono    float32
	stereoL float32
	stereoR float32
	chan0   float32
	chan1   float32
}

type winOscilloscope struct {
	img  *SdlImgui
	open bool

	monoBuffer    []float32
	stereoLBuffer []float32
	stereoRBuffer []float32
	chan0Buffer   []float32
	chan1Buffer   []float32
	newData       chan oscilloscopeData
	clearData     chan bool

	monoDim   imgui.Vec2
	stereoDim imgui.Vec2
}

func newWinOscilloscope(img *SdlImgui) (window, error) {
	win := &winOscilloscope{
		img:       img,
		newData:   make(chan oscilloscopeData, 2048),
		clearData: make(chan bool, 1),
	}
	win.reset()

	img.tv.AddAudioMixer(win)

	return win, nil
}

func (win *winOscilloscope) init() {
	win.monoDim = imgui.Vec2{X: 200 + imgui.CurrentStyle().FramePadding().X + imgui.CurrentStyle().CellPadding().X}
	win.stereoDim = imgui.Vec2{X: 100}
}

func (win *winOscilloscope) id() string {
	return winOscilloscopeID
}

func (win *winOscilloscope) isOpen() bool {
	return win.open
}

func (win *winOscilloscope) setOpen(open bool) {
	win.open = open
}

func (win *winOscilloscope) draw() {
	if !win.open {
		return
	}

	imgui.SetNextWindowPosV(imgui.Vec2{625, 567}, imgui.ConditionFirstUseEver, imgui.Vec2{0, 0})
	imgui.BeginV(win.id(), &win.open, imgui.WindowFlagsAlwaysAutoResize)
	defer imgui.End()

	imgui.PushStyleColor(imgui.StyleColorFrameBg, win.img.cols.AudioOscBg)
	imgui.PushStyleColor(imgui.StyleColorPlotLines, win.img.cols.AudioOscLine)
	defer imgui.PopStyleColorV(2)

	if win.img.audio.Prefs.Stereo.Get().(bool) {
		win.drawTVLabel("Stereo")
		imgui.PlotLinesV("##stereoL", win.stereoLBuffer, 0, "", math.MaxFloat32, math.MaxFloat32, win.stereoDim)
		imgui.SameLine()
		imgui.PlotLinesV("##stereoR", win.stereoRBuffer, 0, "", math.MaxFloat32, math.MaxFloat32, win.stereoDim)
	} else {
		imgui.AlignTextToFramePadding()
		win.drawTVLabel("Mono")
		imgui.PlotLinesV("##mono", win.monoBuffer, 0, "", math.MaxFloat32, math.MaxFloat32, win.monoDim)
	}

	imguiSeparator()

	imgui.Text("VCS Output")
	imgui.PlotLinesV("##chan0", win.chan0Buffer, 0, "", math.MaxFloat32, math.MaxFloat32, win.monoDim)
	imgui.PlotLinesV("##chan1", win.chan1Buffer, 0, "", math.MaxFloat32, math.MaxFloat32, win.monoDim)

	done := false
	ct := 0
	for !done {
		select {
		case nd := <-win.newData:
			ct++
			win.monoBuffer = append(win.monoBuffer, nd.mono)
			win.stereoLBuffer = append(win.stereoLBuffer, nd.stereoL)
			win.stereoRBuffer = append(win.stereoRBuffer, nd.stereoR)
			win.chan0Buffer = append(win.chan0Buffer, nd.chan0)
			win.chan1Buffer = append(win.chan1Buffer, nd.chan1)
		case <-win.clearData:
			win.reset()
		default:
			done = true
			win.monoBuffer = win.monoBuffer[ct:]
			win.chan0Buffer = win.chan0Buffer[ct:]
			win.chan1Buffer = win.chan1Buffer[ct:]
			win.stereoLBuffer = win.stereoLBuffer[ct:]
			win.stereoRBuffer = win.stereoRBuffer[ct:]
		}
	}
}

func (win *winOscilloscope) drawTVLabel(label string) {
	imgui.AlignTextToFramePadding()
	imgui.Text(fmt.Sprintf("TV Output (%s)", label))
	imgui.SameLine()

	enabled := win.img.prefs.audioEnabled.Get().(bool)

	var output string
	if enabled {
		output = fmt.Sprintf("%c", fonts.AudioEnabled)
	} else {
		output = fmt.Sprintf("%c muted", fonts.AudioDisabled)
	}

	imgui.PushStyleColor(imgui.StyleColorButton, win.img.cols.Transparent)
	imgui.PushStyleColor(imgui.StyleColorButtonActive, win.img.cols.Transparent)
	imgui.PushStyleColor(imgui.StyleColorButtonHovered, win.img.cols.Transparent)
	defer imgui.PopStyleColorV(3)

	if imgui.Button(output) {
		win.img.prefs.audioEnabled.Set(!enabled)
	}
}

// SetAudio implements protocol.AudioMixer.
func (win *winOscilloscope) SetAudio(sig []signal.SignalAttributes) error {
	for _, s := range sig {
		if s&signal.AudioUpdate != signal.AudioUpdate {
			continue
		}

		v0 := uint8((s & signal.AudioChannel0) >> signal.AudioChannel0Shift)
		v1 := uint8((s & signal.AudioChannel1) >> signal.AudioChannel1Shift)
		m := mix.Mono(v0, v1)

		sep := win.img.audio.Prefs.Separation.Get().(int)
		s0, s1 := mix.Stereo(v0, v1, sep)

		nd := oscilloscopeData{
			mono:    float32(m) / 256,
			chan0:   float32(v0) / 256,
			chan1:   float32(v1) / 256,
			stereoL: float32(s0) / 256,
			stereoR: float32(s1) / 256,
		}

		select {
		case win.newData <- nd:
		default:
		}
	}
	return nil
}

// EndMixing implements protocol.AudioMixer.
func (win *winOscilloscope) EndMixing() error {
	return nil
}

// Reset implements protocol.AudioMixer.
//
// Should not be called by the GUI gorountine. Use winAudio.reset() instead.
func (win *winOscilloscope) Reset() {
	select {
	case win.clearData <- true:
	default:
		// if we don't succeed in not sending the clear signal it's not the end
		// of the world. the signal that has been queued will do the job for us
	}
}

func (win *winOscilloscope) reset() {
	win.monoBuffer = make([]float32, 2048)
	win.chan0Buffer = make([]float32, 2048)
	win.chan1Buffer = make([]float32, 2048)
	win.stereoLBuffer = make([]float32, 2048)
	win.stereoRBuffer = make([]float32, 2048)
}