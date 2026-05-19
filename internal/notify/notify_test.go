package notify

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNotifyMacOS(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping macOS-only test")
	}

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
	tmpDir := t.TempDir()
	var stubName string
	switch runtime.GOOS {
	case "darwin":
		stubName = "osascript"
	case "linux":
		stubName = "notify-send"
	default:
		t.Skip("unsupported platform")
	}
	stubPath := filepath.Join(tmpDir, stubName)
	os.WriteFile(stubPath, []byte("#!/bin/sh\n"), 0755)
	t.Setenv("PATH", tmpDir+":"+os.Getenv("PATH"))

	Notify("test", "test body")
}
