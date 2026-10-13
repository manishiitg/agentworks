package main

import (
	"errors"
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// winProcCreated is the process start time (FILETIME ticks), 0 when it cannot be read.
func winProcCreated(pid uint32) uint64 {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return 0
	}
	defer windows.CloseHandle(h)
	var created, exit, kernel, user windows.Filetime
	if windows.GetProcessTimes(h, &created, &exit, &kernel, &user) != nil {
		return 0
	}
	return uint64(created.HighDateTime)<<32 | uint64(created.LowDateTime)
}

// winProcList returns every process in one snapshot, without start times.
func winProcList() ([]procEntry, error) {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil, err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	if err = windows.Process32First(snapshot, &entry); err != nil {
		return nil, err
	}
	var out []procEntry
	for {
		out = append(out, procEntry{PID: int(entry.ProcessID), PPID: int(entry.ParentProcessID), Name: windows.UTF16ToString(entry.ExeFile[:])})
		if err = windows.Process32Next(snapshot, &entry); err != nil {
			if errors.Is(err, windows.ERROR_NO_MORE_FILES) {
				return out, nil
			}
			return nil, err
		}
	}
}

// winProcRead returns one process with its start time.
func winProcRead(pid int) (procEntry, error) {
	list, err := winProcList()
	if err != nil {
		return procEntry{}, err
	}
	for _, e := range list {
		if e.PID == pid {
			e.Created = winProcCreated(uint32(pid))
			return e, nil
		}
	}
	return procEntry{}, fmt.Errorf("process %d is not running", pid)
}
