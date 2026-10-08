package filemetadata

import "os"

// macOS grants are owned by the signed-in CLI user; Unix ownership/mode is
// preserved here. Server slot-account ACL preservation is Linux-specific.
func preserveACL(dst, src *os.File) error { return nil }
