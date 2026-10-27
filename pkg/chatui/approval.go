package chatui

import (
	"fmt"
	"strings"
)

// ApprovalReply is the decision made from the one-line answer to an approval
// question. The wiring layer turns the two persistent replies into session rules.
type ApprovalReply uint8

const (
	ReplyDeny ApprovalReply = iota
	ReplyAllow
	ReplyAllowAlways
	ReplyDenyAlways
)

// ParseApprovalReply accepts the stable answer aliases used by chat. Empty and
// unknown answers stay deny, preserving the old fail-closed behavior.
func ParseApprovalReply(answer string) ApprovalReply {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "y", "yes":
		return ReplyAllow
	case "a", "always":
		return ReplyAllowAlways
	case "d", "deny":
		return ReplyDenyAlways
	default:
		return ReplyDeny
	}
}

// PersistReply identifies the requested persistence destination. The wiring layer
// performs the writeback after the session rule has been installed in memory.
type PersistReply uint8

const (
	PersistSession PersistReply = iota
	PersistUser
	PersistProject
)

// ParsePersistReply keeps Enter as the safe default and recognizes only the
// explicit destinations shown in the follow-up question.
func ParsePersistReply(answer string) PersistReply {
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "u", "user":
		return PersistUser
	case "p", "project":
		return PersistProject
	default:
		return PersistSession
	}
}

// ApprovalPrompt builds the compact answer line. allowAlways is false for an
// unsafe shell command: a deny scope is still useful, but an allow scope must
// never bypass the command safety matcher. Without a translator it falls back
// to the Chinese texts.
func ApprovalPrompt(tool, scopeDescription string, allowAlways bool) string {
	return ApprovalPromptWithTranslator(nil, tool, scopeDescription, allowAlways)
}

// ApprovalPromptWithTranslator builds the compact answer line with the injected
// host UI translator; the compatibility wrapper above keeps older callers in zh.
// It accepts the host's minimal translation contract (Translator).
func ApprovalPromptWithTranslator(t Translator, tool, scopeDescription string, allowAlways bool) string {
	tr := approvalTranslator(t)
	if allowAlways && scopeDescription == "" {
		return tr("approval.prompt.allow", tool)
	}
	if allowAlways {
		return tr("approval.prompt.allow_scope", tool, scopeDescription)
	}
	return tr("approval.prompt.deny_scope", tool)
}

// PersistPrompt is explicit about the safe default: an empty answer keeps the
// rule in the current session, while only u/p request a file writeback.
func PersistPrompt(action string) string { return PersistPromptWithTranslator(nil, action) }

// PersistPromptWithTranslator builds the rule writeback question using the
// injected translator while retaining the safe session-only default.
func PersistPromptWithTranslator(t Translator, action string) string {
	key := "approval.prompt.persist.allow"
	if action == "deny" {
		key = "approval.prompt.persist.deny"
	}
	return approvalTranslator(t)(key)
}

func approvalTranslator(t Translator) func(string, ...string) string {
	if t != nil {
		return func(key string, args ...string) string {
			values := make(map[string]any)
			if len(args) > 0 {
				values["tool"] = args[0]
			}
			if len(args) > 1 {
				values["scope"] = args[1]
			}
			return t.T(key, values)
		}
	}
	return func(key string, args ...string) string {
		tool, scope := "", ""
		if len(args) > 0 {
			tool = args[0]
		}
		if len(args) > 1 {
			scope = args[1]
		}
		switch key {
		case "approval.prompt.allow":
			return fmt.Sprintf("允许执行 %s？[y]是 [N]否 [a]本会话总是允许该工具：", tool)
		case "approval.prompt.allow_scope":
			return fmt.Sprintf("允许执行 %s？[y]是 [N]否 [a]本会话总是允许%s [d]本会话总是拒绝：", tool, scope)
		case "approval.prompt.deny_scope":
			return fmt.Sprintf("允许执行 %s？[y]是 [N]否 [d]本会话总是拒绝：", tool)
		case "approval.prompt.persist.allow":
			return "把这条规则写进规则文件？[u] 用户级 ~/.suga/approval-rules.yaml [N] 只本会话："
		case "approval.prompt.persist.deny":
			return "把这条规则写进规则文件？[u] 用户级 ~/.suga/approval-rules.yaml [p] 项目级 .suga/approval-rules.yaml [N] 只本会话："
		default:
			return key
		}
	}
}
