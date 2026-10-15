package main

import (
	"os"
	"path/filepath"
)

// notificationPreviewFile marks the person's choice to keep message text out of
// notifications (they can show on a lock screen).
func notificationPreviewFile() string {
	cache, _ := os.UserCacheDir()
	return filepath.Join(cache, "agentworks", "notify-no-preview")
}

func notificationPreviewOff() bool {
	_, err := os.Stat(notificationPreviewFile())
	return err == nil
}

func setNotificationPreview(on bool) (string, error) {
	if on {
		_ = os.Remove(notificationPreviewFile())
		return "Notifications show the first line of a waiting message again.", nil
	}
	if err := os.MkdirAll(filepath.Dir(notificationPreviewFile()), 0o700); err != nil {
		return "", err
	}
	if err := os.WriteFile(notificationPreviewFile(), []byte("off\n"), 0o600); err != nil {
		return "", err
	}
	return "Notifications now say only who is waiting, without any message text.", nil
}
