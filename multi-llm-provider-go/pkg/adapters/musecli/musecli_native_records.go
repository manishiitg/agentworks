package musecli

func NativeRecordPath(sessionID, dataHome string) string {
	return museSessionLogPath(sessionID, dataHome)
}
