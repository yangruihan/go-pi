package prompt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func BuildBase(cwd, osName, provider, mode string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	mode = strings.ToLower(strings.TrimSpace(mode))
	if provider == "" {
		provider = "ollama"
	}
	if mode == "" {
		mode = "cli"
	}

	providerRules := providerRule(provider)
	modeRules := modeRule(mode)

	return fmt.Sprintf(`你是 Gopi，一个运行在本地的 AI 编程助手。

当前工作目录: %s
操作系统: %s
LLM Provider: %s
运行模式: %s

可用工具:
- bash: 执行 shell 命令
- read_file / write_file / edit_file: 读写与精确编辑文件
- grep_search / find_files / list_dir: 搜索与文件遍历

行为规范:
1. 先理解任务再执行；信息不足时先读取相关文件，不凭空猜测。
2. 优先做最小可行改动，保持与现有代码风格一致，避免无关重构。
3. 涉及删除、覆盖、批量改动或可能破坏环境的操作，先明确风险并征求确认。
4. 输出简洁直接：先给结果，再给关键依据和下一步。
5. 默认中文回复（除非用户要求英文）。

工程流程:
1. 改代码后优先运行相关测试/构建验证，再给结论。
2. 若发现错误，先定位根因并修复；不要用表面规避方案。
3. 未经用户明确要求，不执行 git commit / push / 分支操作。
4. 当仓库根目录存在 AGENT.md 时，视为项目级最高优先级补充规则并严格遵循。

Provider 注意事项:
%s

输出模式注意事项:
%s`, cwd, osName, provider, mode, providerRules, modeRules)
}

func BuildWithTemplate(templateFile, basePrompt, agentText string) string {
	basePrompt = strings.TrimSpace(basePrompt)
	agentText = strings.TrimSpace(agentText)

	if strings.TrimSpace(templateFile) == "" {
		if agentText == "" {
			return basePrompt
		}
		return basePrompt + "\n\n项目代理配置文件(AGENT.md)：\n" + agentText
	}

	data, err := os.ReadFile(resolveTemplatePath(templateFile))
	if err != nil {
		if agentText == "" {
			return basePrompt
		}
		return basePrompt + "\n\n项目代理配置文件(AGENT.md)：\n" + agentText
	}

	tmpl := string(data)
	hasBase := strings.Contains(tmpl, "{{BASE_PROMPT}}")
	hasAgent := strings.Contains(tmpl, "{{AGENT_MD}}")

	result := strings.ReplaceAll(tmpl, "{{BASE_PROMPT}}", basePrompt)
	result = strings.ReplaceAll(result, "{{AGENT_MD}}", agentText)

	if !hasBase {
		result = strings.TrimSpace(result) + "\n\n" + basePrompt
	}
	if !hasAgent && agentText != "" {
		result = strings.TrimSpace(result) + "\n\n项目代理配置文件(AGENT.md)：\n" + agentText
	}
	return strings.TrimSpace(result)
}

func providerRule(provider string) string {
	switch provider {
	case "openai":
		return `- 使用 OpenAI 兼容后端；工具调用能力可能因网关实现而差异。
- 若模型未返回工具调用，先输出简短计划，再给出最小可执行下一步。`
	default:
		return `- 使用 Ollama 本地后端；优先走原生工具调用。
- 当工具调用不可用时，明确说明并给出可执行替代步骤。`
	}
}

func modeRule(mode string) string {
	switch mode {
	case "print":
		return "- 仅输出最终答案正文，不输出多余装饰和解释前缀。"
	case "tui":
		return "- 每次回复尽量短段落；优先可扫描结构，减少冗长。"
	default:
		return "- CLI 交互中先给结果，再给简短原因与建议。"
	}
}

func resolveTemplatePath(path string) string {
	p := strings.TrimSpace(path)
	if p == "" {
		return p
	}
	if strings.HasPrefix(p, "~") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
