package filesnapshots

import (
	"fmt"
	"strings"
)

// fast-import accepts Git's byte-oriented C quoting, not Go's \u escapes.
// Quote non-ASCII bytes as octal too, preserving the exact filesystem name.
func gitQuotePath(path string) string {
	var out strings.Builder
	out.WriteByte('"')
	for i := 0; i < len(path); i++ {
		c := path[i]
		switch c {
		case '\\', '"':
			out.WriteByte('\\')
			out.WriteByte(c)
		default:
			if c < 0x20 || c >= 0x7f {
				fmt.Fprintf(&out, "%c%03o", '\\', c)
			} else {
				out.WriteByte(c)
			}
		}
	}
	out.WriteByte('"')
	return out.String()
}
