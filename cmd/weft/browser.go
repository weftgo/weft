package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
)

// stdoutIsTTY reports whether stdout is a terminal: --open's default.
// A variable so tests pin it off.
var stdoutIsTTY = func() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// openURL hands link to the platform's opener. A variable so no test
// ever opens a browser (TestMain swaps it for a recorder).
var openURL = func(link string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", link)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", link)
	default:
		cmd = exec.Command("xdg-open", link)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	// The opener returns at once (it hands the link to the browser);
	// reap it so it never lingers as a zombie.
	go func() { _ = cmd.Wait() }()
	return nil
}

// openBrowser opens link; a failure is one line, never an exit — the
// link is printed (or the banner's) and the operator can open it.
func openBrowser(stdout io.Writer, link string) {
	if err := openURL(link); err != nil {
		_, _ = fmt.Fprintf(stdout, "weft: could not open a browser (%v); open the link yourself\n", err)
	}
}
