package mail

import (
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var skipElements = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Head: true, atom.Title: true,
	atom.Noscript: true, atom.Template: true, atom.Svg: true,
}

// blockElements maps elements to the number of newlines that separate them
// from surrounding text (2 = blank line).
var blockElements = map[atom.Atom]int{
	atom.P: 2, atom.H1: 2, atom.H2: 2, atom.H3: 2, atom.H4: 2, atom.H5: 2,
	atom.H6: 2, atom.Blockquote: 2, atom.Table: 2, atom.Pre: 2,
	atom.Div: 1, atom.Br: 1, atom.Li: 1, atom.Tr: 1, atom.Ul: 1, atom.Ol: 1,
	atom.Hr: 1, atom.Section: 1, atom.Article: 1, atom.Header: 1,
	atom.Footer: 1, atom.Dl: 1, atom.Dt: 1, atom.Dd: 1, atom.Center: 1,
}

const maxHTMLDepth = 512

// HTMLToText converts an HTML document or fragment to plain text suitable for
// full-text search and snippets.
func HTMLToText(src string) string {
	root, err := html.Parse(strings.NewReader(src))
	if err != nil || root == nil {
		return collapseText(src)
	}
	var b strings.Builder
	renderText(&b, root, 0, false)
	return collapseText(b.String())
}

// breakLines makes the output end with at least want newlines.
func breakLines(b *strings.Builder, want int) {
	cur := b.String()
	if cur == "" {
		return
	}
	have := 0
	for i := len(cur) - 1; i >= 0 && have < want && (cur[i] == '\n' || cur[i] == ' '); i-- {
		if cur[i] == '\n' {
			have++
		}
	}
	for ; have < want; have++ {
		b.WriteByte('\n')
	}
}

func renderText(b *strings.Builder, n *html.Node, depth int, pre bool) {
	if depth > maxHTMLDepth {
		return
	}
	switch n.Type {
	case html.TextNode:
		if pre {
			b.WriteString(n.Data)
		} else {
			b.WriteString(collapseSpaces(n.Data))
		}
		return
	case html.CommentNode, html.DoctypeNode:
		return
	case html.ElementNode:
		if skipElements[n.DataAtom] || isHidden(n) {
			return
		}
	}
	block := 0
	if n.Type == html.ElementNode {
		block = blockElements[n.DataAtom]
		pre = pre || n.DataAtom == atom.Pre
	}
	if block > 0 {
		breakLines(b, block)
	}
	if n.Type == html.ElementNode && n.DataAtom == atom.Li {
		b.WriteString("- ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		renderText(b, c, depth+1, pre)
	}
	if n.Type == html.ElementNode {
		switch {
		case n.DataAtom == atom.A:
			href := attr(n, "href")
			if href != "" && (strings.HasPrefix(href, "http://") || strings.HasPrefix(href, "https://")) &&
				strings.TrimSpace(nodeText(n)) != href {
				b.WriteString(" (" + href + ")")
			}
		case n.DataAtom == atom.Td || n.DataAtom == atom.Th:
			b.WriteByte(' ')
		}
	}
	if block > 0 {
		breakLines(b, block)
	}
}

// collapseSpaces turns whitespace runs into single spaces, keeping a leading
// or trailing space so adjacent inline text stays separated.
func collapseSpaces(s string) string {
	f := strings.Fields(strings.ReplaceAll(s, "\u00a0", " "))
	out := strings.Join(f, " ")
	if len(f) == 0 {
		if s != "" {
			return " "
		}
		return ""
	}
	if r := s[0]; r == ' ' || r == '\n' || r == '\t' || r == '\r' {
		out = " " + out
	}
	if r := s[len(s)-1]; r == ' ' || r == '\n' || r == '\t' || r == '\r' {
		out += " "
	}
	return out
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func isHidden(n *html.Node) bool {
	if attr(n, "hidden") != "" {
		return true
	}
	style := strings.ToLower(strings.ReplaceAll(attr(n, "style"), " ", ""))
	return strings.Contains(style, "display:none")
}

func nodeText(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node, int)
	walk = func(n *html.Node, d int) {
		if d > maxHTMLDepth {
			return
		}
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, d+1)
		}
	}
	walk(n, 0)
	return b.String()
}

// collapseText normalizes whitespace: NBSP and tabs become spaces, space runs
// collapse, lines are trimmed and runs of blank lines shrink to one.
func collapseText(s string) string {
	s = strings.NewReplacer(" ", " ", "​", "", "‌", "", "\r\n", "\n", "\r", "\n").Replace(s)
	var out []string
	blank := 0
	for _, line := range strings.Split(s, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" {
			blank++
			if blank > 1 || len(out) == 0 {
				continue
			}
		} else {
			blank = 0
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, "\n"))
}
