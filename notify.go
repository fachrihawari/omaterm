package main

import "omaterm/internal/session"

// maybeNotify is filled in with the notification rules (Phase 2b).
func (a *appState) maybeNotify(prev, next session.Info) {}

// programNotice is a desktop notification a program sent (OSC 9, 777).
func (a *appState) programNotice(id, title, body string) {}
