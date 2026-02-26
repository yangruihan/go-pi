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

func LoadModelProfiles(path string) ([]ModelProfile, error) {
	if strings.TrimSpace(path) == "" {
		var err error
		path, err = DefaultModelsFile()
		if err != nil {
			return nil, err
		}
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var mf modelsFile
	if err := yaml.Unmarshal(data, &mf); err != nil {
		return nil, err
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
	return out, nil
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
