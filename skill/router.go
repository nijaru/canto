package skill

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	agentskills "github.com/nijaru/agentskills"
	ccontext "github.com/nijaru/canto/context"
	"github.com/nijaru/canto/llm"
	"github.com/nijaru/canto/session"
)

type SkillMetadata struct {
	Name         string
	Description  string
	AllowedTools []string
}

type Router interface {
	SelectRelevant(
		ctx context.Context,
		taskDescription string,
		limit int,
	) ([]SkillMetadata, error)
}

type LexicalRouter struct {
	Registry          *agentskills.Registry
	NameWeight        float64
	DescriptionWeight float64
	BodyWeight        float64
	ToolWeight        float64
}

func (r *LexicalRouter) SelectRelevant(
	_ context.Context,
	taskDescription string,
	limit int,
) ([]SkillMetadata, error) {
	if r == nil || r.Registry == nil {
		return nil, nil
	}
	if limit <= 0 {
		limit = 8
	}
	skills := r.Registry.List()
	if len(skills) == 0 {
		return nil, nil
	}
	queryTokens := tokenize(taskDescription)
	if len(queryTokens) == 0 {
		return listMetadata(skills, limit), nil
	}
	nameWeight := r.NameWeight
	if nameWeight == 0 {
		nameWeight = 2.0
	}
	descriptionWeight := r.DescriptionWeight
	if descriptionWeight == 0 {
		descriptionWeight = 1.5
	}
	bodyWeight := r.BodyWeight
	if bodyWeight == 0 {
		bodyWeight = 1.0
	}
	toolWeight := r.ToolWeight
	if toolWeight == 0 {
		toolWeight = 1.25
	}

	type scored struct {
		meta  SkillMetadata
		score float64
	}
	results := make([]scored, 0, len(skills))
	for _, skill := range skills {
		score := overlapScore(queryTokens, tokenize(skill.Name))*nameWeight +
			overlapScore(queryTokens, tokenize(skill.Description))*descriptionWeight +
			overlapScore(queryTokens, tokenize(skill.Instructions))*bodyWeight +
			overlapScore(
				queryTokens,
				tokenize(strings.Join(toolNames(skill.AllowedTools), " ")),
			)*toolWeight
		if score == 0 {
			continue
		}
		results = append(results, scored{
			meta:  skillMetadata(skill),
			score: score,
		})
	}
	if len(results) == 0 {
		return listMetadata(skills, limit), nil
	}
	slices.SortFunc(results, func(a, b scored) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return strings.Compare(a.meta.Name, b.meta.Name)
	})
	if len(results) > limit {
		results = results[:limit]
	}
	selected := make([]SkillMetadata, 0, len(results))
	for _, result := range results {
		selected = append(selected, result.meta)
	}
	return selected, nil
}

func listMetadata(skills []*agentskills.Skill, limit int) []SkillMetadata {
	if len(skills) == 0 {
		return nil
	}
	if limit <= 0 || limit > len(skills) {
		limit = len(skills)
	}
	out := make([]SkillMetadata, 0, limit)
	for _, skill := range skills[:limit] {
		out = append(out, skillMetadata(skill))
	}
	return out
}

func skillMetadata(skill *agentskills.Skill) SkillMetadata {
	if skill == nil {
		return SkillMetadata{}
	}
	return SkillMetadata{
		Name:         skill.Name,
		Description:  skill.Description,
		AllowedTools: toolNames(skill.AllowedTools),
	}
}

func toolNames(tools agentskills.ToolList) []string {
	if len(tools) == 0 {
		return nil
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool)
	}
	return names
}

var tokenPattern = regexp.MustCompile(`[a-z0-9][a-z0-9_-]*`)

func tokenize(text string) map[string]float64 {
	text = strings.ToLower(text)
	matches := tokenPattern.FindAllString(text, -1)
	if len(matches) == 0 {
		return nil
	}
	tokens := make(map[string]float64, len(matches))
	for _, token := range matches {
		tokens[token]++
	}
	return tokens
}

func overlapScore(query, candidate map[string]float64) float64 {
	if len(query) == 0 || len(candidate) == 0 {
		return 0
	}
	score := 0.0
	for token := range maps.Keys(query) {
		if weight, ok := candidate[token]; ok {
			score += 1.0
			if weight > 1 {
				score += 0.1 * (weight - 1)
			}
		}
	}
	return score
}

type ListPromptOptions struct {
	Router Router
	Query  string
	Limit  int
}

func ListPromptWithOptions(
	reg *agentskills.Registry,
	opts ListPromptOptions,
) ccontext.RequestProcessor {
	if reg == nil {
		return ccontext.RequestProcessorFunc(
			func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
				return nil
			},
		)
	}
	return ccontext.RequestProcessorFunc(
		func(ctx context.Context, p llm.Provider, model string, sess *session.Session, req *llm.Request) error {
			metas, err := selectSkillMetadata(ctx, reg, sess, opts)
			if err != nil {
				return err
			}
			if len(metas) == 0 {
				return nil
			}
			var sb strings.Builder
			sb.WriteString("Available Skills (use read_skill for full details):\n")
			for _, skill := range metas {
				sb.WriteString(fmt.Sprintf("- %s: %s\n", skill.Name, skill.Description))
			}
			return ccontext.Instructions(sb.String()).ApplyRequest(ctx, p, model, sess, req)
		},
	)
}

func selectSkillMetadata(
	ctx context.Context,
	reg *agentskills.Registry,
	sess *session.Session,
	opts ListPromptOptions,
) ([]SkillMetadata, error) {
	query := strings.TrimSpace(opts.Query)
	if query == "" && sess != nil {
		messages, err := sess.EffectiveMessages()
		if err != nil {
			return nil, err
		}
		for i := len(messages) - 1; i >= 0; i-- {
			if messages[i].Content != "" {
				query = messages[i].Content
				break
			}
		}
	}
	if opts.Router != nil {
		return opts.Router.SelectRelevant(ctx, query, opts.Limit)
	}
	return listMetadata(reg.List(), opts.Limit), nil
}
