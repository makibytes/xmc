package broker

import (
	"fmt"
	"io/fs"
)

var aiDocsFS fs.FS

// RegisterAIDocs stores the embedded docs filesystem for later lookup.
// Called from main's init() before GetRootCommand().
func RegisterAIDocs(fsys fs.FS) {
	aiDocsFS = fsys
}

// AIDoc reads docs/<name>.md from the embedded filesystem and returns its
// content as a string. Returns "" if the filesystem was not registered or the
// file does not exist — AI mode degrades gracefully to mechanics-only.
func AIDoc(name string) string {
	if aiDocsFS == nil {
		return ""
	}
	data, err := fs.ReadFile(aiDocsFS, fmt.Sprintf("docs/%s.md", name))
	if err != nil {
		return ""
	}
	return string(data)
}
