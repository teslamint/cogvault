package trial

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const claimLockName = ".cogvault-trial-claim.lock"

// claimLock serializes budget checks with job claims across trial processes.
// It is held only until ClaimJob records the attempt, not during the fetch.
func (s *Service) claimLock(ctx context.Context) (func(), error) {
	path := filepath.Join(s.Cfg.Root, claimLockName)
	fd, err := syscall.Open(path, syscall.O_CREAT|syscall.O_RDWR|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("trial claim lock could not be opened: %w", err)
	}
	f := os.NewFile(uintptr(fd), path)
	for {
		err = syscall.Flock(fd, syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, fmt.Errorf("trial claim lock could not be acquired: %w", err)
		}
		select {
		case <-ctx.Done():
			f.Close()
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
	return func() { _ = syscall.Flock(fd, syscall.LOCK_UN); _ = f.Close() }, nil
}
