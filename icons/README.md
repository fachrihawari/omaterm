# Icons

- `*.svg` at the top: [Lucide](https://lucide.dev) (ISC, © Lucide Contributors), as
  [Iconify](https://iconify.design) serves them: `https://api.iconify.design/lucide/<name>.svg`.
- `programs/si-*.svg`: [Simple Icons](https://simpleicons.org) (CC0), programs' logos, shown
  in pane headers and session rows; the brands are their owners' trademarks. Most came through
  Kero (MIT); more from `https://api.iconify.design/simple-icons/<slug>.svg`.

To show a program's logo: add its SVG to `programs/`, embed it in `programs.go`, and add a
line to `programIcons`.
