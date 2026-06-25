package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestBinBaseName(t *testing.T) {
	orig := os.Args[0]
	defer func() { os.Args[0] = orig }()

	os.Args[0] = "/usr/local/bin/amc"
	if got := binBaseName(); got != "amc" {
		t.Errorf("binBaseName() = %q, want %q", got, "amc")
	}

	os.Args[0] = `C:\Programs\xmc\amc.exe`
	if got := binBaseName(); got != "amc" {
		t.Errorf("binBaseName() = %q, want %q", got, "amc")
	}

	os.Args[0] = "./kmc"
	if got := binBaseName(); got != "kmc" {
		t.Errorf("binBaseName() = %q, want %q", got, "kmc")
	}
}

func TestConfigDir_Unix(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix-only test")
	}
	dir, err := xmcConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".xmc")
	if dir != want {
		t.Errorf("xmcConfigDir() = %q, want %q", dir, want)
	}
}

func TestDerivedPaths(t *testing.T) {
	orig := os.Args[0]
	defer func() { os.Args[0] = orig }()
	os.Args[0] = "/usr/local/bin/amc"

	cfg, err := configFilePath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(cfg) != "amc.yml" {
		t.Errorf("configFilePath() base = %q, want %q", filepath.Base(cfg), "amc.yml")
	}

	sh, err := shellHistoryPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(sh) != "amc-sh.log" {
		t.Errorf("shellHistoryPath() base = %q, want %q", filepath.Base(sh), "amc-sh.log")
	}

}
