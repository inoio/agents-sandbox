//go:build windows

package sysinfo

import (
	"math"
	"unsafe"

	"golang.org/x/sys/windows"
)

var globalMemoryStatusEx = windows.NewLazySystemDLL("kernel32.dll").NewProc("GlobalMemoryStatusEx")

// memoryStatusEx mirrors the Win32 MEMORYSTATUSEX struct.
type memoryStatusEx struct {
	Length               uint32
	MemoryLoad           uint32
	TotalPhys            uint64
	AvailPhys            uint64
	TotalPageFile        uint64
	AvailPageFile        uint64
	TotalVirtual         uint64
	AvailVirtual         uint64
	AvailExtendedVirtual uint64
}

func readMemory() (totalBytes uint64, availableBytes uint64, ok bool) {
	status := memoryStatusEx{Length: uint32(unsafe.Sizeof(memoryStatusEx{}))}
	result, _, _ := globalMemoryStatusEx.Call(uintptr(unsafe.Pointer(&status)))
	if result == 0 {
		return 0, 0, false
	}
	return status.TotalPhys, status.AvailPhys, true
}

// TotalMemoryGiB returns the installed memory in GiB on Windows.
func TotalMemoryGiB() int {
	total, _, ok := readMemory()
	if !ok {
		return 0
	}
	return int(total / (kiB * kiB * kiB))
}

// AvailableMemoryMiB returns the physical memory Windows reports available.
func AvailableMemoryMiB() (uint32, bool) {
	_, available, ok := readMemory()
	if !ok {
		return 0, false
	}
	return uint32(min(available/(kiB*kiB), uint64(math.MaxUint32))), true
}
