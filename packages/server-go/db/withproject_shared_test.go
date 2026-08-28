package db

import (
	"sync"
	"testing"
	"time"
)

// Under a pinned ENGINE_STATE_DIR every project resolves to one database
// file, so WithProject must not hold the swap mutex for the duration of fn:
// two callers on different projects run concurrently.
func TestWithProjectSharedStateDirRunsConcurrently(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", t.TempDir())
	globalDBMu.Lock()
	if globalDB != nil {
		_ = globalDB.Close()
		globalDB = nil
		globalDBPath = ""
	}
	globalDBMu.Unlock()

	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	for _, p := range []string{"/proj/a", "/proj/b"} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			_ = WithProject(p, func() error {
				started <- struct{}{}
				<-release
				return nil
			})
		}(p)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatalf("WithProject serialized under a shared state dir: only %d of 2 callers entered fn", i)
		}
	}
	close(release)
	wg.Wait()
}

// Without a pinned dir, different projects are different files and the
// swap must still be exclusive.
func TestWithProjectPerProjectStillSerializes(t *testing.T) {
	t.Setenv("ENGINE_STATE_DIR", "")
	a, b := t.TempDir(), t.TempDir()
	inside := 0
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, p := range []string{a, b, a, b} {
		wg.Add(1)
		go func(p string) {
			defer wg.Done()
			_ = WithProject(p, func() error {
				mu.Lock()
				inside++
				if inside > 1 {
					t.Errorf("two swapped runs inside fn at once")
				}
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
		}(p)
	}
	wg.Wait()
}
