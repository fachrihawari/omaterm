# Agent detection rules

`manifests/*.toml` are herdr's agent detection manifests
(github.com/herdrdev/herdr, formerly github.com/ogulcancelik/herdr),
Apache-2.0; see `manifests/LICENSE`. They came to omaterm through Kero
(github.com/egoist/kero, MIT) at commit 00a334c, which vendored 13 of them.
They are unmodified.

`manifests/agy.toml`, for the Antigravity CLI, is omaterm's own (herdr has
none), written from the real screens in `testdata/agy-*.txt`.

`detect.go` and its test are Kero's (MIT, `docs/third_party/kero-LICENSE`),
and read the manifests as herdr's engine does: of the rules that match, the
one of the highest priority wins; with none, the agent is idle.

To update the rules, copy newer manifests from herdr's
`src/detect/manifests/` and run `go test ./internal/detect/`.
