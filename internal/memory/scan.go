package memory

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/paths"
)

// Origin is a directory sessions ran in (a session's cwd or repository), with its git remote when known.
type Origin struct{ Dir, Remote string }

// Orphan classes.
const (
	OrphanTemp    = "temp"    // its directory was temporary: to the trash
	OrphanMoved   = "moved"   // its repository has one checkout here: merge into Target's
	OrphanUnknown = "unknown" // nothing tells where it went
)

// Orphan is a memory directory none of whose origins is still there.
type Orphan struct {
	Dir    string `json:"dir"`
	From   string `json:"from,omitempty"` // the origin it was found for
	Class  string `json:"class"`
	Target string `json:"target,omitempty"` // moved: the checkout to merge into
	Items  int    `json:"items"`
}

// Report is this machine's Claude memory directories: Dirs with something in them, Empty ones, the orphans among
// them and the MEMORY.md files Claude does not load whole.
type Report struct {
	Dirs    int      `json:"dirs"`
	Empty   int      `json:"empty"`
	Orphans []Orphan `json:"orphans,omitempty"`
	Over    []string `json:"over,omitempty"`
}

var temporary = isTemporary

// isTemporary: an agent's scratch directory, or one in a temp directory when the Claude home is not in one itself.
func isTemporary(dir string) bool {
	if index.AgentScratch(dir) {
		return true
	}
	_, inTemp := paths.InTemp(dir)
	_, homeInTemp := paths.InTemp(capture.ClaudeHome())
	return inTemp && !homeInTemp
}

// Scan goes through every projects/*/memory/ directory; their names encode a path one way, so origins whose encoding
// matches are where they came from, and checkouts lists this machine's checkouts of a git remote.
func Scan(origins []Origin, checkouts func(remote string) []string) Report {
	var r Report
	byName := map[string][]Origin{}
	for _, o := range origins {
		if o.Dir != "" {
			n := index.ClaudeProjectName(o.Dir)
			byName[n] = append(byName[n], o)
		}
	}
	ents, _ := os.ReadDir(claudeProjects())
	for _, e := range ents {
		mem := filepath.Join(claudeProjects(), e.Name(), "memory")
		inner, err := os.ReadDir(mem)
		if !e.IsDir() || err != nil {
			continue
		}
		if len(inner) == 0 {
			r.Empty++
			continue
		}
		r.Dirs++
		s, _ := Load(KindClaude, mem)
		if s.Over {
			r.Over = append(r.Over, s.Index)
		}
		from := byName[e.Name()]
		if slices.ContainsFunc(from, func(o Origin) bool { return paths.IsDir(o.Dir) }) || stillThere(e.Name()) {
			continue
		}
		o := Orphan{Dir: mem, Class: OrphanUnknown, Items: len(s.Items)}
		if len(from) > 0 {
			o.From = from[0].Dir
		}
		switch {
		case slices.ContainsFunc(from, func(o Origin) bool { return temporary(o.Dir) }):
			o.Class = OrphanTemp
		default:
			if t := checkout(from, checkouts); t != "" {
				o.Class, o.Target = OrphanMoved, t
			}
		}
		r.Orphans = append(r.Orphans, o)
	}
	return r
}

// stillThere: some existing directory encodes to name, found by walking down from the root one matching name at a time.
func stillThere(name string) bool {
	root := string(filepath.Separator)
	if len(name) > 3 && name[1:3] == "--" {
		root = name[:1] + ":" + root
	}
	rest, ok := strings.CutPrefix(name, index.ClaudeProjectName(root))
	if !ok || !paths.IsDir(root) {
		return false
	}
	budget := 500
	var walk func(dir, rest string) bool
	walk = func(dir, rest string) bool {
		if budget--; budget < 0 {
			return false
		}
		ents, _ := os.ReadDir(dir)
		for _, e := range ents {
			if !e.IsDir() && (e.Type()&os.ModeSymlink == 0 || !paths.IsDir(filepath.Join(dir, e.Name()))) {
				continue
			}
			enc := index.ClaudeProjectName(e.Name())
			if rest == enc {
				return true
			}
			if after, ok := strings.CutPrefix(rest, enc+"-"); ok && walk(filepath.Join(dir, e.Name()), after) {
				return true
			}
		}
		return false
	}
	return rest == "" || walk(root, rest)
}

// checkout is the one existing checkout here of the orphan's remotes, "" when there are none or several.
func checkout(from []Origin, checkouts func(remote string) []string) string {
	if checkouts == nil {
		return ""
	}
	var hits, asked []string
	for _, f := range from {
		if f.Remote == "" || slices.Contains(asked, f.Remote) {
			continue
		}
		asked = append(asked, f.Remote)
		for _, d := range checkouts(f.Remote) {
			if paths.IsDir(d) && !slices.ContainsFunc(hits, func(h string) bool { return paths.Same(h, d) }) {
				hits = append(hits, d)
			}
		}
	}
	if len(hits) != 1 {
		return ""
	}
	return hits[0]
}
