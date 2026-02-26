# config 模板说明

本目录提供 gopi 的配置模板示例，用于快速初始化本地配置。

## 模板列表

- `config.yaml.example`：主配置（provider、上下文、TUI、扩展、提示词模板）
- `models.yaml.example`：模型别名配置，支持 `/model <alias>`
- `tools.yaml.example`：自定义工具定义（YAML shell 工具）
- `prompt.md.example`：系统提示词外置模板（支持占位符）
- `AGENT.md.example`：项目级代理规则示例

## 建议使用方式

### 一键初始化（Windows PowerShell）

```powershell
.\config\init.ps1
```

可选参数：

```powershell
# 覆盖已存在文件
.\config\init.ps1 -Force

# 指定目标目录
.\config\init.ps1 -TargetDir "D:\my-gopi-config"

# 同时在当前项目根目录生成 AGENT.md
.\config\init.ps1 -InitProjectAgent
```

---

1. 复制模板到用户目录：
   - `~/.gopi/config.yaml`
   - `~/.gopi/models.yaml`
   - `~/.gopi/tools.yaml`
   - `~/.gopi/prompt.md`
2. 可选：在项目根目录放置项目级配置（用于覆盖用户级）
   - `<project>/.gopi/config.yaml`、`<project>/.gopi/models.yaml`
   - `<project>/.claude/config.yaml`、`<project>/.claude/models.yaml`
   - `<project>/.pi/config.yaml`、`<project>/.pi/models.yaml`
3. 在项目根目录放置 `AGENT.md`（可从模板改写）
4. 启动 gopi 后验证：
   - `/help`
   - `/model <alias>`
   - `/skill:<name>`

## 配置加载优先级

- 主配置：`default` → `~/.gopi/config.yaml` → `<project>/.gopi/config.yaml` → `<project>/.claude/config.yaml` → `<project>/.pi/config.yaml`
- 模型别名：`~/.gopi/models.yaml` → `<project>/.gopi/models.yaml` → `<project>/.claude/models.yaml` → `<project>/.pi/models.yaml`（同名 alias 以后者覆盖前者）

启动时会显示“配置路径 / 模型配置路径”的最终生效文件路径。
