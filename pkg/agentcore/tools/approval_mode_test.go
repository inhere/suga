package tools

import (
	"testing"

	"github.com/gookit/goutil/x/assert"
)

func TestParseApprovalMode(t *testing.T) {
	for _, tc := range []struct {
		in      string
		want    ApprovalMode
		wantErr bool
	}{
		{in: "", want: ApprovalModeDefault},
		{in: "ask", want: ApprovalModeAsk},
		{in: "auto", want: ApprovalModeAuto},
		{in: "trust", want: ApprovalModeTrust},
		{in: "Trust", wantErr: true},
		{in: " trust", wantErr: true},
		{in: "yolo", wantErr: true},
	} {
		t.Run("input "+tc.in, func(t *testing.T) {
			got, err := ParseApprovalMode(tc.in)
			if tc.wantErr {
				assert.Err(t, err)
				assert.Eq(t, ApprovalModeDefault, got)
				return
			}
			assert.NoErr(t, err)
			assert.Eq(t, tc.want, got)
		})
	}
}
