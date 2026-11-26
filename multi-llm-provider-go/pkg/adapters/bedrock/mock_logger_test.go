package bedrock

import "fmt"

// MockLogger is a stdout-only logger used by the package's tests. It
// satisfies the interfaces.Logger contract minimally so tests don't have
// to import a real logger implementation.
type MockLogger struct{}

func (l *MockLogger) Infof(format string, args ...any)  { fmt.Printf("INFO: "+format+"\n", args...) }
func (l *MockLogger) Errorf(format string, args ...any) { fmt.Printf("ERROR: "+format+"\n", args...) }
func (l *MockLogger) Debugf(format string, args ...interface{}) {
	fmt.Printf("DEBUG: "+format+"\n", args...)
}
