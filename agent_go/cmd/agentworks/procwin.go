package main

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"strconv"
)

// The Windows counterparts of the ps-based provider process checks. A process is identified, as on
// other systems, by its id together with its start time, so a reused id is a different process.

func winHostKey(e procEntry) string {
	sum := sha256.Sum256([]byte(strconv.Itoa(e.PID) + " " + strconv.FormatUint(e.Created, 10)))
	return hex.EncodeToString(sum[:])
}

// winHostProcess finds the provider process this command runs under and its host key.
func winHostProcess(provider string) (int, string, error) {
	e, err := walkAncestors(os.Getpid(), 8, winProcRead, func(p procEntry) bool {
		return providerProcessMatches(provider, procBaseName(p.Name))
	})
	if err != nil {
		return 0, "", err
	}
	if e.Created == 0 {
		return 0, "", errors.New("cannot read the provider process start time")
	}
	return e.PID, winHostKey(e), nil
}

// winPIDHostKey is the host key of one given provider process.
func winPIDHostKey(provider string, pid int) (string, error) {
	e, err := winProcRead(pid)
	if err != nil || !providerProcessMatches(provider, procBaseName(e.Name)) || e.Created == 0 {
		return "", errors.New("registered provider process is unavailable")
	}
	return winHostKey(e), nil
}

// winHostAlive reports whether a process with this host key still runs. A listing that cannot be
// read is not evidence of death.
func winHostAlive(provider, host string) bool {
	list, err := winProcList()
	if err != nil {
		return true
	}
	for _, e := range list {
		if !providerProcessMatches(provider, procBaseName(e.Name)) {
			continue
		}
		e.Created = winProcCreated(uint32(e.PID))
		if e.Created != 0 && winHostKey(e) == host {
			return true
		}
	}
	return false
}

// winOwnAgentAncestor names the coding agent this command runs under (for its own-chat attach).
func winOwnAgentAncestor() (string, int, error) {
	var provider string
	e, err := walkAncestors(os.Getpid(), 32, winProcRead, func(p procEntry) bool {
		provider = ownProviderExecutable(procBaseName(p.Name))
		return provider != ""
	})
	if err != nil {
		return "", 0, err
	}
	return provider, e.PID, nil
}

// winInsideCodingAgent reports whether any ancestor is a coding agent.
func winInsideCodingAgent() (bool, error) {
	_, err := walkAncestors(os.Getpid(), 32, winProcRead, func(p procEntry) bool { return isCodingAgentProcess(procBaseName(p.Name)) })
	if err == nil {
		return true, nil
	}
	if errors.Is(err, errNoProviderAncestor) {
		return false, nil
	}
	return false, err
}
