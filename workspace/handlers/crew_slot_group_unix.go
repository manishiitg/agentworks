//go:build !windows

package handlers

import (
	"os"
	"os/user"
	"strconv"
	"syscall"

	"github.com/manishiitg/coding-agent-loop/workspace/slots"
)

// slotOwningGroup is the slot whose own group owns dir ("" when the group belongs to no assigned slot, or on any
// error). A Crew folder is group-owned by its owner's slot (mode 2770), so this names the Crew owner's slot.
func slotOwningGroup(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	assigned, err := slots.Assigned()
	if err != nil {
		return ""
	}
	for _, slot := range assigned {
		account, lookupErr := user.Lookup(slot)
		if lookupErr != nil {
			continue
		}
		if gid, convErr := strconv.ParseUint(account.Gid, 10, 32); convErr == nil && uint32(gid) == stat.Gid {
			return slot
		}
	}
	return ""
}
