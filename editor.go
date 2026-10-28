package main

import (
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"omaterm/service"
)

// The editor view: a real code editor of the active file, with the
// mock's breadcrumb and status line around it.

// openEditor opens path (workspace-relative) in an editor tab.
func (a *appState) openEditor(path string) {
	a.openEditorAt(path, 0)
}

// openEditorAt opens path and moves the caret to line (1-based, 0 = keep).
func (a *appState) openEditorAt(path string, line int) {
	if _, ok := a.editors[path]; !ok {
		abs := filepath.Join(a.workspace, path)
		if st, err := os.Stat(abs); err == nil && st.Size() > maxEditorSize && !isImagePath(path) {
			a.editors[path] = &openFile{Path: path, Binary: true, TooLarge: true, Size: st.Size()}
			a.focusEditorTab(path)
			return
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			// Deleted since it was listed, or unreadable: say so, rather
			// than nothing happening.
			a.toast(nil, "Can't open "+path+": "+errText(err))
			return
		}
		doc := &openFile{Path: path, Disk: sha256.Sum256(data), checked: time.Now()}
		switch {
		case isImagePath(path):
			// A picture opens in the viewer. An SVG is text too, so its
			// source stays reachable behind the preview toggle.
			img := decodeImage(path, data)
			doc.Image = &img
			doc.Size = int64(len(data))
			if !service.IsBinary(data) {
				buf := editor.NewBuffer(string(data))
				if lang := editor.LanguageFor(path); lang != nil {
					buf.SetLanguage(lang)
				}
				doc.State = a.newEditorState(buf)
				doc.Preview = true
			}
			a.editors[path] = doc
		case service.IsBinary(data):
			// Reading a PNG as text shows mojibake, and an editor full of
			// it is worse than an honest placeholder.
			doc.Binary = true
			if info, err := os.Stat(abs); err == nil {
				doc.Size = info.Size()
			}
			a.editors[path] = doc
		default:
			buf := editor.NewBuffer(string(data))
			if lang := editor.LanguageFor(path); lang != nil {
				buf.SetLanguage(lang)
			}
			preview := (hasSuffix(path, ".md") || hasSuffix(path, ".mdx")) && a.settings.Editor.MarkdownPreview
			doc.State = a.newEditorState(buf)
			doc.Preview = preview
			a.editors[path] = doc
		}
	}
	if line > 0 {
		if doc := a.editors[path]; doc != nil && doc.State != nil {
			doc.State.GoTo(editor.Pos{Line: line - 1, Col: 0})
		}
	}
	if a.win != nil {
		mygo.App.AddRecentDocument(filepath.Join(a.workspace, path))
	}
	a.focusEditorTab(path)
}

// focusEditorTab activates the tab showing path, creating one if needed.
func (a *appState) focusEditorTab(path string) {
	for i, t := range a.tabs {
		if t.Kind == tabEditor && t.Path == path {
			a.activeTab = i
			a.activeView = tabEditor
			return
		}
	}
	a.tabs = append(a.tabs, topTab{
		Kind:  tabEditor,
		Label: filepath.Base(path),
		Icon:  fileIconTS,
		Path:  path,
	})
	a.activeTab = len(a.tabs) - 1
	a.activeView = tabEditor
}

// activeEditor returns the document of the active editor tab, or nil.
func (a *appState) activeEditor() *openFile {
	if a.activeTab < 0 || a.activeTab >= len(a.tabs) {
		return nil
	}
	t := a.tabs[a.activeTab]
	if t.Kind != tabEditor {
		return nil
	}
	return a.editors[t.Path]
}

func (a *appState) editorView(c *ui.Context) ui.Element {
	doc := a.activeEditor()
	if doc == nil {
		return ui.Column(c).Fill().Background(pal.Code).Center().Children(func() {
			ui.Text(c, "No file open").FontSize(13).TextColor(pal.TextMuted)
			ui.Text(c, keyLabels("file.quickOpen")+" to open a file").FontSize(11).TextColor(pal.TextFaint).Margin(4, 0, 0, 0)
		})
	}
	if doc.Image != nil && (doc.State == nil || doc.Preview) {
		return a.imageView(c, doc)
	}
	if doc.Binary {
		return ui.Column(c).Fill().Background(pal.Code).Children(func() {
			a.viewHeader(c, func() {
				headerTitle(c, iconFileCode, pal.TextFaint, pathBase(doc.Path), pathDir(doc.Path))
			})
			ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
				ui.Icon(c, iconFileCode).Width(28).Height(28).TextColor(pal.TextFaint)
				ui.Text(c, pathBase(doc.Path)).FontSize(13).TextColor(pal.TextSoft)
				what := "Binary file · " + service.FormatBytes(uint64(doc.Size)) + " · preview not available"
				if doc.TooLarge {
					what = service.FormatBytes(uint64(doc.Size)) + " · too large to open here: use an editor of your own"
				}
				ui.Text(c, what).
					FontSize(11).TextColor(pal.TextMuted)
			})
		})
	}

	// An agent may change the file while it shows: look every 2s.
	if time.Since(doc.checked) > 2*time.Second {
		a.checkDisk(doc)
	}
	c.After(2 * time.Second)

	return ui.Column(c).Fill().Background(pal.Code).Children(func() {
		// Breadcrumb + actions.
		a.viewHeader(c, func() {
			headerTitle(c, iconFileCode, pal.Syn.FileTS, pathBase(doc.Path), pathDir(doc.Path))
			ui.Spacer(c)
			if doc.Image != nil {
				// An SVG opened in the viewer can flip to its source.
				label := "Preview"
				if doc.Preview {
					label = "Source"
				}
				if cmdButton(c, iconImage, iconBtn, false).Tooltip(label).Clicked() {
					doc.Preview = !doc.Preview
				}
			}
			isMd := hasSuffix(doc.Path, ".md") || hasSuffix(doc.Path, ".mdx")
			if isMd {
				label := "Preview"
				if doc.Preview {
					label = "Source"
				}
				if cmdButton(c, iconFileText, iconBtn, false).
					Tooltip(withKeys(label, "markdown.togglePreview")).Clicked() {
					doc.Preview = !doc.Preview
				}
			}
			if save := cmdButton(c, iconSave, iconBtn, false).
				Tooltip("Save (" + keyLabels("file.save") + ")"); save.Clicked() {
				a.saveEditor(doc, c)
			}
		})

		// The real editor, or markdown preview.
		isMd := hasSuffix(doc.Path, ".md") || hasSuffix(doc.Path, ".mdx")
		if isMd && doc.Preview {
			ui.Scroll(c).Fill().Padding(16, 24).Children(func() {
				mdOpts := &markdown.Options{}
				// A ~720 DIP measure keeps lines in the 80-char reading
				// range. The Row spans the viewport and centers the box:
				// AlignSelf alone measured from the scroll content, which
				// drifted left on a wide window.
				ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
					ui.Box(c).WidthPercent(100).MaxWidth(720).Children(func() {
						markdown.View(c, stripFrontMatter(doc.State.Buffer.String()), mdOpts)
					})
				})
			})
		} else {
			editor.View(c, doc.State).Fill()
		}

		// Status line.
		ui.Row(c).Height(26).AlignItems(ui.Center).Padding(0, 12).Gap(16).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Children(func() {
			// The preview has no caret: a position would name a line
			// nobody can see.
			if !doc.Preview {
				pos := doc.State.CursorPos()
				ui.Text(c, "Ln "+itoa(pos.Line+1)+", Col "+itoa(pos.Col+1)).FontSize(10).TextColor(pal.TextMuted)
			}
			ui.Text(c, "UTF-8").FontSize(10).TextColor(pal.TextMuted)
			ui.Spacer(c)
			if doc.State.Buffer.Modified() {
				ui.Text(c, "●").FontSize(10).TextColor(pal.Warn)
			}
			lang := ""
			if l := doc.State.Buffer.Language(); l != nil {
				lang = l.Name
			}
			ui.Text(c, lang).FontSize(10).TextColor(pal.TextMuted)
		})
	})
}

// saveEditor writes the buffer back to disk.
func (a *appState) saveEditor(doc *openFile, c *ui.Context) {
	abs := filepath.Join(a.workspace, doc.Path)
	if data, err := os.ReadFile(abs); err == nil && sha256.Sum256(data) != doc.Disk {
		// Changed on disk since it was opened, an agent's edit perhaps:
		// saving would overwrite it.
		open := true
		a.overwriteAsk, a.overwriteDoc = &open, doc
		return
	}
	a.writeEditor(doc, c)
}

// writeEditor writes the editor's text to its file, whatever is there.
func (a *appState) writeEditor(doc *openFile, c *ui.Context) {
	text := []byte(doc.State.Buffer.String())
	if err := writeFileAtomic(filepath.Join(a.workspace, doc.Path), text); err != nil {
		c.Toast("Save failed: " + err.Error())
		return
	}
	doc.State.Buffer.MarkSaved()
	doc.Disk = sha256.Sum256(text)
	c.Toast("Saved " + doc.Path)
	a.refreshActiveRepo()
}

// pictureTrouble says why a picture isn't shown.
func pictureTrouble(img *imageSide) string {
	if img != nil && img.TooBig {
		return "Too large to show: " + itoa(int(img.W)) + "×" + itoa(int(img.H)) + " pixels"
	}
	return "Could not decode this picture"
}

// maxEditorSize is the largest file the editor opens: larger, it held the
// whole of it as text, a log of gigabytes the app's memory.
const maxEditorSize = 32 << 20

// errText is an error as the user reads it: "no such file" rather than
// the path it names again.
func errText(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// writeFileAtomic replaces path's contents through a file beside it, so a
// full disk or a crash never leaves it half written or empty, as writing
// it in place did. It keeps the file's mode, and writes through a link.
func writeFileAtomic(path string, data []byte) error {
	if real, err := filepath.EvalSymlinks(path); err == nil {
		path = real
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	_, err = f.Write(data)
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp, mode)
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		os.Remove(tmp)
	}
	return err
}

// checkDisk follows changes made to an editor's file on disk: an
// unmodified editor takes them, so it never shows (or saves back) a
// stale text. A modified one keeps its text, and its save asks.
func (a *appState) checkDisk(doc *openFile) {
	if doc == nil || doc.State == nil || doc.Binary {
		return
	}
	doc.checked = time.Now()
	data, err := os.ReadFile(filepath.Join(a.workspace, doc.Path))
	if err != nil {
		return
	}
	sum := sha256.Sum256(data)
	if sum == doc.Disk || doc.State.Buffer.Modified() {
		return
	}
	doc.State.Buffer.Reset(string(data))
	doc.Disk = sum
}

// imageView shows a picture the way an image viewer does: the file's path
// above, the picture scaled to fit the pane, and its format, dimensions
// and size below. There is nothing to edit, so the save action is gone.
func (a *appState) imageView(c *ui.Context, doc *openFile) ui.Element {
	img := doc.Image
	return ui.Column(c).Fill().Background(pal.Code).Children(func() {
		a.viewHeader(c, func() {
			headerTitle(c, iconImage, pal.Ok, pathBase(doc.Path), pathDir(doc.Path))
			ui.Spacer(c)
			if doc.State != nil {
				if cmdButton(c, iconFileCode, iconBtn, false).Tooltip("Source").Clicked() {
					doc.Preview = false
				}
			}
		})

		ui.Box(c).Grow(1).Padding(16).Children(func() {
			if img.Pic == nil {
				ui.Column(c).Fill().Center().Gap(6).Children(func() {
					ui.Icon(c, iconImage).Width(28).Height(28).TextColor(pal.TextFaint)
					ui.Text(c, pictureTrouble(img)).FontSize(12).TextColor(pal.TextSoft)
					ui.Text(c, pathBase(doc.Path)).FontSize(10).TextColor(pal.TextMuted)
				})
				return
			}
			// A big picture shrinks to fit; a small one keeps its size, so
			// a 24px icon is not blown up into a blurry smear. A vector
			// scales up cleanly, so it may fill the pane.
			fit := ui.ScaleDown
			if img.Vector {
				fit = ui.Contain
			}
			ui.Box(c).Fill().Radius(8).Border(1, pal.Border).Background(pal.Code).Padding(12).Children(func() {
				ui.Image(c, img.Pic).Fit(fit).Fill()
			})
		})

		ui.Row(c).Height(26).AlignItems(ui.Center).Padding(0, 12).Gap(16).BorderWidth(1, 0, 0, 0).BorderColor(pal.Border).Children(func() {
			dims := itoa(int(img.W)) + " × " + itoa(int(img.H)) + " px"
			ui.Text(c, dims).FontSize(10).TextColor(pal.TextMuted)
			ui.Text(c, service.FormatBytes(uint64(img.Size))).FontSize(10).TextColor(pal.TextMuted)
			ui.Spacer(c)
			ui.Text(c, imageFormat(doc.Path)).FontSize(10).TextColor(pal.TextMuted)
		})
	})
}

// imageDiff shows two versions of a picture side by side, which is what a
// picture diff is: a binary patch has no lines to compare. Missing
// versions, as an untracked or deleted file, say so instead of showing a
// broken box.
func (a *appState) imageDiff(c *ui.Context, old, new imageSide) ui.Element {
	oldLabel, newLabel := a.diffSideLabels()
	sideHeader := func(label string, side imageSide) {
		ui.Row(c).Grow(1).MinWidth(0).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Text(c, label).FontSize(9).TextColor(pal.TextMuted).LetterSpacing(1.0).
				SingleLine().Shrink(1).MinWidth(0).Ellipsis("…")
			ui.Spacer(c)
			switch {
			case side.Missing:
				ui.Text(c, "missing").FontSize(9).TextColor(pal.TextFaint).Shrink(0)
			case side.Pic != nil:
				ui.Text(c, itoa(int(side.W))+" × "+itoa(int(side.H))+" px").
					FontSize(9).TextColor(pal.TextFaint).SingleLine().Shrink(0)
				ui.Text(c, service.FormatBytes(uint64(side.Size))).
					FontSize(9).TextColor(pal.TextFaint).SingleLine().Shrink(0)
			}
		})
	}
	return ui.Column(c).Grow(1).Background(pal.Code).Children(func() {
		ui.Row(c).Height(24).Padding(0, 16).Gap(16).Background(pal.Sidebar).
			BorderWidth(0, 0, 1, 0).BorderColor(pal.Border).Children(func() {
			sideHeader(oldLabel, old)
			sideHeader(newLabel, new)
		})
		ui.Row(c).Grow(1).Children(func() {
			imageDiffSide(c, old, "Not in "+oldLabel)
			imageDiffSide(c, new, "Not in "+newLabel)
		})
	})
}

func imageDiffSide(c *ui.Context, side imageSide, missingLabel string) {
	ui.Box(c).Grow(1).MinWidth(0).FillHeight().Padding(12).Children(func() {
		ui.Box(c).Fill().Radius(8).Border(1, pal.Border).Background(pal.Code).Padding(12).Children(func() {
			switch {
			case side.Missing:
				ui.Column(c).Fill().Center().Gap(6).Children(func() {
					ui.Icon(c, iconImage).Width(24).Height(24).TextColor(pal.TextFaint)
					ui.Text(c, missingLabel).FontSize(11).TextColor(pal.TextMuted)
				})
			case side.Pic == nil:
				ui.Column(c).Fill().Center().Gap(6).Children(func() {
					ui.Icon(c, iconImage).Width(24).Height(24).TextColor(pal.TextFaint)
					ui.Text(c, pictureTrouble(&side)).FontSize(11).TextColor(pal.TextMuted)
				})
			default:
				fit := ui.ScaleDown
				if side.Vector {
					fit = ui.Contain
				}
				ui.Image(c, side.Pic).Fit(fit).Fill()
			}
		})
	})
}

// newEditorState is a file editor over buf, as the settings have it.
func (a *appState) newEditorState(buf *editor.Buffer) *editor.State {
	st := &editor.State{Buffer: buf, Theme: omaEditorTheme(), OnInput: a.trackEditorChord}
	a.styleEditor(st)
	return st
}

// styleEditor gives an editor the settings' font, tab size, wrapping and
// line numbers.
func (a *appState) styleEditor(st *editor.State) {
	e := a.settings.Editor
	st.Font = a.editorFont(a.fontSize)
	st.TabSize = e.TabSize
	st.SoftWrap = e.Wrap
	st.LineNumbers = editor.LineNumbersOn
	if !e.LineNumbers {
		st.LineNumbers = editor.LineNumbersOff
	}
}
