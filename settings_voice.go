package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/daemon"
	"omaterm/internal/paths"
	"omaterm/internal/voice"
	"omaterm/service"
)

// voiceData is the Voice page's: what is downloaded, what downloads now,
// and whether whisper-server runs.
type voiceData struct {
	status voice.Status
	have   map[string]bool // the models downloaded, by name
	// disk is what the downloads take.
	disk int64
	// running is the daemon's word on whisper-server.
	running bool
	// downloads run in the background, by "engine" or a model's name.
	downloads map[string]*voiceDownload
}

// voiceDownload is a download in progress, for its bar and Cancel.
type voiceDownload struct {
	done, total int64
	cancel      context.CancelFunc
}

// voiceDir is where the engine and models are kept.
func voiceDir() string { return voice.Dir(paths.StateDir()) }

// refreshVoice reads what the page shows: the files at once, whether
// whisper-server runs from the daemon in the background.
func (a *appState) refreshVoice() {
	d := &a.settingsUI.data.voice
	dir := voiceDir()
	d.status = voice.Check(dir, a.settings.Voice)
	d.have = map[string]bool{}
	for _, m := range voice.Models {
		if fi, err := os.Stat(voice.ModelPath(dir, m)); err == nil && fi.Size() > 0 {
			d.have[m.Name] = true
		}
	}
	d.disk = 0
	filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			if fi, err := e.Info(); err == nil {
				d.disk += fi.Size()
			}
		}
		return nil
	})
	if !a.settings.Voice.Enabled {
		d.running = false
		return
	}
	go func() {
		st, err := daemon.StatusVoice()
		a.post(func() {
			a.settingsUI.data.voice.running = err == nil && st.Running
			a.invalidateWindows()
		})
	}()
}

// setVoice turns voice input on or off. Off, downloads stop, a dictation
// is dropped, and the daemon stops whisper-server; what was downloaded
// stays.
func (a *appState) setVoice(on bool) {
	a.settings.Voice.Enabled = on
	if !on {
		for _, dl := range a.settingsUI.data.voice.downloads {
			dl.cancel()
		}
		a.cancelDictation()
	}
	a.voiceChanged()
}

// voiceChanged keeps the voice settings and tells the daemon.
func (a *appState) voiceChanged() {
	a.applySettings()
	a.refreshVoice()
	go daemon.ReloadVoice()
}

// startVoiceDownload runs a download in the background, its progress on
// the page; done runs on the UI thread once it succeeds.
func (a *appState) startVoiceDownload(key, what string, run func(context.Context, voice.Progress) error, done func()) {
	d := &a.settingsUI.data.voice
	if d.downloads == nil {
		d.downloads = map[string]*voiceDownload{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	dl := &voiceDownload{cancel: cancel}
	d.downloads[key] = dl
	go func() {
		err := run(ctx, func(n, total int64) {
			a.post(func() {
				dl.done, dl.total = n, total
				a.invalidateWindows()
			})
		})
		cancel()
		a.post(func() {
			delete(a.settingsUI.data.voice.downloads, key)
			switch {
			case errors.Is(err, context.Canceled):
			case errors.Is(err, voice.ErrChecksum):
				a.settingsToast(what + " didn't download correctly: try again")
			case err != nil:
				a.settingsToast(what + " didn't download: " + err.Error())
			default:
				a.settingsToast("Downloaded " + what)
				if done != nil {
					done()
				}
			}
			a.refreshVoice()
			a.invalidateWindows()
		})
	}()
}

func (a *appState) settingsVoice(c *ui.Context) {
	if onWindows {
		a.settingsNotOnWindows(c, "voice", "Voice input isn't available on Windows yet", "It works through web access, which the session daemon serves, and that doesn't run on Windows yet.")
		return
	}
	v := &a.settings.Voice
	d := &a.settingsUI.data.voice
	if d.have == nil {
		a.refreshVoice()
	}
	a.settingsGroup(c, "voice", "", a.voiceNote(), func() {
		on := v.Enabled
		a.settingDefRow(c, "voice", func() {
			// Steady: whether it can work, not whether whisper-server
			// happens to run, which comes and goes with use.
			switch {
			case v.Enabled && d.status.Ready:
				settingsPill(c, "Ready", pillOk)
			case v.Enabled:
				settingsPill(c, "Needs downloads", pillWarn)
			}
			if a.settingsSwitch(c, "voice", &on, false) {
				if !on && len(d.downloads) > 0 {
					a.settingsUI.sheet = &settingsSheet{
						title: "Stop the downloads?", danger: true,
						text:    "Turning voice input off stops what is downloading; what is done stays.",
						confirm: "Turn Off",
						run:     func(*ui.Context) { a.setVoice(false) },
					}
				} else {
					a.setVoice(on)
				}
			}
		})
	})
	if v.Enabled {
		// What you speak first: a wrong language garbles more than a
		// small model does.
		a.settingsGroup(c, "voiceLanguage", "", "", func() {
			a.settingsRow(c, "Language", settingsRowOpts{id: "voiceLanguage", sub: "What you speak, here and on the phone: tick two, as Indonesian and English, for it to tell which. Detect Each Time can hear another one."}, func() {
				// A menu, not a popup: it takes several languages.
				name := voice.LanguageName(d.status.Language)
				b := ui.ButtonBase(c.Key("voiceLanguage")).Height(26).MaxWidth(260).Shrink(1).MinWidth(0).
					Padding(0, 8, 0, 10).Gap(6).Radius(6).AlignItems(ui.Center).Label("Language").Tooltip(name)
				face(b, pal.Raised).Border(1, pal.BorderStrong)
				b.Children(func() {
					ui.Text(c, name).FontSize(12).TextColor(pal.Text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
					ui.Icon(c, iconChevronsUpDown).Size(12, 12).TextColor(pal.TextFaint).Shrink(0)
				})
				b.Menu(func(m *ui.Menu) { a.voiceLanguageMenu(m) })
			})
		})
		a.settingsGroup(c, "voiceEngine", "Engine", "", func() { a.voiceEngineRow(c) })
		a.settingsGroup(c, "voiceModels", "Model", "Every model knows about 99 languages: the bigger, the better it hears, and the slower.", func() {
			for _, m := range voice.Models {
				a.voiceModelRow(c, m)
			}
		})
	}
	if d.disk > 0 {
		settingsActions(c, func() {
			if a.settingsButton(c, "Remove Downloads…", false, true, len(d.downloads) > 0) {
				a.voiceRemoveSheet()
			}
		})
	}
}

// voiceNote says where voice input works: in a terminal pane, with the
// key or the header's mic (not on Windows, nor without the session
// daemon), and on the phone over HTTPS.
func (a *appState) voiceNote() string {
	phone := "On your phone it works in web access over HTTPS (Tailscale or Cloudflare): a phone’s browser gives the microphone to secure pages only."
	if startRecording == nil {
		return phone
	}
	return "Here, press " + keyPrimary("voice.dictate") + " or the mic in a terminal pane’s header and speak. Enter sends what you said; " +
		keyPrimary("voice.dictate") + " again, or letting go of it after holding it, pastes it to read first; Esc drops it. " + phone
}

// voiceEngineRow says which whisper-server runs, or downloads one.
func (a *appState) voiceEngineRow(c *ui.Context) {
	d := &a.settingsUI.data.voice
	st := d.status
	e, built := voice.EngineForHost()
	dl := d.downloads["engine"]
	sub := ""
	switch {
	case dl != nil:
		sub = "Downloading whisper.cpp " + voice.EngineVersion + "…" + downloadedOf(dl)
	case st.EngineOnPATH:
		sub = "Using whisper-server from your PATH: " + st.Engine
	case st.Engine != "":
		sub = "whisper.cpp " + voice.EngineVersion + ", downloaded"
	case built && e.Published():
		// To the nearest MB: FormatBytes cuts 1.9 MB down to 1.
		sub = "whisper.cpp’s whisper-server, about " + service.FormatBytes(uint64((e.Size+1<<19)>>20<<20)) + "."
	default:
		sub = "No download for this computer yet: install whisper.cpp so that whisper-server is on your PATH."
	}
	a.settingsRow(c, "whisper-server", settingsRowOpts{id: "voiceEngine", sub: sub}, func() {
		switch {
		case dl != nil:
			a.voiceProgress(c, "engine", dl)
		case st.Engine != "":
			settingsPill(c, "Installed", pillOk)
		case built && e.Published():
			if a.settingsButton(c, "Download##engine", true, false, false) {
				a.startVoiceDownload("engine", "the engine", func(ctx context.Context, p voice.Progress) error {
					return voice.InstallEngine(ctx, e, voiceDir(), p)
				}, nil)
			}
		}
	})
}

// voiceModelRow is a model: download it, use it, or delete it.
func (a *appState) voiceModelRow(c *ui.Context, m voice.Model) {
	d := &a.settingsUI.data.voice
	v := &a.settings.Voice
	dl := d.downloads[m.Name]
	inUse := d.status.Model == m.Name
	sub := service.FormatBytes(uint64(m.Size)) + " · " + m.Note
	if dl != nil {
		sub = "Downloading…" + downloadedOf(dl)
	}
	a.settingsRow(c, m.Title, settingsRowOpts{id: "voiceModel-" + m.Name, sub: sub}, func() {
		switch {
		case dl != nil:
			a.voiceProgress(c, m.Name, dl)
		case d.have[m.Name]:
			if inUse {
				settingsPill(c, "In use", pillOk)
			} else if a.settingsButton(c, "Use##"+m.Name, false, false, false) {
				v.Model = m.Name
				a.voiceChanged()
			}
			if a.settingsButton(c, "Delete…##"+m.Name, false, true, false) {
				what := "It downloads again from here."
				if inUse {
					what = "Voice input stops until a model is downloaded again."
				}
				a.settingsUI.sheet = &settingsSheet{
					title: "Delete the " + m.Title + " model?", danger: true, text: what, confirm: "Delete",
					run: func(c *ui.Context) {
						os.Remove(voice.ModelPath(voiceDir(), m))
						a.refreshVoice()
						// A whisper-server running it holds it until told.
						go daemon.ReloadVoice()
					},
				}
			}
		default:
			if a.settingsButton(c, "Download##"+m.Name, inUse, false, false) {
				a.startVoiceDownload(m.Name, m.Title, func(ctx context.Context, p voice.Progress) error {
					return voice.Download(ctx, m.URL(), m.SHA256, voice.ModelPath(voiceDir(), m), m.Size, p)
				}, func() {
					// The first model downloaded is the one used, unless
					// the one chosen is there.
					if !a.settingsUI.data.voice.have[a.settingsUI.data.voice.status.Model] {
						v.Model = m.Name
						a.voiceChanged()
					}
				})
			}
		}
	})
}

// voiceProgress is a download's bar and its Cancel.
// downloadedOf says how far a download is, " 120 MB of 466 MB", "" before
// its size is known.
func downloadedOf(dl *voiceDownload) string {
	if dl.total <= 0 {
		return ""
	}
	return " " + service.FormatBytes(uint64(dl.done)) + " of " + service.FormatBytes(uint64(dl.total))
}

func (a *appState) voiceProgress(c *ui.Context, key string, dl *voiceDownload) {
	frac := -1.0
	if dl.total > 0 {
		frac = float64(dl.done) / float64(dl.total)
	}
	ui.Progress(c, frac).Width(96).Shrink(0)
	if a.settingsButton(c, "Cancel##"+key, false, false, false) {
		dl.cancel()
	}
}

// voiceRemoveSheet asks before removing the engine and models downloaded.
func (a *appState) voiceRemoveSheet() {
	d := &a.settingsUI.data.voice
	a.settingsUI.sheet = &settingsSheet{
		title:   "Remove the voice downloads?",
		text:    "The engine and models take " + service.FormatBytes(uint64(d.disk)) + ". Voice input stays as it is: download them again to use it.",
		confirm: "Remove",
		danger:  true,
		run: func(*ui.Context) {
			os.RemoveAll(voiceDir())
			a.refreshVoice()
			go daemon.ReloadVoice()
			a.settingsToast("Removed the voice downloads")
		},
	}
}
