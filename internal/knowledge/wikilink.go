package knowledge

import (
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

var (
	wikiPattern  = regexp.MustCompile(`^\[\[([a-z0-9-]+)(?:\|[^\[\]\r\n]*)?\]\]`)
	wikiKind     = ast.NewNodeKind("Wikilink")
	wikiMarkdown = goldmark.New(goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithInlineParsers(util.Prioritized(wikiParser{}, 199))))
)

type wikiLink struct {
	ast.BaseInline
	target string
}

func (*wikiLink) Kind() ast.NodeKind { return wikiKind }

func (n *wikiLink) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, map[string]string{"target": n.target}, nil)
}

type wikiParser struct{}

func (wikiParser) Trigger() []byte { return []byte{'['} }

func (wikiParser) Parse(_ ast.Node, block text.Reader, _ parser.Context) ast.Node {
	line, _ := block.PeekLine()
	match := wikiPattern.FindSubmatch(line)
	if match == nil || !ValidSlug(string(match[1])) {
		return nil
	}
	block.Advance(len(match[0]))
	return &wikiLink{target: string(match[1])}
}

func wikiTargets(body string) []string {
	if !strings.Contains(body, "[[") {
		return nil
	}
	doc := wikiMarkdown.Parser().Parse(text.NewReader([]byte(body)))
	var targets []string
	seen := map[string]bool{}
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			switch n := node.(type) {
			case *ast.Link, *ast.Image:
				return ast.WalkSkipChildren, nil
			case *wikiLink:
				if !seen[n.target] {
					seen[n.target] = true
					targets = append(targets, n.target)
				}
			}
		}
		return ast.WalkContinue, nil
	})
	return targets
}
