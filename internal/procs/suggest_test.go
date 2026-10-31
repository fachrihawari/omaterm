package procs

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSuggest(t *testing.T) {
	tests := []struct {
		dir  string
		want []Def
	}{
		{"vite", []Def{
			{Name: "dev", Command: "pnpm run dev", Port: 5173},
			{Name: "preview", Command: "pnpm run preview", Port: 5173},
		}},
		{"bun", []Def{
			{Name: "start", Command: "bun run start", Port: 4000},
		}},
		{"procfile", []Def{
			{Name: "web", Command: "bin/rails s"},
			{Name: "js", Command: "yarn build --watch"},
		}},
		{"makefile", []Def{
			{Name: "dev", Command: "make dev"},
		}},
		{"laravel", []Def{
			{Name: "serve", Command: "php artisan serve", Port: 8000},
			{Name: "queue", Command: "php artisan queue:listen"},
		}},
		{"go", []Def{
			{Name: "go", Command: "go run ."},
		}},
		{"solo", []Def{
			{Name: "dev", Command: "npm run dev", Port: 3000},
			{Name: "web", Command: "npm run dev", Env: map[string]string{"PORT": "3000"}, Watch: []string{"src/**"}},
			{Name: "worker", Command: "go run ./cmd/worker"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			got := Suggest(filepath.Join("testdata", "suggest", tt.dir))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSuggestEmpty(t *testing.T) {
	if defs := Suggest(t.TempDir()); defs != nil {
		t.Errorf("got %v for an empty folder", defs)
	}
}
