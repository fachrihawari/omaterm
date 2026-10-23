package main

import "omaterm/internal/model"

// newID returns a short random id for a project, session or pane.
func newID() string { return model.RandomID() }
