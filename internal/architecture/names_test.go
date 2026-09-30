package architecture

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/adro-project/adro/tools/namingdeny/policy"
)

func TestNoForbiddenNames(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(root, "testkit", "golden", "naming", "denylist.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	d, loadErr := policy.Load(f)
	closeErr := f.Close()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	violations, err := policy.Scan(ctx, root, d)
	if err != nil {
		t.Fatal(err)
	}
	for _, violation := range violations {
		t.Error(violation)
	}
}
