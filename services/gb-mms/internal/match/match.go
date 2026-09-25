// Package match decides which maps are worth keeping and where they belong.
package match

import (
	"fmt"
	"path"
	"slices"
	"strings"
)

// Route sends maps whose name starts with Prefix to Dir on the FastDL host.
// Dir is relative to the uploader's base directory; empty puts them straight
// into it.
type Route struct {
	Prefix string
	Dir    string
}

type Matcher struct {
	routes []Route

	// fallback takes maps that matched no route. Having one is what turns the
	// route list from a filter into pure routing: without it an unmatched map
	// is dropped.
	fallback    string
	hasFallback bool

	// byCategory overrides the fallback for a single category. A broad listing
	// wants a strict filter; one that is already about a single kind of map
	// wants to keep everything.
	byCategory map[int]rule
}

// rule is a fallback and whether there is one, which "." cannot express on its
// own.
type rule struct {
	dir string
	on  bool
}

// New builds a matcher. Order matters: the first matching prefix wins, so a
// more specific one has to come first ("kz_bhop_" before "kz_").
//
// fallback is the directory for maps that matched nothing; empty drops them.
// Pass "." to keep them in the base directory itself. byCategory overrides it
// for the categories listed, by the same rules.
func New(routes []Route, fallback string, byCategory map[int]string) (*Matcher, error) {
	out := make([]Route, 0, len(routes))
	seen := make(map[string]struct{}, len(routes))

	for i, r := range routes {
		p := strings.ToLower(strings.TrimSpace(r.Prefix))
		if p == "" {
			return nil, fmt.Errorf("match: routes[%d].prefix is empty", i)
		}

		if _, dup := seen[p]; dup {
			return nil, fmt.Errorf("match: routes[%d].prefix %q is a duplicate and would never match", i, p)
		}

		seen[p] = struct{}{}
		out = append(out, Route{Prefix: p, Dir: cleanDir(r.Dir)})
	}

	m := &Matcher{
		routes:      out,
		fallback:    cleanDir(fallback),
		hasFallback: strings.TrimSpace(fallback) != "",
		byCategory:  make(map[int]rule, len(byCategory)),
	}

	for id, dir := range byCategory {
		m.byCategory[id] = rule{dir: cleanDir(dir), on: strings.TrimSpace(dir) != ""}
	}

	if len(m.routes) == 0 {
		if !m.hasFallback {
			return nil, fmt.Errorf("match: needs at least one route or a fallback_dir, or nothing would ever be kept")
		}

		for id, dir := range byCategory {
			if strings.TrimSpace(dir) == "" {
				return nil, fmt.Errorf("match: category %d drops everything: it clears the fallback and there are no routes", id)
			}
		}
	}

	return m, nil
}

func (m *Matcher) Routes() []Route {
	return m.routes
}

// Fallback reports the default directory for unmatched maps, and whether there
// is one.
func (m *Matcher) Fallback() (string, bool) {
	return m.fallback, m.hasFallback
}

// FallbackFor is Fallback after the category's own override is applied.
func (m *Matcher) FallbackFor(categoryID int) (string, bool) {
	if r, ok := m.byCategory[categoryID]; ok {
		return r.dir, r.on
	}

	return m.fallback, m.hasFallback
}

// CategoryIDs lists the categories with an override, for logging.
func (m *Matcher) CategoryIDs() []int {
	out := make([]int, 0, len(m.byCategory))
	for id := range m.byCategory {
		out = append(out, id)
	}

	slices.Sort(out)

	return out
}

func (m *Matcher) Prefixes() []string {
	out := make([]string, 0, len(m.routes))
	for _, r := range m.routes {
		out = append(out, r.Prefix)
	}

	return out
}

// Map reports where a map belongs and whether it is wanted at all.
func (m *Matcher) Map(categoryID int, name string) (string, bool) {
	name = strings.ToLower(name)

	for _, r := range m.routes {
		if strings.HasPrefix(name, r.Prefix) {
			return r.Dir, true
		}
	}

	return m.FallbackFor(categoryID)
}

// Mod is a pre-filter on the mod title, applied before downloading.
func (m *Matcher) Mod(categoryID int, title string) bool {
	if _, ok := m.FallbackFor(categoryID); ok {
		return true
	}

	title = strings.ToLower(title)
	for _, r := range m.routes {
		if strings.Contains(title, r.Prefix) {
			return true
		}
	}

	return false
}

func cleanDir(dir string) string {
	d := path.Clean("/" + strings.ReplaceAll(strings.TrimSpace(dir), "\\", "/"))

	return strings.TrimPrefix(d, "/")
}
