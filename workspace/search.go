package workspace

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
)

// SearchHit is a ranked match returned from a workspace search index.
type SearchHit struct {
	Ref   ContentRef
	Score int
}

// SearchIndex stores workspace documents in a searchable form.
//
// The current concrete implementation is a trigram index over rooted workspace
// paths plus content. The interface stays small so other index algorithms can
// replace it later without changing callers that only need upsert/search.
type SearchIndex interface {
	Upsert(ctx context.Context, ref ContentRef, data []byte) error
	Delete(ctx context.Context, path string) error
	Search(ctx context.Context, query string, limit int) ([]SearchHit, error)
}

// TrigramIndex is an in-memory trigram search substrate over workspace files.
//
// It keeps doc IDs stable per workspace path, stores only unique normalized
// trigrams per document, and uses sorted merge intersection at query time.
type TrigramIndex struct {
	mu       sync.RWMutex
	nextID   uint32
	docs     map[string]*trigramDoc
	byID     map[uint32]*trigramDoc
	postings map[string][]uint32
}

type trigramDoc struct {
	id    uint32
	ref   ContentRef
	terms []string
}

// NewSearchIndex returns the default workspace search substrate.
func NewSearchIndex() *TrigramIndex {
	return &TrigramIndex{
		docs:     make(map[string]*trigramDoc),
		byID:     make(map[uint32]*trigramDoc),
		postings: make(map[string][]uint32),
	}
}

var _ SearchIndex = (*TrigramIndex)(nil)

// Upsert materializes one workspace file into the trigram index.
func (i *TrigramIndex) Upsert(ctx context.Context, ref ContentRef, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if i == nil {
		return fmt.Errorf("workspace search index: nil index")
	}
	if ref.Path == "" {
		return fmt.Errorf("workspace search index: empty path")
	}

	terms := corpusTerms(ref.Path, data)

	i.mu.Lock()
	defer i.mu.Unlock()

	doc, ok := i.docs[ref.Path]
	if !ok {
		i.nextID++
		doc = &trigramDoc{id: i.nextID}
		i.docs[ref.Path] = doc
		i.byID[doc.id] = doc
	}
	if doc.ref.Digest == ref.Digest && doc.ref.Size == ref.Size && doc.ref.Path == ref.Path {
		return nil
	}

	oldTerms := doc.terms
	oldSet := make(map[string]struct{}, len(oldTerms))
	for _, term := range oldTerms {
		oldSet[term] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(terms))
	for _, term := range terms {
		newSet[term] = struct{}{}
	}

	for term := range oldSet {
		if _, ok := newSet[term]; ok {
			continue
		}
		i.postings[term] = removeDocID(i.postings[term], doc.id)
		if len(i.postings[term]) == 0 {
			delete(i.postings, term)
		}
	}
	for term := range newSet {
		if _, ok := oldSet[term]; ok {
			continue
		}
		i.postings[term] = insertDocID(i.postings[term], doc.id)
	}

	doc.ref = ref
	doc.terms = terms
	return nil
}

// Delete removes a workspace path from the search index.
func (i *TrigramIndex) Delete(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if i == nil {
		return fmt.Errorf("workspace search index: nil index")
	}
	if path == "" {
		return nil
	}

	i.mu.Lock()
	defer i.mu.Unlock()

	doc, ok := i.docs[path]
	if !ok {
		return nil
	}
	for _, term := range doc.terms {
		i.postings[term] = removeDocID(i.postings[term], doc.id)
		if len(i.postings[term]) == 0 {
			delete(i.postings, term)
		}
	}
	delete(i.docs, path)
	delete(i.byID, doc.id)
	return nil
}

// Search returns ranked hits for the query across indexed workspace files.
func (i *TrigramIndex) Search(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if i == nil {
		return nil, fmt.Errorf("workspace search index: nil index")
	}
	if limit < 0 {
		limit = 0
	}

	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return nil, nil
	}

	i.mu.RLock()
	defer i.mu.RUnlock()

	terms := queryTerms(query)
	if len(terms) == 0 {
		return i.searchShortLocked(query, limit)
	}

	type termPosting struct {
		term    string
		posting []uint32
	}
	postings := make([]termPosting, 0, len(terms))
	for _, term := range terms {
		list := i.postings[term]
		if len(list) == 0 {
			return nil, nil
		}
		postings = append(postings, termPosting{term: term, posting: list})
	}
	slices.SortFunc(postings, func(a, b termPosting) int {
		return cmp.Compare(len(a.posting), len(b.posting))
	})

	candidates := slices.Clone(postings[0].posting)
	for _, posting := range postings[1:] {
		candidates = intersectSorted(candidates, posting.posting)
		if len(candidates) == 0 {
			return nil, nil
		}
	}

	hits := make([]SearchHit, 0, len(candidates))
	for _, id := range candidates {
		doc := i.byID[id]
		if doc == nil {
			continue
		}
		hits = append(hits, SearchHit{
			Ref:   doc.ref,
			Score: scoreHit(query, doc.ref, len(terms)),
		})
	}

	slices.SortFunc(hits, func(a, b SearchHit) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score)
		}
		return strings.Compare(a.Ref.Path, b.Ref.Path)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (i *TrigramIndex) searchShortLocked(query string, limit int) ([]SearchHit, error) {
	hits := make([]SearchHit, 0, len(i.docs))
	for _, doc := range i.docs {
		score := 0
		if strings.Contains(strings.ToLower(doc.ref.Path), query) {
			score = 1 + len(query)
		}
		if score == 0 {
			continue
		}
		hits = append(hits, SearchHit{Ref: doc.ref, Score: score})
	}
	slices.SortFunc(hits, func(a, b SearchHit) int {
		if a.Score != b.Score {
			return cmp.Compare(b.Score, a.Score)
		}
		return strings.Compare(a.Ref.Path, b.Ref.Path)
	})
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// IndexFile reads one workspace file, computes its content identity, and
// upserts it into the provided search index.
func IndexFile(
	ctx context.Context,
	fs WorkspaceFS,
	index SearchIndex,
	path string,
) (ContentRef, error) {
	if err := ctx.Err(); err != nil {
		return ContentRef{}, err
	}
	if fs == nil {
		return ContentRef{}, fmt.Errorf("workspace index: nil workspace fs")
	}
	if index == nil {
		return ContentRef{}, fmt.Errorf("workspace index: nil search index")
	}

	ref, data, err := RefFile(ctx, fs, path)
	if err != nil {
		return ContentRef{}, err
	}
	if err := index.Upsert(ctx, ref, data); err != nil {
		return ContentRef{}, err
	}
	return ref, nil
}

// IndexWorkspace walks the rooted workspace and indexes every regular file.
func IndexWorkspace(ctx context.Context, ws WorkspaceFS, index SearchIndex) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if ws == nil {
		return 0, fmt.Errorf("workspace index: nil workspace fs")
	}
	if index == nil {
		return 0, fmt.Errorf("workspace index: nil search index")
	}

	var count int
	err := walkWorkspaceFiles(ctx, ws, ".", func(filePath string) error {
		if _, err := IndexFile(ctx, ws, index, filePath); err != nil {
			return err
		}
		count++
		return nil
	})
	if err != nil {
		return count, err
	}
	return count, nil
}

func walkWorkspaceFiles(
	ctx context.Context,
	ws WorkspaceFS,
	dir string,
	visit func(path string) error,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := ws.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		child := entry.Name()
		if dir != "." && dir != "" {
			child = path.Join(dir, child)
		}
		if entry.IsDir() {
			if err := walkWorkspaceFiles(ctx, ws, child, visit); err != nil {
				return err
			}
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := visit(child); err != nil {
			return err
		}
	}
	return nil
}

func corpusTerms(path string, data []byte) []string {
	return trigrams(strings.ToLower(path + "\n" + string(data)))
}

func queryTerms(query string) []string {
	return trigrams(strings.ToLower(query))
}

func trigrams(text string) []string {
	runes := []rune(text)
	if len(runes) < 3 {
		return nil
	}

	terms := make([]string, 0, len(runes)-2)
	seen := make(map[string]struct{}, len(runes)-2)
	for i := 0; i+3 <= len(runes); i++ {
		term := string(runes[i : i+3])
		if strings.TrimSpace(term) == "" {
			continue
		}
		if _, ok := seen[term]; ok {
			continue
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	slices.Sort(terms)
	return terms
}

func scoreHit(query string, ref ContentRef, matchedTerms int) int {
	score := matchedTerms * 100
	if strings.Contains(strings.ToLower(ref.Path), query) {
		score += 25
	}
	score -= len(ref.Path)
	return score
}

func insertDocID(list []uint32, id uint32) []uint32 {
	idx, found := slices.BinarySearch(list, id)
	if found {
		return list
	}
	list = append(list, 0)
	copy(list[idx+1:], list[idx:])
	list[idx] = id
	return list
}

func removeDocID(list []uint32, id uint32) []uint32 {
	idx, found := slices.BinarySearch(list, id)
	if !found {
		return list
	}
	return slices.Delete(list, idx, idx+1)
}

func intersectSorted(a, b []uint32) []uint32 {
	if len(a) == 0 || len(b) == 0 {
		return nil
	}
	out := make([]uint32, 0, min(len(a), len(b)))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			i++
		default:
			j++
		}
	}
	return out
}
