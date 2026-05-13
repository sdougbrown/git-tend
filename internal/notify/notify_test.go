package notify

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNotifyMacOS(t *testing.T) {
	tmpDir := t.TempDir()
	stubPath := filepath.Join(tmpDir, "osascript")
	calledPath := filepath.Join(tmpDir, "called")
	script := "#!/bin/sh\necho \"$@\" > " + calledPath + "\n"
	os.WriteFile(stubPath, []byte(script), 0755)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	Notify("test title", "test body")

	data, err := os.ReadFile(calledPath)
	if err != nil {
		t.Fatal("osascript stub was not called")
	}
	t.Logf("osascript called with: %s", string(data))
}

func TestNotifyDoesNotCrash(t *testing.T) {
	Notify("test", "test body")
}
