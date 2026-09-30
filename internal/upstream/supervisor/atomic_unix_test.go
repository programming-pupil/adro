//go:build !windows

package supervisor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/adro-project/adro/internal/upstream/scan"
)

func TestSaveAtomicReplacesPrivateRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.json")
	if err := os.WriteFile(path, []byte("old-long-record"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, record := range [][]byte{[]byte("new"), {}, bytes.Repeat([]byte("x"), 1<<20)} {
		if err := SaveAtomic(path, record, scan.SyncParent); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("record permissions: %v %v", info, err)
		}
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, record) {
			t.Fatalf("record replacement: length=%d err=%v", len(data), err)
		}
		assertNoTemps(t, dir)
	}
}

func TestSaveAtomicReadersObserveOnlyCompleteRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "proc.json")
	first, second := bytes.Repeat([]byte("a"), 256<<10), bytes.Repeat([]byte("b"), 512<<10)
	if err := SaveAtomic(path, first, scan.SyncParent); err != nil {
		t.Fatal(err)
	}
	var readers sync.WaitGroup
	stop := make(chan struct{})
	failures := make(chan error, 4)
	ready := make(chan struct{}, 4)
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			firstRead := true
			for {
				select {
				case <-stop:
					return
				default:
					data, err := os.ReadFile(path)
					if err != nil || (!bytes.Equal(data, first) && !bytes.Equal(data, second)) {
						if firstRead {
							ready <- struct{}{}
						}
						failures <- fmt.Errorf("partial record: length=%d err=%v", len(data), err)
						return
					}
					if firstRead {
						ready <- struct{}{}
						firstRead = false
					}
				}
			}
		}()
	}
	for i := 0; i < 4; i++ {
		<-ready
	}
	var writeErr error
	for i := 0; i < 20 && writeErr == nil; i++ {
		record := first
		if i%2 == 0 {
			record = second
		}
		writeErr = SaveAtomic(path, record, scan.SyncParent)
	}
	close(stop)
	readers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	assertNoTemps(t, dir)
}
