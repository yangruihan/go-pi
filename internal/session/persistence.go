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
	ParentID  string    `json:"parent_id,omitempty"`
	ParentEntryID string `json:"parent_entry_id,omitempty"`
	Timestamp string    `json:"timestamp"`
}

type messageEntry struct {
	Type       entryType       `json:"type"`
	ID         string          `json:"id,omitempty"`
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
	ParentID  string
	ParentEntryID string
	UpdatedAt time.Time
}

// SessionEntryMeta 会话消息条目元信息
type SessionEntryMeta struct {
	ID        string
	Role      string
	Preview   string
	Timestamp string
}

// LoadedSession 从持久化加载出的会话
type LoadedSession struct {
	ID       string
	FilePath string
	CWD      string
	ParentID string
	ParentEntryID string
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

func newEntryID() string {
	return "e-" + time.Now().UTC().Format("20060102T150405.000000000Z")
}

func (m *SessionManager) sessionDir(cwd string) string {
	return filepath.Join(m.rootDir, hashCWD(cwd))
}

func (m *SessionManager) Create(cwd, model string) (*LoadedSession, error) {
	return m.createWithParent(cwd, model, "", "")
}

func (m *SessionManager) createWithParent(cwd, model, parentID, parentEntryID string) (*LoadedSession, error) {
	if err := os.MkdirAll(m.sessionDir(cwd), 0o755); err != nil {
		return nil, err
	}
	id := newSessionID()
	filePath := filepath.Join(m.sessionDir(cwd), id+".jsonl")

	header := headerEntry{Type: entryHeader, ID: id, CWD: cwd, ParentID: parentID, ParentEntryID: parentEntryID, Timestamp: time.Now().UTC().Format(time.RFC3339)}
	if err := appendJSONL(filePath, header); err != nil {
		return nil, err
	}
	if model != "" {
		_ = appendJSONL(filePath, modelChangeEntry{Type: entryModelChange, Model: model, Timestamp: time.Now().UTC().Format(time.RFC3339)})
	}

	return &LoadedSession{ID: id, FilePath: filePath, CWD: cwd, ParentID: parentID, ParentEntryID: parentEntryID, Model: model}, nil
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
		h := readSessionHeader(filepath.Join(dir, e.Name()))
		metas = append(metas, SessionMeta{ID: id, FilePath: filepath.Join(dir, e.Name()), CWD: cwd, ParentID: h.ParentID, ParentEntryID: h.ParentEntryID, UpdatedAt: info.ModTime()})
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
				out.ParentID = v.ParentID
				out.ParentEntryID = v.ParentEntryID
			}
		case entryModelChange:
			var v modelChangeEntry
			if json.Unmarshal(line, &v) == nil {
				out.Model = v.Model
			}
		case entryMessage:
			var v messageEntry
			if json.Unmarshal(line, &v) == nil {
				out.Messages = append(out.Messages, llm.Message{EntryID: v.ID, Role: v.Role, Content: v.Content, Images: v.Images, ToolCalls: v.ToolCalls, ToolCallID: v.ToolCallID})
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

func (m *SessionManager) ListEntries(sessionFile string, limit int) ([]SessionEntryMeta, error) {
	f, err := os.Open(sessionFile)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make([]SessionEntryMeta, 0)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var env struct { Type entryType `json:"type"` }
		if json.Unmarshal(line, &env) != nil || env.Type != entryMessage {
			continue
		}
		var msg messageEntry
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		if strings.TrimSpace(msg.ID) == "" {
			continue
		}
		preview := strings.TrimSpace(msg.Content)
		if len([]rune(preview)) > 40 {
			r := []rune(preview)
			preview = string(r[:40]) + "..."
		}
		out = append(out, SessionEntryMeta{ID: msg.ID, Role: msg.Role, Preview: preview, Timestamp: msg.Timestamp})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (m *SessionManager) CheckoutFromEntry(cwd, currentSessionID, currentFile, entryID, model string) (*LoadedSession, error) {
	if strings.TrimSpace(entryID) == "" {
		return nil, fmt.Errorf("entry id cannot be empty")
	}
	messages, err := loadMessagesUntilEntry(currentFile, entryID)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("entry id %s not found", entryID)
	}
	created, err := m.createWithParent(cwd, model, currentSessionID, entryID)
	if err != nil {
		return nil, err
	}
	for _, msg := range messages {
		if err := appendJSONL(created.FilePath, msg); err != nil {
			return nil, err
		}
	}
	return m.Load(created.FilePath)
}

func loadMessagesUntilEntry(filePath, entryID string) ([]messageEntry, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	out := make([]messageEntry, 0)
	found := false
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var env struct { Type entryType `json:"type"` }
		if json.Unmarshal(line, &env) != nil || env.Type != entryMessage {
			continue
		}
		var msg messageEntry
		if json.Unmarshal(line, &msg) != nil {
			continue
		}
		out = append(out, msg)
		if msg.ID == entryID {
			found = true
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("entry id %s not found", entryID)
	}
	return out, nil
}

func readSessionHeader(filePath string) headerEntry {
	f, err := os.Open(filePath)
	if err != nil {
		return headerEntry{}
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Bytes()
		var h headerEntry
		if json.Unmarshal(line, &h) == nil && h.Type == entryHeader {
			return h
		}
	}
	return headerEntry{}
}

func appendJSONL(filePath string, v any) error {
	line, err := marshalJSONLLine(v)
	if err != nil {
		return err
	}
	return appendJSONLLine(filePath, line)
}

func marshalJSONLLine(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func appendJSONLLine(filePath string, line []byte) error {
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(filePath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(line); err != nil {
		return err
	}
	return nil
}
