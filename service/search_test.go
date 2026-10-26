package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A search with more matches than it keeps returns: rg, left writing into
// a full pipe, blocked it forever.
func TestSearchStopsAtMax(t *testing.T) {
	if !HasRipgrep() {
		t.Skip("no rg")
	}
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < MaxMatches*3; i++ {
		b.WriteString("needle needle needle " + strings.Repeat("x", 200) + "\n")
	}
	os.WriteFile(filepath.Join(dir, "many.txt"), []byte(b.String()), 0o644)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	matches, err := SearchContent(ctx, dir, "needle")
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the search took %v", time.Since(start))
	}
	if len(matches) != MaxMatches {
		t.Errorf("%d matches, want %d", len(matches), MaxMatches)
	}
}
