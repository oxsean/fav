package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/oxsean/fav/internal/capture"
	"github.com/oxsean/fav/internal/index"
	"github.com/oxsean/fav/internal/projects"
	"github.com/oxsean/fav/internal/tend"
)

// projectDial bounds the one dial a command makes for mode 2's projects.
const projectDial = time.Second

// sessionProjects are the projects this command places sessions in, read once per command (run sets it).
var sessionProjects = sync.OnceValue(loadProjects)

// loadProjects: mode 1 this machine's journal; mode 2 the project table while it is fresh, else what one short dial
// fetches (kept as the table), else none.
func loadProjects() *projects.Snapshot {
	s := projectsAt(time.Now())
	return &s
}

func projectsAt(now time.Time) projects.Snapshot {
	c := loadConfig().Coordinator
	if c == nil || c.URL == "" {
		j := projects.NewJournal(journalPath())
		if _, err := j.Read(); err != nil {
			fmt.Fprintln(os.Stderr, err)
		}
		return j.Snapshot()
	}
	path := projects.TablePath(tend.Home())
	switch s, fresh, failed := projects.LoadTable(path, now); {
	case fresh:
		return s
	case failed:
		return projects.Down(projects.Offline)
	}
	ctx, cancel := context.WithTimeout(context.Background(), projectDial)
	defer cancel()
	s, err := fetchProjects(ctx)
	if err != nil {
		projects.SaveTable(path, projects.Snapshot{}, now)
		return projects.Down(projects.Offline)
	}
	projects.SaveTable(path, s, time.Time{})
	return s
}

func fetchProjects(ctx context.Context) (projects.Snapshot, error) {
	cl, err := dialServer(ctx)
	if err != nil {
		return projects.Snapshot{}, err
	}
	defer cl.Close()
	return projects.Fetch(ctx, cl, projects.NodeID(tend.Home()))
}

// belongRows lists with sessions placed in their projects.
func belongRows() index.Rows { return index.Rows{Belong: sessionProjects().Belong} }

// projectFor is the name of the project dir on this machine belongs to, "" for none.
func projectFor(dir string) string {
	s := sessionProjects()
	if p := s.Holding(s.Here, dir); p != nil {
		return p.Name
	}
	return ""
}

// sessionDir is the directory that decides the session's project: its main checkout as the index knows it, else
// where it runs.
func sessionDir(ctx *capture.Context) string {
	if idx, err := index.Open(); err == nil {
		for _, ss := range idx.Sessions() {
			if ss.Provider == ctx.Provider && ss.SessionID == ctx.SessionID && ss.Repo != "" {
				return ss.Repo
			}
		}
	}
	return ctx.Cwd
}
