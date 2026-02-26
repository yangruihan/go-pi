package session

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/coderyrh/gopi/internal/llm"
)

type entryType string

const (
	entryHeader      entryType = "header"
	entryMessage     entryType = "message"
	entryModelChange entryType = "model_change"
	entryCompaction  entryType = "compaction"
)

type headerEntry struct {
	Type      entryType `json:"type"`
	ID        string    `json:"id"`
	CWD       string    `json:"cwd"`
	Timestamp string    `json:"timestamp"`
}

type messageEntry struct {
	Type       entryType       `json:"type"`
	Role       string          `json:"role"`
	Content    string          `json:"content,omitempty"`
	Images     []string        `json:"images,omitempty"`
	ToolCalls  []llm.ToolCall  `json:"tool_calls,omitempty"`
	ToolCallID string          `json:"tool_call_id,omitempty"`
	Timestamp  string          `json:"timestamp"`
}

type modelChangeEntry struct {
	Type      entryType `json:"type"`
	Model     string    `json:"model"`
	Timestamp string    `json:"timestamp"`
}

type compactionEntry struct {
	Type        entryType `json:"type"`
	Summary     string    `json:"summary"`
	TokenBefore int       `json:"token_before"`
	TokenAfter  int       `json:"token_after"`
	Timestamp   string    `json:"timestamp"`
}

// SessionMeta 会话列表元数据
type SessionMeta struct {
	ID        string
	FilePath  string
	CWD       string
	UpdatedAt time.Time
}

// LoadedSession 从持久化加载出的会话
type LoadedSession struct {
	ID       string
	FilePath string
	CWD      string
	Model    string
	Messages []llm.Message
}

// SessionManager 管理会话文件
type SessionManager struct {
	rootDir string
}

func NewSessionManager(rootDir string) *SessionManager {
	return &SessionManager{rootDir: rootDir}
}

func (m *SessionManager) RootDir() string { return m.rootDir }

func DefaultSessionsRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gopi", "sessions"), nil
}

func hashCWD(cwd string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(cwd))))
	return hex.EncodeToString(h[:])[:12]
}

func newSessionID() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z")
}

func (m *SessionManager) sessionDir(cwd string) string {
	return filepath.Join(m.rootDir, hashCWD(cwd))
}

func (m *SessionManager) Create(cwd, model string) (*LoadedSession, error) {
	if err := os.MkdirAll(m.sessionDir(cwd), 0o755); err != nil {
		return nil, err
	}
	id := newSessionID()
	filePath := filepath.Join(m.sessionDir(cwd), id+".jsonl")

	header := headerEntry{Type: entryHeader, ID: id, CWD: cwd, Timestamp: time.Now().UTC().Format(time.RFC3339)}
	if err := appendJSONL(filePath, header); err != nil {
		return nil, err
	}
	if model != "" {
		_ = appendJSONL(filePath, modelChangeEntry{Type: entryModelChange, Model: model, Timestamp: time.Now().UTC().Format(time.RFC3339)})
	}

	return &LoadedSession{ID: id, FilePath: filePath, CWD: cwd, Model: model}, nil
}

func (m *SessionManager) List(cwd string) ([]SessionMeta, error) {
	dir := m.sessionDir(cwd)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	metas := make([]SessionMeta, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		id := strings.TrimSuffix(e.Name(), ".jsonl")
		metas = append(metas, SessionMeta{ID: id, FilePath: filepath.Join(dir, e.Name()), CWD: cwd, UpdatedAt: info.ModTime()})
	}
	sort.Slice(metas, func(i, j int) bool { return metas[i].UpdatedAt.After(metas[j].UpdatedAt) })
	return metas, nil
}

func (m *SessionManager) Continue(cwd string) (*LoadedSession, error) {
	list, err := m.List(cwd)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, os.ErrNotExist
	}
	return m.Load(list[0].FilePath)
}

func (m *SessionManager) LoadByID(cwd, id string) (*LoadedSession, error) {
	if strings.TrimSpace(id) == "" {
		return nil, fmt.Errorf("session id cannot be empty")
	}
	return m.Load(filepath.Join(m.sessionDir(cwd), id+".jsonl"))
}

func (m *SessionManager) Load(filePath string) (*LoadedSession, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := &LoadedSession{FilePath: filePath}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var envelope struct {
			Type entryType `json:"type"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			continue
		}
		switch envelope.Type {
		case entryHeader:
			var v headerEntry
			if json.Unmarshal(line, &v) == nil {
				out.ID = v.ID
				out.CWD = v.CWD
			}
		case entryModelChange:
			var v modelChangeEntry
			if json.Unmarshal(line, &v) == nil {
				out.Model = v.Model
			}
		case entryMessage:
			var v messageEntry
			if json.Unmarshal(line, &v) == nil {
				out.Messages = append(out.Messages, llm.Message{Role: v.Role, Content: v.Content, Images: v.Images, ToolCalls: v.ToolCalls, ToolCallID: v.ToolCallID})
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if out.ID == "" {
		out.ID = strings.TrimSuffix(filepath.Base(filePath), ".jsonl")
	}
	return out, nil
}

func appendJSONL(filePath string, v any) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()

	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	return nil
}
