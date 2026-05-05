package memory

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

type IndexSnapshot struct {
	Entries []IndexEntry `json:"entries"`
}

func (s IndexSnapshot) String() string {
	if len(s.Entries) == 0 {
		return ""
	}
	root := indexTreeNode{
		children: make(map[string]*indexTreeNode),
	}
	for _, entry := range s.Entries {
		parts := strings.Split(entry.Path, "/")
		node := &root
		for _, part := range parts[:len(parts)-1] {
			child := node.children[part]
			if child == nil {
				child = &indexTreeNode{children: make(map[string]*indexTreeNode)}
				node.children[part] = child
			}
			node = child
		}
		node.children[parts[len(parts)-1]] = &indexTreeNode{
			summary: entry.Summary,
		}
	}
	var sb strings.Builder
	renderIndexTree(&sb, root.children, 0)
	return strings.TrimRight(sb.String(), "\n")
}

type indexTreeNode struct {
	summary  string
	children map[string]*indexTreeNode
}

func renderIndexTree(sb *strings.Builder, nodes map[string]*indexTreeNode, depth int) {
	names := slices.Collect(maps.Keys(nodes))
	slices.Sort(names)
	indent := strings.Repeat("  ", depth)
	for _, name := range names {
		node := nodes[name]
		if len(node.children) == 0 {
			fmt.Fprintf(sb, "%s%s", indent, name)
			if node.summary != "" {
				fmt.Fprintf(sb, " -- %s", node.summary)
			}
			sb.WriteString("\n")
			continue
		}
		fmt.Fprintf(sb, "%s%s/\n", indent, name)
		renderIndexTree(sb, node.children, depth+1)
	}
}
