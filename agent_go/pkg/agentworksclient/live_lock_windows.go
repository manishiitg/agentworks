package agentworksclient

import "errors"

func liveFileLock(string) (func(), error) {
	return nil, errors.New("local agent delivery bridge currently requires macOS or Linux")
}
