package main

import (
	"context"
	_ "embed"
	"encoding/xml"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
)

// Windows shows a standard notification (toast) through the built-in
// PowerShell and Windows notification API, with the AgentWorks logo as its
// picture. Nothing is installed.

//go:embed assets/agentworks.png
var notifierLogo []byte

var notifierMu sync.Mutex

const toastScript = `[Windows.UI.Notifications.ToastNotificationManager, Windows.UI.Notifications, ContentType = WindowsRuntime] > $null
[Windows.Data.Xml.Dom.XmlDocument, Windows.Data.Xml.Dom.XmlDocument, ContentType = WindowsRuntime] > $null
$xml = New-Object Windows.Data.Xml.Dom.XmlDocument
$xml.LoadXml($env:AGENTWORKS_TOAST)
$toast = [Windows.UI.Notifications.ToastNotification]::new($xml)
[Windows.UI.Notifications.ToastNotificationManager]::CreateToastNotifier('{1AC14E77-02E7-4E5D-B744-2EB1AE5198B7}\WindowsPowerShell\v1.0\powershell.exe').Show($toast)`

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func notifyWithIcon(text string) error {
	notifierMu.Lock()
	defer notifierMu.Unlock()
	cache, err := os.UserCacheDir()
	if err != nil {
		return err
	}
	dir := filepath.Join(cache, "agentworks")
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logo := filepath.Join(dir, "agentworks-logo.png")
	if _, e := os.Stat(logo); e != nil {
		if err = os.WriteFile(logo, notifierLogo, 0o644); err != nil {
			return err
		}
	}
	toast := `<toast><visual><binding template="ToastGeneric"><text>AgentWorks</text><text>` + xmlText(text) +
		`</text><image placement="appLogoOverride" src="` + xmlText("file:///"+filepath.ToSlash(logo)) + `"/></binding></visual></toast>`
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command", toastScript)
	c.Env = append(os.Environ(), "AGENTWORKS_TOAST="+toast)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
	return c.Run()
}
