package chat

import (
	"strings"
	"testing"

	"github.com/gookit/goutil/x/assert"
)

// TestBannerApprovalMode: a start-up tier adds one banner line (trust is spelled
// out), no tier leaves the banner as it was; zh and en have the same keys.
func TestBannerApprovalMode(t *testing.T) {
	text := func(info BannerInfo) string {
		var b strings.Builder
		for _, line := range BuildBanner(info) {
			b.WriteString(line.Text + "\n")
		}
		return b.String()
	}
	t.Run("no tier", func(t *testing.T) {
		assert.Eq(t, 3, len(BuildBanner(BannerInfo{})))
		assert.StrNotContains(t, text(BannerInfo{}), "审批档位")
	})
	t.Run("trust", func(t *testing.T) {
		lines := BuildBanner(BannerInfo{ApprovalMode: "trust"})
		assert.Eq(t, 4, len(lines))
		assert.StrContains(t, lines[3].Text, "审批档位：trust")
	})
	t.Run("ask and auto name the tier", func(t *testing.T) {
		for _, tier := range []string{"ask", "auto"} {
			assert.StrContains(t, text(BannerInfo{ApprovalMode: tier}), "审批档位："+tier)
		}
	})
	t.Run("english catalog", func(t *testing.T) {
		tr := enTranslator(t)
		assert.StrContains(t, text(BannerInfo{ApprovalMode: "trust", Translator: tr}), "Approval: trust")
		assert.StrContains(t, text(BannerInfo{ApprovalMode: "auto", Translator: tr}), "Approval: auto")
	})
}
