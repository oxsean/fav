package tracker_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/tracker"
	"github.com/oxsean/fav/internal/tracker/trackertest"
)

func TestEachTrackerReadsAndWritesWhatTheSyncNeeds(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		t.Run(kind, func(t *testing.T) {
			g := trackertest.New(kind, "acme/app", "tend-bot", "tok")
			defer g.Close()
			tr, err := tracker.New(tracker.Config{Kind: kind, Base: g.URL + "/", Repo: "acme/app", Token: "tok"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if me, err := tr.Me(ctx); err != nil || me != "tend-bot" {
				t.Fatalf("%q %v", me, err)
			}
			if r, err := tr.Repo(ctx); err != nil || r.ID != 42 || r.FullName != "acme/app" {
				t.Fatalf("%+v %v", r, err)
			}
			for n := int64(1); n <= 260; n++ {
				g.Open(n, fmt.Sprintf("issue %d", n), "body", map[bool]string{true: "tend", false: "other"}[n%2 == 0])
			}
			want := 130
			if kind != tracker.KindGitLab { // GitLab keeps merge requests apart
				g.Change(6, func(i *trackertest.Issue) { i.PR = true })
				want--
			}
			page, err := tr.Issues(ctx, time.Time{}, "tend", "")
			if err != nil || len(page.Issues) != want || page.Issues[0].Number != 2 {
				t.Fatalf("every labelled issue, across pages: %d %v", len(page.Issues), err)
			}
			since := g.Get(250).Updated
			if page, err = tr.Issues(ctx, since, "", ""); err != nil || len(page.Issues) != 11 {
				t.Fatalf("those updated since: %d %v", len(page.Issues), err)
			}
			c, err := tr.CreateComment(ctx, 2, "hello")
			if err != nil || c.Author != "tend-bot" {
				t.Fatalf("%+v %v", c, err)
			}
			if err := tr.EditComment(ctx, 2, c.ID, "hello again"); err != nil {
				t.Fatal(err)
			}
			g.DeleteComment(c.ID)
			if err := tr.EditComment(ctx, 2, c.ID, "x"); !errors.Is(err, tracker.ErrNotFound) {
				t.Fatalf("a deleted comment: %v", err)
			}
			if err := tr.Close(ctx, 2); err != nil || !g.Get(2).Closed {
				t.Fatal(err)
			}
			if err := tr.Label(ctx, 4, "done"); err != nil {
				t.Fatal(err)
			}
			if i, err := tr.Issue(ctx, 4); err != nil || len(i.Labels) != 2 || i.Closed {
				t.Fatalf("%+v %v", i, err)
			}
			g.Say(4, "ann", "please")
			cs, err := tr.Comments(ctx, 4)
			if err != nil || len(cs) != 1 || cs[0].Body != "please" || cs[0].Author != "ann" {
				t.Fatalf("only people's comments: %+v %v", cs, err)
			}
			g.RateLimit = 1
			var rl *tracker.RateLimited
			if _, err := tr.Issue(ctx, 4); !errors.As(err, &rl) || rl.Wait != 30*time.Second {
				t.Fatalf("rate limited: %v", err)
			}
			g.Refuse = true
			var ae *tracker.AuthError
			if _, err := tr.Me(ctx); !errors.As(err, &ae) || ae.Status != 401 {
				t.Fatalf("refused: %v", err)
			}
		})
	}
}

func TestEachTrackerOpensSubIssuesAndPullRequests(t *testing.T) {
	for _, kind := range []string{tracker.KindGitea, tracker.KindGitHub, tracker.KindGitLab} {
		t.Run(kind, func(t *testing.T) {
			g := trackertest.New(kind, "acme/app", "tend-bot", "tok")
			defer g.Close()
			tr, err := tracker.New(tracker.Config{Kind: kind, Base: g.URL, Repo: "acme/app", Token: "tok"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if r, err := tr.Repo(ctx); err != nil || r.DefaultBranch != "main" {
				t.Fatalf("the default branch: %+v %v", r, err)
			}
			g.Open(2, "parent", "body", "tend")
			kid, err := tr.CreateIssue(ctx, "child", "Part of #2")
			if err != nil || kid.Number <= 2 || kid.URL == "" || g.Get(kid.Number).Body != "Part of #2" {
				t.Fatalf("a new issue: %+v %v", kid, err)
			}
			if err := tr.LinkSubIssue(ctx, 2, kid); err != nil {
				t.Fatal(err)
			}
			if got, want := g.SubIssues(2), map[bool]int{true: 1}[kind == tracker.KindGitHub]; len(got) != want {
				t.Fatalf("only GitHub has sub-issues: %v", got)
			}
			if _, err := tr.PullRequest(ctx, "tend/t1"); !errors.Is(err, tracker.ErrNotFound) {
				t.Fatalf("no pull request yet: %v", err)
			}
			if _, err := tr.OpenPullRequest(ctx, "tend/t1", "main", "Add it", "For #2"); err == nil {
				t.Fatal("a branch the repository lacks is refused")
			}
			g.Branch("tend/t1")
			pr, err := tr.OpenPullRequest(ctx, "tend/t1", "main", "Add it", "For #2")
			if err != nil || pr.Number == 0 || pr.URL == "" {
				t.Fatalf("%+v %v", pr, err)
			}
			if again, err := tr.PullRequest(ctx, "tend/t1"); err != nil || again != pr {
				t.Fatalf("found by its branch: %+v %v", again, err)
			}
			if page, err := tr.Issues(ctx, time.Time{}, "", ""); err != nil || len(page.Issues) != 2 {
				t.Fatalf("a pull request is not an issue: %+v %v", page.Issues, err)
			}
		})
	}
}

func TestADeliveryIsTrustedOnlyWithItsSecret(t *testing.T) {
	body, secret := []byte(`{"action":"opened"}`), []byte("s3cret")
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	sig := hex.EncodeToString(m.Sum(nil))
	cases := []struct {
		kind, header, good string
	}{
		{tracker.KindGitea, "X-Gitea-Signature", sig},
		{tracker.KindGitHub, "X-Hub-Signature-256", "sha256=" + sig},
		{tracker.KindGitLab, "X-Gitlab-Token", "s3cret"},
	}
	for _, c := range cases {
		h := http.Header{}
		h.Set(c.header, c.good)
		if !tracker.Verify(c.kind, secret, body, h) || tracker.Verify(c.kind, []byte("other"), body, h) {
			t.Fatalf("%s: its own secret only", c.kind)
		}
		h.Set(c.header, "zz")
		if tracker.Verify(c.kind, secret, body, h) || tracker.Verify(c.kind, secret, body, http.Header{}) {
			t.Fatalf("%s: a wrong or missing signature", c.kind)
		}
	}
	if tracker.Verify("jira", secret, body, http.Header{}) {
		t.Fatal("an unknown kind")
	}
}

func TestADeliverySaysWhichIssue(t *testing.T) {
	cases := []struct {
		kind, body   string
		repo, number int64
	}{
		{tracker.KindGitea, `{"repository":{"id":42},"issue":{"number":7}}`, 42, 7},
		{tracker.KindGitHub, `{"repository":{"id":42},"issue":{"number":7},"comment":{"id":9}}`, 42, 7},
		{tracker.KindGitHub, `{"repository":{"id":42},"zen":"hi"}`, 42, 0},
		{tracker.KindGitLab, `{"object_kind":"issue","project":{"id":42},"object_attributes":{"iid":7,"id":900}}`, 42, 7},
		{tracker.KindGitLab, `{"object_kind":"note","project":{"id":42},"object_attributes":{"id":5},"issue":{"iid":7}}`, 42, 7},
		{tracker.KindGitLab, `{"object_kind":"push","project":{"id":42}}`, 42, 0},
	}
	for _, c := range cases {
		repo, n, err := tracker.HookIssue(c.kind, []byte(c.body))
		if err != nil || repo != c.repo || n != c.number {
			t.Fatalf("%s %s: %d %d %v", c.kind, c.body, repo, n, err)
		}
	}
	h := http.Header{}
	h.Set("X-Gitlab-Event-UUID", "u1")
	h.Set("X-GitHub-Delivery", "g1")
	if tracker.Delivery(tracker.KindGitLab, h) != "u1" || tracker.Delivery(tracker.KindGitHub, h) != "g1" || tracker.Delivery(tracker.KindGitea, h) != "" {
		t.Fatal("delivery ids")
	}
}

func TestOnlyKnownTrackersAndWholeRepositoryNames(t *testing.T) {
	bad := []tracker.Config{{Kind: "jira", Repo: "a/b"}, {Kind: tracker.KindGitea, Repo: "ab"}, {Kind: tracker.KindGitHub, Repo: "a/b/c"},
		{Kind: tracker.KindGitLab, Repo: "a//c"}}
	for _, c := range bad {
		if _, err := tracker.New(c); err == nil {
			t.Fatalf("%+v", c)
		}
	}
	if _, err := tracker.New(tracker.Config{Kind: tracker.KindGitLab, Repo: "group/sub/app"}); err != nil {
		t.Fatal(err)
	}
}
