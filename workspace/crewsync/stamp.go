package crewsync

import (
	"io/fs"
	"time"
)

// Stamp identifies the raw file version a local cache read. It is metadata,
// never a content hash or proof that a version reached another computer.
type Stamp struct {
	Size   int64  `json:"size"`
	Mtime  int64  `json:"mtime"`
	Ctime  int64  `json:"ctime"`
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
	Mode   uint32 `json:"mode"`
}

func StampOf(info fs.FileInfo) Stamp {
	if info == nil {
		return Stamp{}
	}
	s := Stamp{Size: info.Size(), Mtime: info.ModTime().UnixNano(), Mode: uint32(info.Mode())}
	s.Ctime, s.Device, s.Inode = stampIdentity(info)
	return s
}

// Racy rejects missing OS identity and timestamps in the read's own second:
// a coarse filesystem clock can otherwise hide a same-size edit. Record the
// time before reading, and compare file/path stamps again after reading.
func (s Stamp) Racy(readStarted time.Time) bool {
	cutoff := readStarted.Truncate(time.Second).UnixNano()
	return readStarted.IsZero() || s.Inode == 0 || s.Ctime == 0 ||
		!fs.FileMode(s.Mode).IsRegular() || s.Mtime >= cutoff || s.Ctime >= cutoff
}

func (s Stamp) Reusable(current Stamp, readStarted time.Time) bool {
	return s == current && !s.Racy(readStarted)
}
