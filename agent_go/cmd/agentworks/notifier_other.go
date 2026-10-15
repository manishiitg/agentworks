//go:build !darwin && !windows

package main

import "errors"

func notifyWithIcon(string) error { return errors.New("only on macOS") }
