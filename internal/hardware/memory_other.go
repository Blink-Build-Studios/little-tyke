//go:build !darwin && !linux

package hardware

import "fmt"

func totalMemoryBytes() (uint64, error) {
	return 0, fmt.Errorf("memory detection not supported on %s", "this platform")
}
