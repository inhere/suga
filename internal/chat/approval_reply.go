package chat

import (
	"github.com/inhere/suga/internal/i18n"
	"github.com/inhere/suga/pkg/chatui"
)

// ApprovalReply and PersistReply remain chat aliases for compatibility; the
// reusable parsing and prompt component lives in pkg/chatui.
type ApprovalReply = chatui.ApprovalReply

const (
	ReplyDeny        = chatui.ReplyDeny
	ReplyAllow       = chatui.ReplyAllow
	ReplyAllowAlways = chatui.ReplyAllowAlways
	ReplyDenyAlways  = chatui.ReplyDenyAlways
)

func ParseApprovalReply(answer string) ApprovalReply { return chatui.ParseApprovalReply(answer) }

type PersistReply = chatui.PersistReply

const (
	PersistSession = chatui.PersistSession
	PersistUser    = chatui.PersistUser
	PersistProject = chatui.PersistProject
)

func ParsePersistReply(answer string) PersistReply { return chatui.ParsePersistReply(answer) }

func ApprovalPrompt(tool, scopeDescription string, allowAlways bool) string {
	return chatui.ApprovalPrompt(tool, scopeDescription, allowAlways)
}

func ApprovalPromptWithTranslator(t i18n.Translator, tool, scopeDescription string, allowAlways bool) string {
	return chatui.ApprovalPromptWithTranslator(t, tool, scopeDescription, allowAlways)
}

func PersistPrompt(action string) string { return chatui.PersistPrompt(action) }
func PersistPromptWithTranslator(t i18n.Translator, action string) string {
	return chatui.PersistPromptWithTranslator(t, action)
}
