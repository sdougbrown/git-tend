package notify

import (
	"fmt"
	"os/exec"
	"runtime"
)

func Notify(title, body string) {
	switch runtime.GOOS {
	case "darwin":
		script := fmt.Sprintf(`display notification %q with title %q`, body, title)
		exec.Command("osascript", "-e", script).Run()
	case "linux":
		exec.Command("notify-send", title, body).Run()
	}
}
