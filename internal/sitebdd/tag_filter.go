//go:build site_tests

package sitebdd

import (
	"fmt"
	"strings"
	"unicode"
)

func countScheduledScenarios(scenarios []ScenarioData, filter string) (int, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return len(scenarios), nil
	}

	expr, err := parseTagFilter(filter)
	if err != nil {
		return 0, err
	}

	count := 0
	for _, scenario := range scenarios {
		tagSet := make(map[string]struct{}, len(deriveTags(scenario)))
		for _, tag := range deriveTags(scenario) {
			tagSet[strings.TrimPrefix(tag, "@")] = struct{}{}
		}
		if expr.eval(tagSet) {
			count++
		}
	}
	return count, nil
}

type tagExpr interface {
	eval(tags map[string]struct{}) bool
}

type tagLiteral string

func (t tagLiteral) eval(tags map[string]struct{}) bool {
	_, ok := tags[string(t)]
	return ok
}

type tagNot struct{ expr tagExpr }

func (t tagNot) eval(tags map[string]struct{}) bool { return !t.expr.eval(tags) }

type tagAnd struct {
	left  tagExpr
	right tagExpr
}

func (t tagAnd) eval(tags map[string]struct{}) bool { return t.left.eval(tags) && t.right.eval(tags) }

type tagOr struct {
	left  tagExpr
	right tagExpr
}

func (t tagOr) eval(tags map[string]struct{}) bool { return t.left.eval(tags) || t.right.eval(tags) }

type tagParser struct {
	tokens []string
	pos    int
}

func parseTagFilter(filter string) (tagExpr, error) {
	p := &tagParser{tokens: tokenizeTagFilter(filter)}
	if len(p.tokens) == 0 {
		return tagLiteral(""), nil
	}
	expr, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if p.pos != len(p.tokens) {
		return nil, fmt.Errorf("unexpected token %q in GODOG_TAGS", p.tokens[p.pos])
	}
	return expr, nil
}

func (p *tagParser) parseOr() (tagExpr, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.match("or") || p.match(",") {
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = tagOr{left: left, right: right}
	}
	return left, nil
}

func (p *tagParser) parseAnd() (tagExpr, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.match("and") || p.match("&&") {
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = tagAnd{left: left, right: right}
	}
	return left, nil
}

func (p *tagParser) parseUnary() (tagExpr, error) {
	if p.match("not") || p.match("~") {
		expr, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return tagNot{expr: expr}, nil
	}
	return p.parsePrimary()
}

func (p *tagParser) parsePrimary() (tagExpr, error) {
	if p.match("(") {
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.match(")") {
			return nil, fmt.Errorf("missing closing ')' in GODOG_TAGS")
		}
		return expr, nil
	}
	if p.pos >= len(p.tokens) {
		return nil, fmt.Errorf("unexpected end of GODOG_TAGS")
	}
	token := p.tokens[p.pos]
	p.pos++
	return tagLiteral(strings.TrimPrefix(token, "@")), nil
}

func (p *tagParser) match(token string) bool {
	if p.pos >= len(p.tokens) {
		return false
	}
	if strings.EqualFold(p.tokens[p.pos], token) {
		p.pos++
		return true
	}
	return false
}

func tokenizeTagFilter(filter string) []string {
	var tokens []string
	var current strings.Builder

	flush := func() {
		if current.Len() == 0 {
			return
		}
		tokens = append(tokens, current.String())
		current.Reset()
	}

	for i := 0; i < len(filter); i++ {
		ch := rune(filter[i])
		switch {
		case unicode.IsSpace(ch):
			flush()
		case ch == '(' || ch == ')' || ch == ',':
			flush()
			tokens = append(tokens, string(ch))
		case ch == '~':
			flush()
			tokens = append(tokens, "~")
		case ch == '&' && i+1 < len(filter) && filter[i+1] == '&':
			flush()
			tokens = append(tokens, "&&")
			i++
		default:
			current.WriteRune(ch)
		}
	}
	flush()

	return tokens
}
