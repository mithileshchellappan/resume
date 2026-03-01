//go:build !linux && !darwin

package cli

import (
	"errors"
	"os"
)

func isTerminalFile(_ *os.File) bool {
	return false
}

func setInputRawMode(_ *os.File) (func(), error) {
	return nil, errors.New("raw terminal mode unsupported on this platform")
}

func terminalWidth(_ *os.File) int {
	return 0
}
