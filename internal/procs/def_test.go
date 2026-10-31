package procs

import "testing"

func TestVariant(t *testing.T) {
	base := Def{Name: "web", Command: "npm run dev", Env: map[string]string{"A": "1", "B": "2"}, Watch: []string{"src/**", "go.mod"}}
	same := []Def{
		{Name: "dev", Command: "npm run dev", Env: map[string]string{"B": "2", "A": "1"}, Watch: []string{"go.mod", "src/**"}},
		{Name: "web", Command: "npm run dev", Env: map[string]string{"A": "1", "B": "2"}, Watch: []string{"src/**", "go.mod"}, Autostart: true},
		{Name: "web", Command: "npm run dev", Env: map[string]string{"A": "1", "B": "2"}, Watch: []string{"src/**", "go.mod"}, Local: true},
	}
	for _, d := range same {
		if d.Variant() != base.Variant() {
			t.Errorf("%+v has another variant than the base", d)
		}
	}
	unset := Def{Command: "make", Restart: ""}
	never := Def{Command: "make", Restart: "never"}
	if unset.Variant() != never.Variant() {
		t.Error("restart unset and never have different variants")
	}
	if never.Variant() == (Def{Command: "make", Restart: "always"}).Variant() {
		t.Error("restart never and always have the same variant")
	}
	changed := base
	changed.Command = "npm start"
	if changed.Variant() == base.Variant() {
		t.Error("a changed command has the same variant")
	}
}

func TestVariantEmptyLists(t *testing.T) {
	none := Def{Command: "make"}
	empty := Def{Command: "make", Env: map[string]string{}, Watch: []string{}}
	if none.Variant() != empty.Variant() {
		t.Error("an empty env or watch list differs from none")
	}
}

func TestValidate(t *testing.T) {
	root := t.TempDir()
	good := Def{Name: "web", Command: "npm run dev", Dir: "web", Port: 3000, Restart: "on-failure"}
	if err := good.Validate(root); err != nil {
		t.Fatalf("a good def: %v", err)
	}
	bad := map[string]Def{
		"bad name":      {Name: "Web App", Command: "x"},
		"long name":     {Name: "a234567890123456789012345678901234", Command: "x"},
		"empty command": {Name: "web", Command: "  "},
		"dir escapes":   {Name: "web", Command: "x", Dir: "../x"},
		"dir absolute":  {Name: "web", Command: "x", Dir: "/etc"},
		"port too big":  {Name: "web", Command: "x", Port: 70000},
		"port negative": {Name: "web", Command: "x", Port: -1},
		"restart value": {Name: "web", Command: "x", Restart: "sometimes"},
	}
	for what, d := range bad {
		if err := d.Validate(root); err == nil {
			t.Errorf("%s: no error", what)
		}
	}
}
