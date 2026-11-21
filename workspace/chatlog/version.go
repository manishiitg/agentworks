package chatlog

import (
	"errors"
	"os"
	"reflect"
)

var ErrSourceChanged = errors.New("conversation changed before append")

type FileVersion struct {
	Exists   bool   `json:"exists"`
	Device   uint64 `json:"device"`
	Inode    uint64 `json:"inode"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}
type Version struct {
	Header  FileVersion `json:"header"`
	History FileVersion `json:"history"`
}

// Filesystem identities are transport data, not paths or an access grant.
// Without native inode/device metadata, callers use the ordinary full merger.
func VersionFromInfos(header, history os.FileInfo) (Version, error) {
	a, err := fileVersion(header)
	if err != nil {
		return Version{}, err
	}
	b, err := fileVersion(history)
	if err != nil {
		return Version{}, err
	}
	return Version{a, b}, nil
}
func fileVersion(info os.FileInfo) (FileVersion, error) {
	if info == nil {
		return FileVersion{}, nil
	}
	stat := reflect.ValueOf(info.Sys())
	if stat.Kind() == reflect.Pointer {
		stat = stat.Elem()
	}
	if stat.Kind() != reflect.Struct {
		return FileVersion{}, errors.New("file identity unavailable")
	}
	value := func(name string) (uint64, bool) {
		f := stat.FieldByName(name)
		if f.IsValid() && f.CanUint() {
			return f.Uint(), true
		}
		if f.IsValid() && f.CanInt() {
			return uint64(f.Int()), true
		}
		return 0, false
	}
	dev, okDev := value("Dev")
	ino, okIno := value("Ino")
	if !okDev || !okIno {
		return FileVersion{}, errors.New("file identity unavailable")
	}
	return FileVersion{true, dev, ino, info.Size(), info.ModTime().UnixNano()}, nil
}
func CurrentVersion(path string) (Version, error) {
	header, err := os.Stat(path)
	if err != nil {
		return Version{}, err
	}
	history, err := os.Stat(HistoryLogPath(path))
	if err != nil && !os.IsNotExist(err) {
		return Version{}, err
	}
	return VersionFromInfos(header, history)
}
