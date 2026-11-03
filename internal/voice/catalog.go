// Package voice turns speech into text for web access: the phone records,
// and whisper.cpp's whisper-server, run beside the session daemon, writes
// down what was said. The engine and its models are downloaded from
// Settings › Voice, each checked against a SHA-256 pinned here.
//
// Voice is off until the user turns it on: while off, nothing downloads and
// no whisper-server starts.
package voice

import (
	"path/filepath"
	"runtime"
)

// Settings are settings.json's "voice": the app writes them, the daemon
// reads them.
type Settings struct {
	Enabled bool `json:"enabled"`
	// Model is the name of the model in use, of Models.
	Model string `json:"model"`
	// Language is the phone's default: a code of Languages, "auto" for
	// whisper to tell.
	Language string `json:"language"`
}

// DefaultModel and DefaultLanguage are the settings' defaults.
const (
	DefaultModel    = "small"
	DefaultLanguage = "auto"
)

// Model is one of whisper.cpp's models, from Hugging Face.
type Model struct {
	// Name is its name in the settings, Title in Settings › Voice.
	Name, Title, File string
	// Size is its size in bytes, as Hugging Face lists it.
	Size   int64
	SHA256 string
	// Note says what it is good for, in a few words.
	Note string
}

// modelRevision is the commit of huggingface.co/ggerganov/whisper.cpp
// the models are downloaded at, so a URL always gives the bytes pinned.
const modelRevision = "5359861c739e955e79d9a303bcbc70fb988958b1"

// Models are the models offered, smallest first. All are multilingual:
// one covers every language, so a language is a setting, not a download.
// The SHA-256s are Hugging Face's LFS ids for these files.
var Models = []Model{
	{Name: "tiny", Title: "Tiny", File: "ggml-tiny.bin", Size: 77691713,
		SHA256: "be07e048e1e599ad46341c8d2a135645097a538221678b7acdd1b1919c6e1b21", Note: "Fastest, rough outside English"},
	{Name: "base", Title: "Base", File: "ggml-base.bin", Size: 147951465,
		SHA256: "60ed5bc3dd14eea856493d334349b405782ddcaf0028d4b5df4088345fba2efe", Note: "Fast, fine for short English prompts"},
	{Name: "small", Title: "Small", File: "ggml-small.bin", Size: 487601967,
		SHA256: "1be3a9b2063867b937e64e2ec7483364a79917e157fa98c5d94b5c1fffea987b", Note: "Recommended: good Indonesian and English"},
	{Name: "large-v3-turbo-q5_0", Title: "Large v3 Turbo", File: "ggml-large-v3-turbo-q5_0.bin", Size: 574041195,
		SHA256: "394221709cd5ad1f40c46e6031ca61bce88931e6e088c188294c6d5a55ffa7e2", Note: "Best, slower on a laptop"},
}

// URL is where the model is downloaded from.
func (m Model) URL() string {
	return "https://huggingface.co/ggerganov/whisper.cpp/resolve/" + modelRevision + "/" + m.File
}

// ModelByName finds a model of Models.
func ModelByName(name string) (Model, bool) {
	for _, m := range Models {
		if m.Name == name {
			return m, true
		}
	}
	return Model{}, false
}

// EngineVersion is the whisper.cpp tag the engine builds are of: the
// release whisper-<EngineVersion> that .github/workflows/whisper.yml
// makes.
const EngineVersion = "v1.9.5"

// Engine is a build of whisper-server for one system, a tar.gz holding
// the one executable.
type Engine struct {
	OS, Arch string
	SHA256   string
	// Size is about how big the download is, for the button.
	Size int64
}

// Engines are the builds published. Their SHA-256s are filled in from
// the release's SHA256SUMS once whisper.yml has run; until then they are
// empty, and Download refuses an empty one, so nothing unchecked ever
// runs: whisper-server on the PATH works meanwhile.
var Engines = []Engine{
	{OS: "linux", Arch: "amd64", SHA256: "" /* PLACEHOLDER: pin from SHA256SUMS of whisper-v1.9.5 */, Size: 6 << 20},
	{OS: "linux", Arch: "arm64", SHA256: "" /* PLACEHOLDER: pin from SHA256SUMS of whisper-v1.9.5 */, Size: 6 << 20},
	{OS: "darwin", Arch: "arm64", SHA256: "" /* PLACEHOLDER: pin from SHA256SUMS of whisper-v1.9.5 */, Size: 8 << 20},
	{OS: "darwin", Arch: "amd64", SHA256: "" /* PLACEHOLDER: pin from SHA256SUMS of whisper-v1.9.5 */, Size: 6 << 20},
}

// Asset is the build's file name on the release.
func (e Engine) Asset() string { return "whisper-server-" + e.OS + "-" + e.Arch + ".tar.gz" }

// URL is where the build is downloaded from.
func (e Engine) URL() string {
	return "https://github.com/fachrihawari/omaterm/releases/download/whisper-" + EngineVersion + "/" + e.Asset()
}

// Published tells whether the build's checksum is pinned, which a
// download needs.
func (e Engine) Published() bool { return e.SHA256 != "" }

// EngineForHost is the build for this system, if there is one.
func EngineForHost() (Engine, bool) {
	for _, e := range Engines {
		if e.OS == runtime.GOOS && e.Arch == runtime.GOARCH {
			return e, true
		}
	}
	return Engine{}, false
}

// Language is a language whisper knows, for the pickers.
type Language struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

// Languages are offered in the pickers: Auto, then those most asked for,
// then the rest whisper does well, by name. The codes are whisper's
// (Javanese is "jw").
var Languages = []Language{
	{"auto", "Auto"},
	{"id", "Indonesian"}, {"en", "English"}, {"jw", "Javanese"}, {"su", "Sundanese"}, {"ms", "Malay"},
	{"ar", "Arabic"}, {"zh", "Chinese"}, {"nl", "Dutch"}, {"tl", "Filipino"}, {"fr", "French"},
	{"de", "German"}, {"hi", "Hindi"}, {"it", "Italian"}, {"ja", "Japanese"}, {"ko", "Korean"},
	{"pl", "Polish"}, {"pt", "Portuguese"}, {"ru", "Russian"}, {"es", "Spanish"}, {"th", "Thai"},
	{"tr", "Turkish"}, {"uk", "Ukrainian"}, {"vi", "Vietnamese"},
}

// KnownLanguage tells whether code is one of Languages.
func KnownLanguage(code string) bool {
	for _, l := range Languages {
		if l.Code == code {
			return true
		}
	}
	return false
}

// Dir is where the engine and models are kept, under the state folder.
func Dir(stateDir string) string { return filepath.Join(stateDir, "voice") }

// ModelPath is where a model is kept in dir.
func ModelPath(dir string, m Model) string { return filepath.Join(dir, m.File) }

// EnginePath is where the downloaded whisper-server is kept in dir: not
// in the state folder's bin/, which panes have on their PATH.
func EnginePath(dir string) string { return filepath.Join(dir, "bin", "whisper-server") }
