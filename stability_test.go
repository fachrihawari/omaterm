package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"omaterm/internal/crash"
)

// Regression tests of the stability work (0.9.2 on): each names the bug
// it keeps fixed.

// A crash a past run left shows a banner until dismissed.
func TestCrashBanner(t *testing.T) {
	a := newTestApp(t)
	defer stopTermPanes(a)
	a.crashes = []crash.Report{{Name: "app", Path: "/nonexistent/crash-app.log"}}
	tr := ui.NewTester(a.view, 1440, 900)
	tr.Frame()
	if !tr.HasText("omaterm quit unexpectedly last time. Its report can help fix it.") {
		t.Fatal("no crash banner")
	}
	if err := tr.Click("Dismiss"); err != nil {
		t.Fatal(err)
	}
	tr.Frame()
	if len(a.crashes) != 0 || tr.HasText("Show Report") {
		t.Fatal("the banner stayed after Dismiss")
	}
}
