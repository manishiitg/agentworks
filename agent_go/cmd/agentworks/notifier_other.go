//go:build !darwin && !windows

package main

import "errors"

func notifyWithIcon(string) error { return errors.New("only on macOS") }

// removeNotifierApp: there is no notifier app outside macOS.
func removeNotifierApp() (string, error) {
	return "AgentWorks adds no notifier app on this system; nothing to remove.", nil
}

func restoreNotifierApp() (string, error) {
	return "AgentWorks adds no notifier app on this system.", nil
}
