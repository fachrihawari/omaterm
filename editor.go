package main

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
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
		buf := editor.NewBuffer(string(data))
		if lang := editor.LanguageFor(path); lang != nil {
			buf.SetLanguage(lang)
		}
		preview := hasSuffix(path, ".md") || hasSuffix(path, ".mdx")
		a.editors[path] = &openFile{Path: path, State: &editor.State{Buffer: buf}, Preview: preview}
	}
	if line > 0 {
		if st := a.editors[path].State; st != nil {
			st.GoTo(editor.Pos{Line: line - 1, Col: 0})
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
			ui.Text(c, "Ctrl+P to open a file").FontSize(11).TextColor(colMuted2).Margin(4, 0, 0, 0)
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
			isMd := hasSuffix(doc.Path, ".md") || hasSuffix(doc.Path, ".mdx")
			if isMd {
				label := "Preview"
				if doc.Preview {
					label = "Source"
				}
				if cmdButton(c, iconFileText, 14, false).Tooltip(label).Clicked() {
					doc.Preview = !doc.Preview
				}
			}
			if save := cmdButton(c, iconSave, 14, false); save.Tooltip("Save").Clicked() {
				a.saveEditor(doc, c)
			}
			cmdButton(c, iconSplit, 14, false)
			cmdButton(c, iconMore, 14, false)
		})

		// The real editor, or markdown preview.
		isMd := hasSuffix(doc.Path, ".md") || hasSuffix(doc.Path, ".mdx")
		if isMd && doc.Preview {
			mdOpts := &markdown.Options{}
			markdown.View(c, doc.State.Buffer.String(), mdOpts).Fill()
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
