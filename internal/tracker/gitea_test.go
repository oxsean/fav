package tracker_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/oxsean/fav/internal/tracker"
	"github.com/oxsean/fav/internal/tracker/giteatest"
)

func TestGiteaReadsAndWritesWhatTheSyncNeeds(t *testing.T) {
	g := giteatest.New("acme/app", "tend-bot", "tok")
	defer g.Close()
	tr, err := tracker.New(tracker.Config{Kind: tracker.KindGitea, Base: g.URL + "/", Repo: "acme/app", Token: "tok"})
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
	for n := int64(1); n <= 60; n++ {
		g.Open(n, fmt.Sprintf("issue %d", n), "body", map[bool]string{true: "tend", false: "other"}[n%2 == 0])
	}
	page, err := tr.Issues(ctx, time.Time{}, "tend", "")
	if err != nil || len(page.Issues) != 30 || page.Issues[0].Number != 2 {
		t.Fatalf("every labelled issue, across pages: %d %v", len(page.Issues), err)
	}
	since := g.Get(50).Updated
	if page, err = tr.Issues(ctx, since, "", ""); err != nil || len(page.Issues) != 11 {
		t.Fatalf("those updated since: %d %v", len(page.Issues), err)
	}
	c, err := tr.CreateComment(ctx, 2, "hello")
	if err != nil || c.Author != "tend-bot" {
		t.Fatalf("%+v %v", c, err)
	}
	if err := tr.EditComment(ctx, c.ID, "hello again"); err != nil {
		t.Fatal(err)
	}
	cs, err := tr.Comments(ctx, 2)
	if err != nil || len(cs) != 1 || cs[0].Body != "hello again" {
		t.Fatalf("%+v %v", cs, err)
	}
	g.DeleteComment(c.ID)
	if err := tr.EditComment(ctx, c.ID, "x"); !errors.Is(err, tracker.ErrNotFound) {
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
}

func TestAGiteaDeliveryIsTrustedOnlyWithItsSignature(t *testing.T) {
	body, secret := []byte(`{"action":"opened"}`), []byte("s3cret")
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	sig := hex.EncodeToString(m.Sum(nil))
	if !tracker.GiteaSigned(secret, body, sig) || tracker.GiteaSigned([]byte("other"), body, sig) || tracker.GiteaSigned(secret, body, "zz") {
		t.Fatal("signature check")
	}
}

func TestOnlyKnownTrackersAndWholeRepositoryNames(t *testing.T) {
	for _, c := range []tracker.Config{{Kind: "jira", Repo: "a/b"}, {Kind: tracker.KindGitea, Repo: "ab"}, {Kind: tracker.KindGitea, Repo: "a/b/c"}} {
		if _, err := tracker.New(c); err == nil {
			t.Fatalf("%+v", c)
		}
	}
}
