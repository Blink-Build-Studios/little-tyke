package hardware

import (
	"fmt"
	"syscall"
	"unsafe"
)

func totalMemoryBytes() (uint64, error) {
	mib := []int32{6 /* CTL_HW */, 24 /* HW_MEMSIZE */}
	var size uint64
	bufLen := unsafe.Sizeof(size)

	_, _, errno := syscall.Syscall6(
		syscall.SYS___SYSCTL,
		uintptr(unsafe.Pointer(&mib[0])),
		uintptr(len(mib)),
		uintptr(unsafe.Pointer(&size)),
		uintptr(unsafe.Pointer(&bufLen)),
		0, 0,
	)
	if errno != 0 {
		return 0, fmt.Errorf("sysctl HW_MEMSIZE: %w", errno)
	}
	return size, nil
}
