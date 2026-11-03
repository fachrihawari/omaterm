package main

// Voice input on macOS, which has no recorder program: a web page the app
// serves on the loopback (filesrv.go) records with getUserMedia and web
// access's voice.js, and posts the WAV back. Only that page, of that
// origin, gets the microphone. macOS asks the user once, with
// NSMicrophoneUsageDescription from mygo.json.
//
// Unverified: built with GOOS=darwin, never run on a Mac.

import (
	"errors"
	"net/url"
	"slices"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

// pageRecording is a recording the recorder page makes in a web view.
type pageRecording struct {
	web     *mygo.WebView
	rec     *pageRecorder
	release func()
	once    sync.Once
}

// platformRecorder opens the recorder page, which starts recording as it
// loads.
func platformRecorder(a *appState) (recording, error) {
	if a.win == nil {
		return nil, errors.New("no window")
	}
	rec := newPageRecorder()
	u, release, err := mediaFiles.serveRecorder(rec)
	if err != nil {
		return nil, err
	}
	pu, err := url.Parse(u)
	if err != nil {
		release()
		return nil, err
	}
	origin := pu.Scheme + "://" + pu.Host
	v, err := a.win.NewWebView(mygo.WebViewOptions{Transparent: true})
	if err != nil {
		release()
		return nil, err
	}
	// Set before the page loads: the microphone, for this page alone.
	v.Page().SetPermissionHandler(func(req mygo.PermissionRequest) bool {
		return req.Origin == origin && len(req.Permissions) > 0 &&
			!slices.ContainsFunc(req.Permissions, func(p mygo.Permission) bool { return p != mygo.PermissionMicrophone })
	})
	v.Page().OnRenderProcessGone(func(string) { rec.finish(nil, errors.New("the recorder page crashed")) })
	if err := v.Page().LoadURL(u); err != nil {
		v.Destroy()
		release()
		return nil, err
	}
	return &pageRecording{web: v, rec: rec, release: release}, nil
}

func (r *pageRecording) Done() <-chan struct{} { return r.rec.done }

// view is the web view, which the header places (dictationChip).
func (r *pageRecording) view() ui.NativeWebView { return r.web }

// close ends the page, and its serving.
func (r *pageRecording) close() {
	r.once.Do(func() {
		r.web.Destroy()
		r.release()
	})
}

// Stop has the page end the recording and send it.
func (r *pageRecording) Stop() ([]byte, error) {
	defer r.close()
	go r.web.Page().Eval("void window.voiceStop?.()")
	select {
	case <-r.rec.done:
	case <-time.After(15 * time.Second):
		return nil, errors.New("the recording didn't end")
	}
	return r.rec.wav, r.rec.err
}

// Cancel has the page drop the recording, and closes it.
func (r *pageRecording) Cancel() {
	r.web.Page().Eval("void window.voiceCancel?.()")
	r.close()
}
