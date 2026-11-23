package sessiontransfer

import (
	"fmt"
	"os"
)

func checkExportBudget(sources map[string]string, maxBytes int64) error {
	if maxBytes <= 0 || maxBytes > MaxBytes() {
		return fmt.Errorf("invalid native copy budget")
	}
	var total int64
	for _, path := range sources {
		info, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() < 0 {
			return fmt.Errorf("native source is not a regular file")
		}
		// Avoid overflow even for a sparse file larger than the configured budget.
		if info.Size() > maxBytes-total {
			return fmt.Errorf("This chat is too large to copy (at least %d MB, limit %d MB)", (total+info.Size())/(1<<20), maxBytes/(1<<20))
		}
		total += info.Size()
	}
	return nil
}
