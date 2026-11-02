package common

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mitchellh/go-homedir"
	"github.com/spf13/viper"
)

func TestGetHomePathCreatesDir(t *testing.T) {
	// override HOME to a temp dir
	tmp, err := os.MkdirTemp("", "ocf_home_test")
	if err != nil {
		t.Fatal(err)
	}
	old := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", old) })
	_ = os.Setenv("HOME", tmp)

	p := GetHomePath()
	if !strings.HasSuffix(p, ".ocfcore") {
		t.Fatalf("expected .ocfcore path, got %s", p)
	}
	if st, err := os.Stat(p); err != nil || !st.IsDir() {
		t.Fatalf("expected directory to be created: %v, %v", st, err)
	}
}

func TestGetDBPath(t *testing.T) {
	// fake HOME
	tmp, _ := os.MkdirTemp("", "ocf_home_test2")
	old := os.Getenv("HOME")
	t.Cleanup(func() { _ = os.Setenv("HOME", old) })
	_ = os.Setenv("HOME", tmp)

	db := GetDBPath("node123")
	if !strings.Contains(db, filepath.Join(".ocfcore", "ocfcore.node123.db")) {
		t.Fatalf("unexpected db path: %s", db)
	}
}

func TestGetDBPathHonorsDatadir(t *testing.T) {
	// A shared $HOME must not collect peer databases: --datadir moves them.
	viper.Reset()
	t.Cleanup(viper.Reset)
	dir := t.TempDir()
	viper.Set("datadir", dir)

	db := GetDBPath("node123")
	if db != filepath.Join(dir, "ocfcore", "ocfcore.node123.db") {
		t.Fatalf("unexpected db path: %s", db)
	}
	if st, err := os.Stat(filepath.Join(dir, "ocfcore")); err != nil || !st.IsDir() {
		t.Fatalf("expected directory to be created: %v, %v", st, err)
	}
}

func TestDataDirExpandsTilde(t *testing.T) {
	// datadir from cfg.yaml or OF_DATADIR reaches us unexpanded; "~/x" must
	// not become a literal "./~/x" under the working directory.
	viper.Reset()
	t.Cleanup(viper.Reset)
	home, err := homedir.Dir()
	if err != nil {
		t.Fatalf("homedir: %v", err)
	}

	viper.Set("datadir", "~/scratch/otela")
	if got, want := DataDir(), filepath.Join(home, "scratch", "otela"); got != want {
		t.Fatalf("DataDir() = %q, want %q", got, want)
	}
	viper.Set("datadir", "")
	if got := DataDir(); got != "" {
		t.Fatalf("unset datadir: DataDir() = %q, want empty", got)
	}
}
