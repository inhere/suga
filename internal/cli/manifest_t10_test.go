package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestManifestApprovalRulesTextIsDiagnostic(t *testing.T) {
	manifestCLIEnv(t)
	var stdout, stderr bytes.Buffer
	err := newCLI(context.Background(), strings.NewReader(""), &stdout, &stderr).RunWithArgs([]string{"inspect", "manifest"})
	assert.Require(t, assert.NoErr(t, err))
	assert.StrContains(t, stdout.String(), "Approval rules")
	assert.StrContains(t, stdout.String(), "tools.approval=ask")
	assert.StrContains(t, stdout.String(), "approval-rules.yaml")
}
