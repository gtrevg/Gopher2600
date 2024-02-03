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
	"image"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/image/draw"

	"github.com/inkyblackness/imgui-go/v4"
	"github.com/jetsetilly/gopher2600/cartridgeloader"
	"github.com/jetsetilly/gopher2600/hardware/television/specification"
	"github.com/jetsetilly/gopher2600/logger"
	"github.com/jetsetilly/gopher2600/properties"
	"github.com/jetsetilly/gopher2600/thumbnailer"
)

const winSelectROMID = "Select ROM"

type winSelectROM struct {
	playmodeWin
	debuggerWin

	img *SdlImgui

	currPath string
	entries  []os.DirEntry
	err      error

	selectedFile     string
	selectedFileBase string
	loader           cartridgeloader.Loader
	properties       properties.Entry
	propertiesOpen   bool

	showAllFiles bool
	showHidden   bool

	scrollToTop  bool
	centreOnFile bool

	// height of options line at bottom of window. valid after first frame
	controlHeight float32

	thmb        *thumbnailer.Anim
	thmbTexture texture

	thmbImage      *image.RGBA
	thmbDimensions image.Point

	// the return channel from the emulation goroutine for the property lookup
	// for the selected cartridge
	propertyResult chan properties.Entry
}

func newSelectROM(img *SdlImgui) (window, error) {
	win := &winSelectROM{
		img:            img,
		showAllFiles:   false,
		showHidden:     false,
		scrollToTop:    true,
		centreOnFile:   true,
		propertyResult: make(chan properties.Entry, 1),
	}
	win.debuggerGeom.noFousTracking = true

	var err error

	// it is assumed in the polling routines that if the file rom selector is
	// open then the thumbnailer is open. if we ever decide that the thumbnailer
	// should be optional we should change this - we don't want the polling to
	// be high if there is no reason
	win.thmb, err = thumbnailer.NewAnim(win.img.dbg.VCS().Env.Prefs)
	if err != nil {
		return nil, fmt.Errorf("debugger: %w", err)
	}

	win.thmbTexture = img.rnd.addTexture(textureColor, true, true)
	win.thmbImage = image.NewRGBA(image.Rect(0, 0, specification.ClksVisible, specification.AbsoluteMaxScanlines))
	win.thmbDimensions = win.thmbImage.Bounds().Size()

	return win, nil
}

func (win *winSelectROM) init() {
}

func (win winSelectROM) id() string {
	return winSelectROMID
}

func (win *winSelectROM) setOpen(open bool) {
	if open {
		var err error
		var p string

		// open at the most recently selected ROM
		f := win.img.dbg.Prefs.RecentROM.String()
		if f == "" {
			p, err = os.Getwd()
			if err != nil {
				logger.Logf("sdlimgui", err.Error())
			}
		} else {
			p = filepath.Dir(f)
		}

		// set path and selected file
		err = win.setPath(p)
		if err != nil {
			logger.Logf("sdlimgui", "error setting path (%s)", p)
		}
		win.setSelectedFile(f)

		return
	}

	// end thumbnail emulation
	win.thmb.EndCreation()
}

func (win *winSelectROM) playmodeSetOpen(open bool) {
	win.playmodeWin.playmodeSetOpen(open)
	win.centreOnFile = true
	win.setOpen(open)

	// set centreOnFile to true, ready for next time window is open
	if !open {
		win.centreOnFile = true
	}
}

func (win *winSelectROM) playmodeDraw() bool {
	win.render()

	if !win.playmodeOpen {
		return false
	}

	posFlgs := imgui.ConditionAppearing
	winFlgs := imgui.WindowFlagsNoSavedSettings | imgui.WindowFlagsAlwaysAutoResize

	imgui.SetNextWindowPosV(imgui.Vec2{75, 75}, posFlgs, imgui.Vec2{0, 0})

	if imgui.BeginV(win.playmodeID(win.id()), &win.playmodeOpen, winFlgs) {
		win.draw()
	}

	win.playmodeWin.playmodeGeom.update()
	imgui.End()

	return true
}

func (win *winSelectROM) debuggerSetOpen(open bool) {
	win.debuggerWin.debuggerSetOpen(open)
	win.centreOnFile = true
	win.setOpen(open)

	// set centreOnFile to true, ready for next time window is open
	if !open {
		win.centreOnFile = true
	}
}

func (win *winSelectROM) debuggerDraw() bool {
	win.render()

	if !win.debuggerOpen {
		return false
	}

	posFlgs := imgui.ConditionFirstUseEver
	winFlgs := imgui.WindowFlagsAlwaysAutoResize

	imgui.SetNextWindowPosV(imgui.Vec2{75, 75}, posFlgs, imgui.Vec2{0, 0})

	if imgui.BeginV(win.debuggerID(win.id()), &win.debuggerOpen, winFlgs) {
		win.draw()
	}

	win.debuggerWin.debuggerGeom.update()
	imgui.End()

	return true
}

func (win *winSelectROM) render() {
	// receive new thumbnail data and copy to texture
	select {
	case newImage := <-win.thmb.Render:
		if newImage != nil {
			// clear image
			for i := 0; i < len(win.thmbImage.Pix); i += 4 {
				s := win.thmbImage.Pix[i : i+4 : i+4]
				s[0] = 10
				s[1] = 10
				s[2] = 10
				s[3] = 255
			}

			// copy new image so that it is centred in the thumbnail image
			sz := newImage.Bounds().Size()
			y := ((win.thmbDimensions.Y - sz.Y) / 2)
			draw.Copy(win.thmbImage, image.Point{X: 0, Y: y},
				newImage, newImage.Bounds(), draw.Over, nil)

			// render image
			win.thmbTexture.render(win.thmbImage)
		}
	default:
	}
}

func (win *winSelectROM) draw() {
	// check for new property information
	select {
	case win.properties = <-win.propertyResult:
	default:
	}

	// reset centreOnFile at end of draw
	defer func() {
		win.centreOnFile = false
	}()

	if imgui.Button("Parent") {
		d := filepath.Dir(win.currPath)
		err := win.setPath(d)
		if err != nil {
			logger.Logf("sdlimgui", "error setting path (%s)", d)
		}
		win.scrollToTop = true
	}

	imgui.SameLine()
	imgui.Text(win.currPath)

	if imgui.BeginTable("romSelector", 2) {
		imgui.TableSetupColumnV("filelist", imgui.TableColumnFlagsWidthStretch, -1, 0)
		imgui.TableSetupColumnV("thumbnail", imgui.TableColumnFlagsWidthStretch, -1, 1)

		imgui.TableNextRow()
		imgui.TableNextColumn()

		height := imgui.WindowHeight() - imgui.CursorPosY() - win.controlHeight - imgui.CurrentStyle().FramePadding().Y*2 - imgui.CurrentStyle().ItemInnerSpacing().Y
		imgui.BeginChildV("##selector", imgui.Vec2{X: 300, Y: height}, true, 0)

		if win.scrollToTop {
			imgui.SetScrollY(0)
			win.scrollToTop = false
		}

		// list directories
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.ROMSelectDir)
		for _, f := range win.entries {
			// ignore dot files
			if !win.showHidden && f.Name()[0] == '.' {
				continue
			}

			fi, err := os.Stat(filepath.Join(win.currPath, f.Name()))
			if err != nil {
				continue
			}

			if fi.Mode().IsDir() {
				s := strings.Builder{}
				s.WriteString(f.Name())
				s.WriteString(" [dir]")

				if imgui.Selectable(s.String()) {
					d := filepath.Join(win.currPath, f.Name())
					err = win.setPath(d)
					if err != nil {
						logger.Logf("sdlimgui", "error setting path (%s)", d)
					}
					win.scrollToTop = true
				}
			}
		}
		imgui.PopStyleColor()

		// list files
		imgui.PushStyleColor(imgui.StyleColorText, win.img.cols.ROMSelectFile)
		for _, f := range win.entries {
			// ignore dot files
			if !win.showHidden && f.Name()[0] == '.' {
				continue
			}

			fi, err := os.Stat(filepath.Join(win.currPath, f.Name()))
			if err != nil {
				continue
			}

			// ignore invalid file extensions unless showAllFiles flags is set
			ext := strings.ToUpper(filepath.Ext(fi.Name()))
			if !win.showAllFiles {
				hasExt := false
				for _, e := range cartridgeloader.FileExtensions {
					if e == ext {
						hasExt = true
						break
					}
				}
				if !hasExt {
					continue // to next file
				}
			}

			if fi.Mode().IsRegular() {
				selected := f.Name() == win.selectedFileBase

				if selected && win.centreOnFile {
					imgui.SetScrollHereY(0.0)
				}

				if imgui.SelectableV(f.Name(), selected, 0, imgui.Vec2{0, 0}) {
					win.setSelectedFile(filepath.Join(win.currPath, f.Name()))
				}
				if imgui.IsItemHovered() && imgui.IsMouseDoubleClicked(0) {
					win.insertCartridge()
				}
			}
		}
		imgui.PopStyleColor()

		imgui.EndChild()

		imgui.TableNextColumn()
		imgui.Image(imgui.TextureID(win.thmbTexture.getID()),
			imgui.Vec2{float32(win.thmbDimensions.X) * 2, float32(win.thmbDimensions.Y)})

		imgui.EndTable()
	}

	// control buttons. start controlHeight measurement
	win.controlHeight = imguiMeasureHeight(func() {
		func() {
			if !win.thmb.IsEmulating() {
				imgui.PushItemFlag(imgui.ItemFlagsDisabled, true)
				imgui.PushStyleVarFloat(imgui.StyleVarAlpha, disabledAlpha)
				defer imgui.PopItemFlag()
				defer imgui.PopStyleVar()
			}

			imgui.SetNextItemOpen(win.propertiesOpen, imgui.ConditionAlways)
			if !imgui.CollapsingHeaderV(win.selectedFileBase, imgui.TreeNodeFlagsNone) {
				win.propertiesOpen = false
			} else {
				win.propertiesOpen = true
				if win.properties.IsValid() {
					if imgui.BeginTable("#properties", 2) {
						imgui.TableSetupColumnV("#category", imgui.TableColumnFlagsWidthFixed, -1, 0)
						imgui.TableSetupColumnV("#detail", imgui.TableColumnFlagsWidthFixed, -1, 1)

						imgui.TableNextRow()
						imgui.TableNextColumn()
						imgui.Text("Name")
						imgui.TableNextColumn()
						imgui.Text(win.properties.Name)

						if win.properties.Manufacturer != "" {
							imgui.TableNextRow()
							imgui.TableNextColumn()
							imgui.Text("Manufacturer")
							imgui.TableNextColumn()
							imgui.Text(win.properties.Manufacturer)
						}
						if win.properties.Rarity != "" {
							imgui.TableNextRow()
							imgui.TableNextColumn()
							imgui.Text("Rarity")
							imgui.TableNextColumn()
							imgui.Text(win.properties.Rarity)
						}
						if win.properties.Model != "" {
							imgui.TableNextRow()
							imgui.TableNextColumn()
							imgui.Text("Model")
							imgui.TableNextColumn()
							imgui.Text(win.properties.Model)
						}

						if win.properties.Note != "" {
							imgui.TableNextRow()
							imgui.TableNextColumn()
							imgui.Text("Note")
							imgui.TableNextColumn()
							imgui.Text(win.properties.Note)
						}

						imgui.EndTable()
					}
				} else {
					imgui.Text("No information")
				}
			}
		}()

		imguiSeparator()

		// imgui.Checkbox("Show all files", &win.showAllFiles)
		// imgui.SameLine()
		// imgui.Checkbox("Show hidden files", &win.showHidden)

		// imgui.Spacing()

		if imgui.Button("Cancel") {
			// close rom selected in both the debugger and playmode
			win.debuggerSetOpen(false)
			win.playmodeSetOpen(false)
		}

		if win.selectedFile != "" {

			var s string

			// load or reload button
			if win.selectedFile == win.img.cache.VCS.Mem.Cart.Filename {
				s = fmt.Sprintf("Reload %s", win.selectedFileBase)
			} else {
				s = fmt.Sprintf("Load %s", win.selectedFileBase)
			}

			// only show load cartridge button if the file is being
			// emulated by the thumbnailer. if it's not then that's a good
			// sign that the file isn't supported
			if win.thmb.IsEmulating() {
				imgui.SameLine()
				if imgui.Button(s) {
					win.insertCartridge()
				}
			}
		}
	})
}

func (win *winSelectROM) insertCartridge() {
	// do not try to load cartridge if the file is not being emulated by the
	// thumbnailer. if it's not then that's a good sign that the file isn't
	// supported
	if !win.thmb.IsEmulating() {
		return
	}

	win.img.dbg.InsertCartridge(win.selectedFile)

	// close rom selected in both the debugger and playmode
	win.debuggerSetOpen(false)
	win.playmodeSetOpen(false)

	// tell thumbnailer to stop emulating
	win.thmb.EndCreation()
}

func (win *winSelectROM) setPath(path string) error {
	var err error
	win.currPath = filepath.Clean(path)
	win.entries, err = os.ReadDir(win.currPath)
	if err != nil {
		return err
	}
	win.setSelectedFile("")
	return nil
}

func (win *winSelectROM) setSelectedFile(filename string) {
	// do nothing if this file has already been selected
	if win.selectedFile == filename {
		return
	}

	// update selected file. return immediately if the filename is empty
	win.selectedFile = filename
	if filename == "" {
		win.selectedFileBase = ""
		return
	}

	// base filename for presentation purposes
	win.selectedFileBase = filepath.Base(filename)

	var err error

	// create cartridge loader and start thumbnail emulation
	win.loader, err = cartridgeloader.NewLoader(filename, "AUTO")
	if err != nil {
		logger.Logf("ROM Select", err.Error())
		return
	}

	// push function to emulation goroutine. result will be checked for in
	// draw() function
	if err := win.loader.Load(); err == nil {
		win.img.dbg.PushPropertyLookup(win.loader.HashMD5, win.propertyResult)
	}

	// create thumbnail animation
	win.thmb.Create(win.loader, thumbnailer.UndefinedNumFrames)
}
