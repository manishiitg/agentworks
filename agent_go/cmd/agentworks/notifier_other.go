//go:build !darwin

package main

import "errors"

func notifyWithIcon(string) error { return errors.New("only on macOS") }
