package wiring

import (
	"github.com/inhere/suga/pkg/agentcore/tools"
	"github.com/inhere/suga/pkg/agentcore/tools/native"
)

// writeToolHint says why write tools are off in this runtime and how the
// person enables them; it differs per entry (run, chat, ACP).
func writeToolHint(opts Options) string {
	switch {
	case opts.CLIMode && opts.ReadOnly:
		return "本次以 --read-only 启动，写文件与执行命令的工具不可用；去掉 --read-only 后，run 加 --allow-write，chat 在 SUGA_HOME/config.yaml 设 tools.allow_write: true"
	case opts.CLIMode && opts.ChatMode:
		return "chat 默认只读；在 SUGA_HOME/config.yaml 设 tools.allow_write: true 后重新打开 chat"
	case opts.CLIMode:
		return "suga run 默认只读；加 --allow-write 重新运行（由任务调度器拉起时，需在其 suga run 命令模板中加 --allow-write）"
	default:
		return "ACP 会话处于 read_only 模式；需由 ACP 客户端把会话模式设为 default（可写）"
	}
}

// disabledToolHints lists the catalog tools that registry lacks with the hint
// for each: write tools get the per-entry hint, web_search the missing key,
// session_message its config switch. Names outside the catalog are not listed:
// calling them is a plain
// "not found" that does not count as a denial.
func disabledToolHints(registry *tools.Registry, opts Options) map[string]string {
	hints := map[string]string{}
	for _, spec := range native.Catalog() {
		if _, err := registry.Get(spec.Name); err == nil {
			continue
		}
		if spec.Name == "web_search" {
			hints[spec.Name] = "未配置 serpapi_key；在 SUGA_HOME/config.yaml 设 serpapi_key 后可用"
			continue
		}
		if spec.Name == "session_message" {
			hints[spec.Name] = "该工具已在配置中关闭（tools.session_message.enabled / SUGA_SESSION_MESSAGE）"
			continue
		}
		// Building with an empty Env only reads the tool's own metadata.
		if tool := spec.New(native.Env{}); tool != nil && !tools.MetaOf(tool).ReadOnly {
			hints[spec.Name] = writeToolHint(opts)
		}
	}
	return hints
}
