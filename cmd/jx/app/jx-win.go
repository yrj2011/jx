// +build windows

package app

import (
	"os"
	"syscall"

	"github.com/jenkins-x/jx/pkg/jx/cmd"
	"github.com/jenkins-x/jx/pkg/jx/cmd/clients"
)

// Run runs the command
func Run() error {
	configureTerminalForAnsiEscapes()
	cmd := cmd.NewJXCommand(clients.NewFactory(), os.Stdin, os.Stdout, os.Stderr, nil)
	return cmd.Execute()
}

const (
	// http://docs.microsoft.com/en-us/windows/console/setconsolemode
	enableProcessedOutput           = 0x1
	enableWrapAtEOLOutput           = 0x2
	enableVirtualTerminalProcessing = 0x4
)

// configureTerminalForAnsiEscapes enables the windows 10 console to translate ansi escape sequences
// requires windows 10 1511 or higher and fails gracefully on older versions (and prior releases like windows 7)
// http://docs.microsoft.com/en-us/windows/console/console-virtual-terminal-sequences
func configureTerminalForAnsiEscapes() {

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	kern32SetConsoleMode := kernel32.NewProc("SetConsoleMode")

	// stderr
	handle := syscall.Handle(os.Stderr.Fd())
	kern32SetConsoleMode.Call(uintptr(handle), enableProcessedOutput|enableWrapAtEOLOutput|enableVirtualTerminalProcessing)

	// stdout
	handle = syscall.Handle(os.Stdout.Fd())
	kern32SetConsoleMode.Call(uintptr(handle), enableProcessedOutput|enableWrapAtEOLOutput|enableVirtualTerminalProcessing)
}
