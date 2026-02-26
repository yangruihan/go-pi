# config 模板说明

本目录提供 gopi 的配置模板示例，用于快速初始化本地配置。

## 模板列表

- `config.yaml.example`：主配置（provider、上下文、TUI、扩展、提示词模板）
- `models.yaml.example`：模型别名配置，支持 `/model <alias>`
- `tools.yaml.example`：自定义工具定义（YAML shell 工具）
- `prompt.md.example`：系统提示词外置模板（支持占位符）
- `AGENT.md.example`：项目级代理规则示例

## 建议使用方式

1. 复制模板到用户目录：
   - `~/.gopi/config.yaml`
   - `~/.gopi/models.yaml`
   - `~/.gopi/tools.yaml`
   - `~/.gopi/prompt.md`
2. 在项目根目录放置 `AGENT.md`（可从模板改写）
3. 启动 gopi 后验证：
   - `/help`
   - `/model <alias>`
   - `/skill:<name>`
