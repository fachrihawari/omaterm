package main

// What `omaterm browser` does to a browser pane's page: go to pages, wait
// for elements, read the page, click and type as a user would, run
// scripts, take a screenshot. Ported from Kero (github.com/egoist/kero,
// MIT, docs/third_party/kero-LICENSE), automate.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"omaterm/internal/proto"
)

// browseTimeout is how long omaterm browser waits, unless told: for an
// element to show, or a page to load.
const browseTimeout = 10 * time.Second

// browse does what omaterm browser asks of a browser pane's page, and
// answers: what waits on the page waits off the UI thread. It controls the
// app, and takes the user's leave as omaterm do does.
// UI thread.
func (a *appState) browse(r proto.BrowserParams, answer func(*proto.BrowserResult, error)) {
	fail := func(format string, args ...any) { answer(nil, fmt.Errorf(format, args...)) }
	if !a.settings.RemoteControl {
		answer(nil, proto.Errorf(proto.CodeRemoteControl, "Agents can't control the app: turn on “Let Agents Control the App” in the palette"))
		return
	}
	if _, _, _, ok := a.findPane(r.ID); !ok {
		answer(nil, proto.Errorf(proto.CodeNotFound, "no pane "+r.ID))
		return
	}
	if !a.isBrowser(r.ID) {
		fail("pane %s is a terminal, not a browser", r.ID)
		return
	}
	// A pane never shown has no page yet: it gets one now, which loads
	// though no one sees it, before it is used.
	_, had := a.webs[r.ID]
	e := a.ensureWeb(r.ID)
	if e.view == nil {
		if e.err != "" {
			fail("%s", e.err)
		} else {
			fail("this window shows no pages")
		}
		return
	}
	timeout := time.Duration(r.TimeoutMS) * time.Millisecond
	if timeout <= 0 {
		timeout = browseTimeout
	}
	var loaded <-chan error
	if !had && a.paneURLs[r.ID] != proto.BlankURL || e.loading {
		// The page is still loading: what reads it waits for it.
		loaded = e.nextLoad()
	}
	view, page := e.view, e.view.Page()
	do := func(fn func() (*proto.BrowserResult, error)) {
		go func() {
			if loaded != nil {
				if err := settle(page, loaded, timeout); err != nil {
					answer(nil, err)
					return
				}
			}
			answer(fn())
		}()
	}
	eval := func(script string) {
		do(func() (*proto.BrowserResult, error) { return evalPage(page, script, timeout) })
	}
	switch r.Op {
	case "url":
		answer(&proto.BrowserResult{Text: page.URL()}, nil)
	case "title":
		answer(&proto.BrowserResult{Text: e.title}, nil)
	case "open", "back", "forward", "reload":
		switch {
		case r.Op == "open" && strings.TrimSpace(r.Text) == "":
			fail("open: which page?")
			return
		case r.Op == "back" && !page.CanGoBack():
			fail("no page to go back to")
			return
		case r.Op == "forward" && !page.CanGoForward():
			fail("no page to go forward to")
			return
		}
		loaded = nil
		done := e.nextLoad()
		switch r.Op {
		case "open":
			e.load(r.Text)
			a.tellPage(r.ID, e)
		case "back":
			page.GoBack()
		case "forward":
			page.GoForward()
		case "reload":
			page.Reload()
			e.loading = true
		}
		do(func() (*proto.BrowserResult, error) { return nil, settle(page, done, timeout) })
	case "wait":
		if r.Selector == "" && r.Text == "" {
			do(func() (*proto.BrowserResult, error) { return nil, settle(page, nil, timeout) })
			return
		}
		eval(pageScript(r, timeout))
	case "text", "html", "click", "type":
		if r.Op != "text" && r.Op != "html" && r.Selector == "" {
			fail("%s: which element?", r.Op)
			return
		}
		eval(pageScript(r, timeout))
	case "eval":
		if strings.TrimSpace(r.Text) == "" {
			fail("eval: what script?")
			return
		}
		eval(r.Text)
	case "screenshot":
		do(func() (*proto.BrowserResult, error) {
			png, err := view.CapturePage()
			switch {
			case err != nil:
				return nil, err
			case len(png) == 0:
				// A web view draws only where the window shows it.
				return nil, fmt.Errorf("pane %s is not shown, so it has no picture: show it with omaterm focus %s, then try again", r.ID, r.ID)
			}
			return &proto.BrowserResult{PNG: png}, nil
		})
	default:
		fail("no browser command %q", r.Op)
	}
}

// evalPage runs a script on the page and returns its value as text: a
// string as it is, else as JSON.
func evalPage(page *mygo.Page, script string, timeout time.Duration) (*proto.BrowserResult, error) {
	// The script's own wait ends first, with its own error.
	ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
	defer cancel()
	v, err := page.EvalContext(ctx, script)
	if err != nil {
		var js *mygo.EvalError
		if errors.As(err, &js) {
			err = errors.New(js.Message)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			err = errors.New("the page did not answer")
		}
		return nil, err
	}
	var text string
	switch v := v.(type) {
	case nil:
	case string:
		text = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		text = string(b)
	}
	return &proto.BrowserResult{Text: text}, nil
}

// nextLoad returns what the page's next load sends its end to: nil, or
// why it failed.
func (e *webEntry) nextLoad() <-chan error {
	ch := make(chan error, 1)
	e.loads = append(e.loads, ch)
	return ch
}

// loadEnded tells those waiting for the page's load that it ended.
func (e *webEntry) loadEnded(err error) {
	for _, ch := range e.loads {
		ch <- err
	}
	e.loads = nil
}

// settle waits for the page to load what it was sent to: until its load
// ends (done), failed or not, or, for a navigation that loads nothing, as
// to an anchor of the page, until it has not loaded for a moment. With
// no done, it waits for what the page loads to end.
func settle(page *mygo.Page, done <-chan error, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	start := time.Now()
	for {
		select {
		case err := <-done:
			return err
		case <-deadline.C:
			return errors.New("timed out waiting for the page to load")
		case <-tick.C:
			if page.IsLoading() {
				continue
			}
			if done == nil {
				return nil
			}
			// A navigation takes a moment to start; one that ended
			// tells, a moment after.
			if time.Since(start) > time.Second {
				select {
				case err := <-done:
					return err
				case <-time.After(200 * time.Millisecond):
					return nil
				}
			}
		}
	}
}

// pageScript is the script that does a omaterm browser command on the page:
// it waits for the element the command needs, then does as a user would,
// with the events a page listens to.
func pageScript(p proto.BrowserParams, timeout time.Duration) string {
	args, _ := json.Marshal(map[string]any{
		"op": p.Op, "selector": p.Selector, "text": p.Text, "enter": p.Enter, "timeout": timeout.Milliseconds(),
	})
	return "(" + pageOps + ")(" + string(args) + ")"
}

// pageOps does the commands on the page. Clicks go as a mouse's would:
// pointer and mouse events down and up, the focus, then the click, which
// follows links and checks boxes. Typing goes as the keyboard's would,
// through the editing command, which sends the input events frameworks
// listen to; where that types nothing, the field's own value setter, which
// React's tracking sees past, and an input event.
const pageOps = `async (a) => {
  const deadline = Date.now() + a.timeout;
  const pause = () => new Promise((r) => setTimeout(r, 50));
  const visible = (el) => el.checkVisibility ? el.checkVisibility() : el.getClientRects().length > 0;
  const find = async (shown) => {
    for (;;) {
      const el = document.querySelector(a.selector);
      if (el && (!shown || visible(el))) return el;
      if (Date.now() > deadline) throw new Error((el ? "no visible element matches " : "no element matches ") + a.selector);
      await pause();
    }
  };
  const root = () => document.body || document.documentElement;
  const fire = (el, type, init) => el.dispatchEvent(new Event(type, { bubbles: true, cancelable: true, composed: true, ...init }));
  const mouse = (el, type, init) => {
    const r = el.getBoundingClientRect();
    const o = { bubbles: true, cancelable: true, composed: true, view: window, button: 0,
      clientX: r.left + r.width / 2, clientY: r.top + r.height / 2, ...init };
    if (type.startsWith("pointer")) return el.dispatchEvent(new PointerEvent(type, { pointerId: 1, pointerType: "mouse", isPrimary: true, ...o }));
    return el.dispatchEvent(new MouseEvent(type, o));
  };
  const key = (el, type) => el.dispatchEvent(new KeyboardEvent(type, { key: "Enter", code: "Enter", keyCode: 13, which: 13,
    charCode: type === "keypress" ? 13 : 0, bubbles: true, cancelable: true, composed: true }));
  const field = (el) => el instanceof HTMLInputElement || el instanceof HTMLTextAreaElement;

  switch (a.op) {
  case "text":
    return (a.selector ? await find() : root()).innerText;
  case "html": {
    if (a.selector) return (await find()).outerHTML;
    const dt = document.doctype ? "<!DOCTYPE " + document.doctype.name + ">\n" : "";
    return dt + document.documentElement.outerHTML;
  }
  case "click": {
    const el = await find(true);
    if (el.disabled) throw new Error(a.selector + " is disabled");
    el.scrollIntoView({ block: "center", inline: "center" });
    mouse(el, "pointerover"); mouse(el, "mouseover");
    mouse(el, "pointerdown", { buttons: 1 });
    if (mouse(el, "mousedown", { buttons: 1 })) {
      const f = el.closest("a[href], button, input, select, textarea, summary, [tabindex], [contenteditable]");
      if (f && f.focus) f.focus({ preventScroll: true });
    }
    mouse(el, "pointerup"); mouse(el, "mouseup");
    mouse(el, "click", { detail: 1 });
    return;
  }
  case "type": {
    const el = await find(true);
    if (el.disabled || el.readOnly) throw new Error(a.selector + " takes no typing");
    el.scrollIntoView({ block: "center" });
    el.focus({ preventScroll: true });
    if (el instanceof HTMLSelectElement) {
      const o = [...el.options].find((o) => o.value === a.text || o.text.trim() === a.text);
      if (!o) throw new Error(a.selector + " has no option " + JSON.stringify(a.text));
      el.value = o.value;
      fire(el, "input"); fire(el, "change");
    } else if (field(el)) {
      try { el.select(); } catch {}
      const typed = a.text === "" ? document.execCommand("delete") : document.execCommand("insertText", false, a.text);
      if (!typed || el.value !== a.text) {
        Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), "value").set.call(el, a.text);
        el.dispatchEvent(new InputEvent("input", { bubbles: true, composed: true, inputType: "insertText", data: a.text }));
      }
      fire(el, "change");
    } else if (el.isContentEditable) {
      const range = document.createRange();
      range.selectNodeContents(el);
      const sel = getSelection();
      sel.removeAllRanges(); sel.addRange(range);
      if (!document.execCommand("insertText", false, a.text)) {
        el.textContent = a.text;
        el.dispatchEvent(new InputEvent("input", { bubbles: true, composed: true, inputType: "insertText", data: a.text }));
      }
    } else {
      throw new Error(a.selector + " is no field to type in");
    }
    if (a.enter) {
      const at = document.activeElement || el;
      // Some engines (WebKitGTK) submit a form on the Enter key's events
      // themselves: the form is submitted only when they did not.
      const form = el instanceof HTMLInputElement ? el.form : null;
      let submitted = false;
      const mark = () => { submitted = true; };
      if (form) form.addEventListener("submit", mark, true);
      if (key(at, "keydown")) {
        key(at, "keypress");
        if (form) {
          if (!submitted) form.requestSubmit ? form.requestSubmit() : form.submit();
        } else if (el instanceof HTMLTextAreaElement || el.isContentEditable) {
          document.execCommand("insertLineBreak");
        }
      }
      key(at, "keyup");
      if (form) form.removeEventListener("submit", mark, true);
    }
    return;
  }
  case "wait": {
    if (a.selector) await find(true);
    if (!a.text) return;
    for (;;) {
      const el = a.selector ? document.querySelector(a.selector) : root();
      if (el && el.innerText.includes(a.text)) return;
      if (Date.now() > deadline) throw new Error("no " + JSON.stringify(a.text) + " on the page");
      await pause();
    }
  }
  }
}`
