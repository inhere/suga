package chat

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/gookit/goutil/x/assert"
	"github.com/inhere/suga/pkg/agentcore"
	"github.com/inhere/suga/pkg/agentcore/api"
	"github.com/inhere/suga/pkg/agentcore/tasks"
)

func TestChatCommandsText(t *testing.T) {
	assert.Eq(t, `:session 显示会话
:compact 压缩上下文
:add-dir/:dirs/:rm-dir 管理附加目录
:diff [N] 显示文件改动
:code [N] 展开上一条回答的代码块
:image <路径|clear> 管理待发图片（附到下一条消息）
:queue [clear] 列出/清空排队的消息（回合中 Tab 排队，本轮结束后发送）
:checkpoints 列出写操作快照
:undo [N] [force] 回滚文件改动
:help 显示此帮助
:usage 显示本会话用量与成本
:tasks 显示后台任务
:quit/:exit 退出
!<命令> 本地执行；!!<命令> 执行并附给下一条消息
按键：Enter/Alt+Enter/Ctrl+J 提交/换行；Tab 补全命令（回合中 Tab 把该行排到本轮结束后发送）；Alt+V 贴剪贴板图片；Esc 中止本轮；Ctrl+C 清空/中止/退出；回合中 Up 撤回插话；Up/Down 浏览历史`, BuildChatHelp())
}

func TestChatUsageAndTasks(t *testing.T) {
	stats := chatStats{turns: 2, usage: agentcore.Usage{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 3, CacheWriteTokens: 1, CostUSD: .25}, session: agentcore.Usage{InputTokens: 30, OutputTokens: 8, CostUSD: .5}}
	assert.StrContains(t, BuildChatUsage(stats), "本次 2 轮 · 输入 10 / 输出 4 · 缓存读 3 / 写 1 · 成本 $0.250000")
	assert.Eq(t, chatTasksEmpty, BuildChatTasks(nil, time.Now()))
	m := tasks.NewManager(tasks.Config{Output: func(_, id string) (string, string, error) { return filepath.Join(t.TempDir(), id), "", nil }})
	info, err := m.Start(api.WithSessionID(context.Background(), "sid"), tasks.Spec{Kind: "shell", Summary: "go test", Run: func(_ context.Context, _ io.Writer, _ tasks.Reporter) (int, error) { return 0, nil }})
	assert.NoErr(t, err)
	assert.StrContains(t, BuildChatTasks(m.List("sid"), time.Now()), info.ID)
	m.Close()
}
