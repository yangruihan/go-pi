package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ModelProfile 模型别名配置
// 示例：
// models:
//   - name: qwen3
//     provider: ollama
//     model: qwen3:8b
//     base_url: http://localhost:11434
//   - name: deepseek
//     provider: openai
//     model: deepseek-chat
//     base_url: https://api.deepseek.com
//     api_key_env: DEEPSEEK_API_KEY
type ModelProfile struct {
	Name      string `yaml:"name"`
	Provider  string `yaml:"provider"`
	Model     string `yaml:"model"`
	BaseURL   string `yaml:"base_url"`
	APIKeyEnv string `yaml:"api_key_env"`
}

type modelsFile struct {
	Models []ModelProfile `yaml:"models"`
}

func DefaultModelsFile() (string, error) {
	dir, err := ConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "models.yaml"), nil
}

// ProjectModelsFile 返回项目级模型别名路径：<cwd>/.gopi/models.yaml
func ProjectModelsFile(cwd string) string {
	if strings.TrimSpace(cwd) == "" {
		return ""
	}
	return filepath.Join(cwd, ".gopi", "models.yaml")
}

// ProjectModelsFiles 返回项目级模型配置候选路径（按优先顺序）
func ProjectModelsFiles(cwd string) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	return []string{
		filepath.Join(cwd, ".gopi", "models.yaml"),
		filepath.Join(cwd, "models.yaml"),
	}
}

func LoadModelProfiles(path string) ([]ModelProfile, error) {
	cwd, _ := os.Getwd()
	profiles, _, err := LoadModelProfilesWithSources(path, cwd)
	return profiles, err
}

// LoadModelProfilesWithSources 加载模型别名并返回来源。
// path 不为空时仅加载该路径；为空时按 home -> project 顺序加载并合并（项目同名覆盖 home）。
func LoadModelProfilesWithSources(path, cwd string) ([]ModelProfile, []string, error) {
	if strings.TrimSpace(path) != "" {
		profiles, loaded, err := loadProfilesFile(path)
		if err != nil {
			return nil, nil, err
		}
		if loaded {
			return profiles, []string{path}, nil
		}
		return nil, nil, nil
	}

	var sources []string
	var merged []ModelProfile

	if homePath, err := DefaultModelsFile(); err == nil {
		if p, loaded, err := loadProfilesFile(homePath); err != nil {
			return nil, nil, err
		} else if loaded {
			merged = append(merged, p...)
			sources = append(sources, homePath)
		}
	}

	for _, projectPath := range ProjectModelsFiles(cwd) {
		if p, loaded, err := loadProfilesFile(projectPath); err != nil {
			return nil, nil, err
		} else if loaded {
			merged = mergeProfilesByName(merged, p)
			sources = append(sources, projectPath)
		}
	}

	return merged, sources, nil
}

func loadProfilesFile(path string) ([]ModelProfile, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("read models file %s: %w", path, err)
	}
	data, err = normalizeYAMLBytes(data)
	if err != nil {
		return nil, false, fmt.Errorf("decode models file %s: %w", path, err)
	}

	var mf modelsFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return nil, false, fmt.Errorf("parse models file %s: %w", path, err)
	}

	out := make([]ModelProfile, 0, len(mf.Models))
	for _, m := range mf.Models {
		m.Name = strings.TrimSpace(m.Name)
		m.Provider = strings.ToLower(strings.TrimSpace(m.Provider))
		m.Model = strings.TrimSpace(m.Model)
		m.BaseURL = strings.TrimSpace(m.BaseURL)
		m.APIKeyEnv = strings.TrimSpace(m.APIKeyEnv)
		if m.Name == "" || m.Model == "" {
			continue
		}
		if m.Provider == "" {
			m.Provider = "ollama"
		}
		out = append(out, m)
	}
	return out, true, nil
}

func mergeProfilesByName(base []ModelProfile, overlay []ModelProfile) []ModelProfile {
	if len(overlay) == 0 {
		return base
	}

	idxByName := make(map[string]int, len(base))
	out := make([]ModelProfile, len(base))
	copy(out, base)
	for i, p := range out {
		idxByName[p.Name] = i
	}

	for _, p := range overlay {
		if idx, ok := idxByName[p.Name]; ok {
			out[idx] = p
			continue
		}
		idxByName[p.Name] = len(out)
		out = append(out, p)
	}

	return out
}

func ResolveModelProfile(input string, profiles []ModelProfile) (ModelProfile, bool) {
	needle := strings.TrimSpace(input)
	if needle == "" {
		return ModelProfile{}, false
	}
	for _, p := range profiles {
		if p.Name == needle {
			return p, true
		}
	}
	return ModelProfile{}, false
}

func (m ModelProfile) ResolveAPIKey() (string, error) {
	if m.APIKeyEnv == "" {
		return "", nil
	}
	v := strings.TrimSpace(os.Getenv(m.APIKeyEnv))
	if v == "" {
		return "", fmt.Errorf("environment variable %s is empty", m.APIKeyEnv)
	}
	return v, nil
}
