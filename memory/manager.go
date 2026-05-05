package memory

import (
	"context"
	"sync"
	"time"

	"github.com/nijaru/canto/llm"
)

type Scope string

const (
	ScopeThread    Scope = "thread"
	ScopeUser      Scope = "user"
	ScopeAgent     Scope = "agent"
	ScopeWorkspace Scope = "workspace"
	ScopeApp       Scope = "app"
)

type Role string

const (
	RoleCore       Role = "core"
	RoleEpisodic   Role = "episodic"
	RoleSemantic   Role = "semantic"
	RoleProcedural Role = "procedural"
)

type Namespace struct {
	Scope Scope  `json:"scope"`
	ID    string `json:"id"`
}

type Block struct {
	Namespace Namespace      `json:"namespace"`
	Name      string         `json:"name"`
	Content   string         `json:"content"`
	Metadata  map[string]any `json:"metadata,omitzero"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type Memory struct {
	ID           string         `json:"id"`
	Namespace    Namespace      `json:"namespace"`
	Role         Role           `json:"role"`
	Key          string         `json:"key,omitzero"`
	Content      string         `json:"content"`
	Metadata     map[string]any `json:"metadata,omitzero"`
	ObservedAt   *time.Time     `json:"observed_at,omitzero"`
	ValidFrom    *time.Time     `json:"valid_from,omitzero"`
	ValidTo      *time.Time     `json:"valid_to,omitzero"`
	Supersedes   string         `json:"supersedes,omitzero"`
	SupersededBy string         `json:"superseded_by,omitzero"`
	ForgottenAt  *time.Time     `json:"forgotten_at,omitzero"`
	UpdatedAt    time.Time      `json:"updated_at"`
	Score        float32        `json:"score,omitzero"`
}

type ConflictMode string

const (
	ConflictReplace ConflictMode = "replace"
	ConflictIgnore  ConflictMode = "ignore"
	ConflictMerge   ConflictMode = "merge"
)

type WriteMode string

const (
	WriteSync  WriteMode = "sync"
	WriteAsync WriteMode = "async"
)

type Candidate struct {
	Namespace  Namespace      `json:"namespace"`
	Role       Role           `json:"role"`
	Key        string         `json:"key,omitzero"`
	Content    string         `json:"content"`
	Metadata   map[string]any `json:"metadata,omitzero"`
	ObservedAt *time.Time     `json:"observed_at,omitzero"`
	ValidFrom  *time.Time     `json:"valid_from,omitzero"`
	ValidTo    *time.Time     `json:"valid_to,omitzero"`
	Supersedes string         `json:"supersedes,omitzero"`
	Importance float64        `json:"importance,omitzero"`
}

type (
	CandidateExtractor func(ctx context.Context, candidate Candidate) ([]Candidate, error)
	CandidateDeduper   func(ctx context.Context, candidates []Candidate) ([]Candidate, error)
)

type WritePolicy struct {
	Extractor           CandidateExtractor
	Deduper             CandidateDeduper
	ConflictMode        ConflictMode
	ImportanceThreshold float64
	DefaultMode         WriteMode
}

type WriteInput struct {
	Namespace  Namespace
	Role       Role
	Key        string
	Content    string
	Metadata   map[string]any
	ObservedAt *time.Time
	ValidFrom  *time.Time
	ValidTo    *time.Time
	Supersedes string
	Importance float64
	Mode       WriteMode
}

type WriteResult struct {
	Stored  int
	Pending int
	IDs     []string
}

type ForgetInput struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitzero"`
}

type ConsolidationInput struct {
	Namespaces        []Namespace
	Roles             []Role
	Limit             int
	IncludeForgotten  bool
	IncludeSuperseded bool
}

type ConsolidationPlan struct {
	Upserts []WriteInput  `json:"upserts,omitzero"`
	Forgets []ForgetInput `json:"forgets,omitzero"`
}

type ConsolidationResult struct {
	Examined  int         `json:"examined"`
	Written   WriteResult `json:"written"`
	Forgotten int         `json:"forgotten"`
}

type Consolidator interface {
	Consolidate(ctx context.Context, memories []Memory) (ConsolidationPlan, error)
}

type ConsolidatorFunc func(
	ctx context.Context,
	memories []Memory,
) (ConsolidationPlan, error)

func (f ConsolidatorFunc) Consolidate(
	ctx context.Context,
	memories []Memory,
) (ConsolidationPlan, error) {
	return f(ctx, memories)
}

type Query struct {
	Namespaces        []Namespace
	Roles             []Role
	Text              string
	Limit             int
	Filters           map[string]any
	UseSemantic       bool
	IncludeCore       bool
	IncludeRecent     bool
	ValidAt           *time.Time
	ObservedAfter     *time.Time
	ObservedBefore    *time.Time
	IncludeForgotten  bool
	IncludeSuperseded bool
}

type Manager struct {
	store          Store
	vector         VectorStore
	embedder       llm.Embedder
	policy         WritePolicy
	retrievePolicy RetrievePolicy

	asyncWG  sync.WaitGroup
	asyncMu  sync.Mutex
	asyncErr error
}

// NewManager builds a memory manager around the provided durable store.
func NewManager(store Store, opts ...ManagerOption) *Manager {
	manager := &Manager{
		store: store,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	if manager.policy.ConflictMode == "" {
		manager.policy.ConflictMode = ConflictReplace
	}
	if manager.policy.DefaultMode == "" {
		manager.policy.DefaultMode = WriteSync
	}
	return manager
}

func (m *Manager) Close() error {
	m.asyncWG.Wait()
	m.asyncMu.Lock()
	defer m.asyncMu.Unlock()
	return m.asyncErr
}

func (m *Manager) recordAsyncError(err error) {
	if err == nil {
		return
	}
	m.asyncMu.Lock()
	defer m.asyncMu.Unlock()
	if m.asyncErr == nil {
		m.asyncErr = err
	}
}
