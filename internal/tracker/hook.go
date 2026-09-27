package tracker

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"
)

// Verify: a webhook delivery of kind carries secret. Gitea and GitHub sign the body with it (X-Gitea-Signature,
// X-Hub-Signature-256); GitLab sends it as it is (X-Gitlab-Token).
func Verify(kind string, secret, body []byte, h http.Header) bool {
	switch kind {
	case KindGitea:
		return signed(secret, body, h.Get("X-Gitea-Signature"))
	case KindGitHub:
		sig, ok := strings.CutPrefix(h.Get("X-Hub-Signature-256"), "sha256=")
		return ok && signed(secret, body, sig)
	case KindGitLab:
		token := h.Get("X-Gitlab-Token")
		return token != "" && subtle.ConstantTimeCompare([]byte(token), secret) == 1
	}
	return false
}

// signed: sig is the hex HMAC-SHA256 of body under secret.
func signed(secret, body []byte, sig string) bool {
	m := hmac.New(sha256.New, secret)
	m.Write(body)
	want, err := hex.DecodeString(sig)
	return err == nil && hmac.Equal(m.Sum(nil), want)
}

// Delivery is the id a tracker gives a webhook delivery, to take each one once; "" when it gives none.
func Delivery(kind string, h http.Header) string {
	switch kind {
	case KindGitea:
		return h.Get("X-Gitea-Delivery")
	case KindGitHub:
		return h.Get("X-GitHub-Delivery")
	case KindGitLab:
		if id := h.Get("X-Gitlab-Event-UUID"); id != "" {
			return id
		}
		return h.Get("Idempotency-Key")
	}
	return ""
}

// HookIssue is the repository and the issue a delivery of kind is about; number is 0 when it is about no issue.
func HookIssue(kind string, body []byte) (repo, number int64, err error) {
	if kind == KindGitLab {
		var h struct {
			Kind    string `json:"object_kind"`
			Project struct {
				ID int64 `json:"id"`
			} `json:"project"`
			Attrs struct {
				IID int64 `json:"iid"`
			} `json:"object_attributes"`
			Issue struct {
				IID int64 `json:"iid"`
			} `json:"issue"`
		}
		if err := json.Unmarshal(body, &h); err != nil {
			return 0, 0, err
		}
		switch h.Kind {
		case "issue":
			return h.Project.ID, h.Attrs.IID, nil
		case "note":
			return h.Project.ID, h.Issue.IID, nil
		}
		return h.Project.ID, 0, nil
	}
	var h struct {
		Repository struct {
			ID int64 `json:"id"`
		} `json:"repository"`
		Issue struct {
			Number int64 `json:"number"`
		} `json:"issue"`
	}
	err = json.Unmarshal(body, &h)
	return h.Repository.ID, h.Issue.Number, err
}
