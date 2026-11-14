//go:build windows

package server

import (
	"errors"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

// On Windows one ReadDirectoryChangesW call over the subtree watches a copy's whole folder with one handle (fsnotify
// can do this too but keeps it switched off), so the name budget does not apply.
const crewTeamTreeWatching = true

const crewTeamWatchMask = windows.FILE_NOTIFY_CHANGE_FILE_NAME | windows.FILE_NOTIFY_CHANGE_DIR_NAME |
	windows.FILE_NOTIFY_CHANGE_SIZE | windows.FILE_NOTIFY_CHANGE_LAST_WRITE

type crewTeamTreeWatcher struct {
	dir, io, stop windows.Handle
	done          chan struct{}
}

func newCrewTeamWatcher(root string, w *crewTeamWatch) (crewTeamWatcher, error) {
	name, err := windows.UTF16PtrFromString(root)
	if err != nil {
		return nil, err
	}
	dir, err := windows.CreateFile(name, windows.FILE_LIST_DIRECTORY,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OVERLAPPED, 0)
	if err != nil {
		return nil, err
	}
	io, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(dir)
		return nil, err
	}
	stop, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		windows.CloseHandle(io)
		windows.CloseHandle(dir)
		return nil, err
	}
	t := &crewTeamTreeWatcher{dir: dir, io: io, stop: stop, done: make(chan struct{})}
	go t.read(root, w)
	return t, nil
}

func (t *crewTeamTreeWatcher) read(root string, w *crewTeamWatch) {
	defer close(t.done)
	buf := make([]byte, 64*1024)
	for {
		ov := windows.Overlapped{HEvent: t.io}
		_ = windows.ResetEvent(t.io)
		if err := windows.ReadDirectoryChanges(t.dir, &buf[0], uint32(len(buf)), true, crewTeamWatchMask, nil, &ov, 0); err != nil {
			w.overflow() // the folder can no longer be read: full scans pick it up
			return
		}
		got, err := windows.WaitForMultipleObjects([]windows.Handle{t.io, t.stop}, false, windows.INFINITE)
		if err != nil || got != windows.WAIT_OBJECT_0 {
			_ = windows.CancelIoEx(t.dir, &ov)
			var n uint32
			_ = windows.GetOverlappedResult(t.dir, &ov, &n, true)
			return
		}
		var n uint32
		if err := windows.GetOverlappedResult(t.dir, &ov, &n, false); err != nil || n == 0 {
			if errors.Is(err, windows.ERROR_OPERATION_ABORTED) {
				return
			}
			w.overflow() // ERROR_NOTIFY_ENUM_DIR or an empty result: too many changes to list
			continue
		}
		for off := uint32(0); ; {
			info := (*windows.FileNotifyInformation)(unsafe.Pointer(&buf[off]))
			chars := unsafe.Slice(&info.FileName, info.FileNameLength/2)
			rel := windows.UTF16ToString(chars)
			w.note(filepath.Join(root, rel), info.Action != windows.FILE_ACTION_MODIFIED)
			if info.NextEntryOffset == 0 {
				break
			}
			off += info.NextEntryOffset
		}
	}
}

func (t *crewTeamTreeWatcher) Add(string) error { return nil }
func (t *crewTeamTreeWatcher) Remove(string)    {}

// Close is called with the watch's lock held; the reader stops and frees its handles in the background so a change
// waiting on that lock cannot block it.
func (t *crewTeamTreeWatcher) Close() error {
	_ = windows.SetEvent(t.stop)
	go func() {
		<-t.done
		windows.CloseHandle(t.dir)
		windows.CloseHandle(t.io)
		windows.CloseHandle(t.stop)
	}()
	return nil
}
