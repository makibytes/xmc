package cmd

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func xmcConfigDir() (string, error) {
	if runtime.GOOS == "windows" {
		local := os.Getenv("LOCALAPPDATA")
		if local == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			local = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(local, "xmc"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".xmc"), nil
}

func binBaseName() string {
	name := os.Args[0]
	// Handle both Unix and Windows path separators.
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	name = strings.TrimSuffix(name, ".exe")
	if name == "" || name == "." {
		return "xmc"
	}
	return name
}

func xmcPath(suffix string) (string, error) {
	dir, err := xmcConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, binBaseName()+suffix), nil
}

func configFilePath() (string, error) {
	return xmcPath(".yml")
}

func shellHistoryPath() (string, error) {
	return xmcPath("-sh.log")
}

func askHistoryPath() (string, error) {
	return xmcPath("-ask.log")
}

func ensureXMCDir() error {
	dir, err := xmcConfigDir()
	if err != nil {
		return err
	}
	return os.MkdirAll(dir, 0o700)
}
