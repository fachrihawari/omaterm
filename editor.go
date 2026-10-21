package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"oterm/service"
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
		data, err := os.ReadFile(abs)
		if err != nil {
			return
		}
		doc := &openFile{Path: path}
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
				doc.State = &editor.State{Buffer: buf, Theme: omaEditorTheme()}
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
			preview := hasSuffix(path, ".md") || hasSuffix(path, ".mdx")
			doc.State = &editor.State{Buffer: buf, Theme: omaEditorTheme()}
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
		return ui.Column(c).Fill().Background(colCodeBg).Center().Children(func() {
			ui.Text(c, "No file open").FontSize(13).TextColor(colMuted)
			ui.Text(c, keyLabels("file.quickOpen")+" to open a file").FontSize(11).TextColor(colMuted2).Margin(4, 0, 0, 0)
		})
	}
	if doc.Image != nil && (doc.State == nil || doc.Preview) {
		return a.imageView(c, doc)
	}
	if doc.Binary {
		return ui.Column(c).Fill().Background(colCodeBg).Children(func() {
			ui.Row(c).Height(34).AlignItems(ui.Center).Padding(0, 12).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
				ui.Text(c, doc.Path).FontSize(10).TextColor(colText2).SingleLine().Shrink(0)
			})
			ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
				ui.Icon(c, iconFileCode).Width(28).Height(28).TextColor(colMuted2)
				ui.Text(c, pathBase(doc.Path)).FontSize(13).TextColor(colText2)
				ui.Text(c, "Binary file · "+service.FormatBytes(uint64(doc.Size))+" · preview not available").
					FontSize(11).TextColor(colMuted)
			})
		})
	}

	return ui.Column(c).Fill().Background(colCodeBg).Children(func() {
		// Breadcrumb + actions.
		ui.Row(c).Height(34).AlignItems(ui.Center).Padding(0, 12).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			parts := strings.Split(doc.Path, string(filepath.Separator))
			for i, part := range parts {
				if i > 0 {
					ui.Icon(c, iconChevronRight).Width(12).Height(12).TextColor(colMuted2)
				}
				col := colMuted
				if i == len(parts)-1 {
					col = colText2
				}
				ui.Text(c, part).FontSize(10).TextColor(col).SingleLine().Shrink(0)
			}
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
					Tooltip(label + " (" + keyLabels("markdown.togglePreview") + ")").Clicked() {
					doc.Preview = !doc.Preview
				}
			}
			if save := cmdButton(c, iconSave, iconBtn, false).
				Tooltip("Save (" + keyLabels("file.save") + ")"); save.Clicked() {
				a.saveEditor(doc, c)
			}
			cmdButton(c, iconSplit, iconBtn, false)
			cmdButton(c, iconMore, iconBtn, false)
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
		ui.Row(c).Height(26).AlignItems(ui.Center).Padding(0, 12).Gap(16).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			pos := doc.State.CursorPos()
			ui.Text(c, "Ln "+itoa(pos.Line+1)+", Col "+itoa(pos.Col+1)).FontSize(10).TextColor(colMuted)
			ui.Text(c, "UTF-8").FontSize(10).TextColor(colMuted)
			ui.Spacer(c)
			if doc.State.Buffer.Modified() {
				ui.Text(c, "●").FontSize(10).TextColor(colYellow)
			}
			lang := ""
			if l := doc.State.Buffer.Language(); l != nil {
				lang = l.Name
			}
			ui.Text(c, lang).FontSize(10).TextColor(colMuted)
		})
	})
}

// saveEditor writes the buffer back to disk.
func (a *appState) saveEditor(doc *openFile, c *ui.Context) {
	abs := filepath.Join(a.workspace, doc.Path)
	text := doc.State.Buffer.String()
	if err := os.WriteFile(abs, []byte(text), 0o644); err != nil {
		c.Toast("Save failed: " + err.Error())
		return
	}
	doc.State.Buffer.MarkSaved()
	c.Toast("Saved " + doc.Path)
	a.refreshActiveRepo()
}

// imageView shows a picture the way an image viewer does: the file's path
// above, the picture scaled to fit the pane, and its format, dimensions
// and size below. There is nothing to edit, so the save action is gone.
func (a *appState) imageView(c *ui.Context, doc *openFile) ui.Element {
	img := doc.Image
	return ui.Column(c).Fill().Background(colCodeBg).Children(func() {
		ui.Row(c).Height(34).AlignItems(ui.Center).Padding(0, 12).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
			parts := strings.Split(doc.Path, string(filepath.Separator))
			for i, part := range parts {
				if i > 0 {
					ui.Icon(c, iconChevronRight).Width(12).Height(12).TextColor(colMuted2)
				}
				col := colMuted
				if i == len(parts)-1 {
					col = colText2
				}
				ui.Text(c, part).FontSize(10).TextColor(col).SingleLine().Shrink(0)
			}
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
					ui.Icon(c, iconImage).Width(28).Height(28).TextColor(colMuted2)
					ui.Text(c, "Could not decode this picture").FontSize(12).TextColor(colText2)
					ui.Text(c, pathBase(doc.Path)).FontSize(10).TextColor(colMuted)
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
			ui.Box(c).Fill().Radius(8).Border(1, colBorder).Background(ui.Hex("#0c0f13")).Padding(12).Children(func() {
				ui.Image(c, img.Pic).Fit(fit).Fill()
			})
		})

		ui.Row(c).Height(26).AlignItems(ui.Center).Padding(0, 12).Gap(16).BorderWidth(1, 0, 0, 0).BorderColor(colBorder).Children(func() {
			dims := itoa(int(img.W)) + " × " + itoa(int(img.H)) + " px"
			ui.Text(c, dims).FontSize(10).TextColor(colMuted)
			ui.Text(c, service.FormatBytes(uint64(img.Size))).FontSize(10).TextColor(colMuted)
			ui.Spacer(c)
			ui.Text(c, imageFormat(doc.Path)).FontSize(10).TextColor(colMuted)
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
			ui.Text(c, label).FontSize(9).TextColor(colMuted).LetterSpacing(1.0).
				SingleLine().Shrink(1).MinWidth(0).Ellipsis("…")
			ui.Spacer(c)
			switch {
			case side.Missing:
				ui.Text(c, "missing").FontSize(9).TextColor(colMuted2).Shrink(0)
			case side.Pic != nil:
				ui.Text(c, itoa(int(side.W))+" × "+itoa(int(side.H))+" px").
					FontSize(9).TextColor(colMuted2).SingleLine().Shrink(0)
				ui.Text(c, service.FormatBytes(uint64(side.Size))).
					FontSize(9).TextColor(colMuted2).SingleLine().Shrink(0)
			}
		})
	}
	return ui.Column(c).Grow(1).Background(colCodeBg).Children(func() {
		ui.Row(c).Height(24).Padding(0, 16).Gap(16).Background(colDiffTop).
			BorderWidth(0, 0, 1, 0).BorderColor(colBorder).Children(func() {
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
		ui.Box(c).Fill().Radius(8).Border(1, colBorder).Background(ui.Hex("#0c0f13")).Padding(12).Children(func() {
			switch {
			case side.Missing:
				ui.Column(c).Fill().Center().Gap(6).Children(func() {
					ui.Icon(c, iconImage).Width(24).Height(24).TextColor(colMuted2)
					ui.Text(c, missingLabel).FontSize(11).TextColor(colMuted)
				})
			case side.Pic == nil:
				ui.Column(c).Fill().Center().Gap(6).Children(func() {
					ui.Icon(c, iconImage).Width(24).Height(24).TextColor(colMuted2)
					ui.Text(c, "Could not decode this picture").FontSize(11).TextColor(colMuted)
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
