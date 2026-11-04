package publication

import "os"

// MultipleLinks lets other cross-person exporters use the same hardlink rule
// as publication.Build: a selected folder must not expose an outside inode.
func MultipleLinks(file *os.File, info os.FileInfo) (bool, error) {
	return publicationMultipleLinks(file, info)
}
