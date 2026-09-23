package runtime

import (
	"context"
	"errors"
	"golang.org/x/sys/unix"
	"runtime"
)

// ResourceCounts samples this process and its filesystem only. It cannot attest
// to public network reachability or free cloud-monitor coverage.
func ResourceCounts(ctx context.Context) (map[string]int64, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var fs unix.Statfs_t
	if err := unix.Statfs("/", &fs); err != nil {
		return nil, errors.New("filesystem metrics unavailable")
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	total := int64(fs.Blocks) * int64(fs.Bsize)
	free := int64(fs.Bavail) * int64(fs.Bsize)
	used := int64(0)
	if total > 0 {
		used = (total - free) * 100 / total
	}
	return map[string]int64{"filesystem_total_bytes": total, "filesystem_available_bytes": free, "filesystem_used_percent": used, "process_heap_bytes": int64(mem.HeapAlloc), "process_goroutines": int64(runtime.NumGoroutine())}, nil
}
