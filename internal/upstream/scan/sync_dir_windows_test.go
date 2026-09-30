//go:build windows

package scan_test

import (
	"testing"

	"github.com/adro-project/adro/core/errs"
	"github.com/adro-project/adro/internal/upstream/scan"
)

func TestSyncParentDoesNotClaimUnsupportedDurability(t *testing.T) {
	if err := scan.SyncParent("record"); errs.KindOf(err) != errs.KindUnsupported {
		t.Fatalf("unsupported directory sync: %v", err)
	}
}
