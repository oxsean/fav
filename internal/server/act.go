package server

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/coord"
	"github.com/oxsean/fav/internal/store"
	"github.com/oxsean/fav/internal/wire"
)

// ⚠️ The action tokens' key among the server's sealed secrets, what a token starts with, and how long one acts: as
// long as a push service keeps the push it rides in.
const (
	actKeyName = "act"
	actPrefix  = "a1."
	actLife    = pushTTL
)

// ActKey signs the tokens a push carries for its buttons: whoever holds one acts on the item it names, as it names it.
type ActKey struct{ key []byte }

// LoadActKey reads the key from the team's database, making and keeping one (sealed) the first time.
func LoadActKey(team *store.Team, seal *Sealer) (*ActKey, error) {
	fresh := make([]byte, 32)
	rand.Read(fresh)
	kept, err := team.KeepSecret(actKeyName, seal.Seal(fresh))
	if err != nil {
		return nil, err
	}
	key, err := seal.Open(kept)
	if err != nil || len(key) != 32 {
		return nil, errors.Join(errors.New("the action key"), err)
	}
	return &ActKey{key: key}, nil
}

// actClaim is what a token says: who acts, on which item at which version, what they do, from which notice (seq) to
// which device, and until when (unix seconds).
type actClaim struct {
	User    string `json:"u"`
	Task    string `json:"t"`
	Item    string `json:"i"`
	Version int64  `json:"v"`
	Action  string `json:"a"`
	Seq     int64  `json:"s"`
	Device  string `json:"d"`
	Until   int64  `json:"x"`
}

var (
	errActToken   = errors.New("token")
	errActExpired = errors.New("expired")
)

func (k *ActKey) sign(body string) string {
	m := hmac.New(sha256.New, k.key)
	m.Write([]byte(actPrefix + body))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// mint is c's token: a1.<claim>.<signature>, both base64url.
func (k *ActKey) mint(c actClaim) string {
	b, _ := json.Marshal(c)
	body := base64.RawURLEncoding.EncodeToString(b)
	return actPrefix + body + "." + k.sign(body)
}

// open is what tok says, when this key signed it and it still holds at now.
func (k *ActKey) open(tok string, now time.Time) (actClaim, error) {
	rest, ok := strings.CutPrefix(tok, actPrefix)
	body, mac, ok2 := strings.Cut(rest, ".")
	if !ok || !ok2 || !hmac.Equal([]byte(mac), []byte(k.sign(body))) {
		return actClaim{}, errActToken
	}
	var c actClaim
	b, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil || json.Unmarshal(b, &c) != nil {
		return actClaim{}, errActToken
	}
	if now.Unix() >= c.Until {
		return actClaim{}, errActExpired
	}
	return c, nil
}

// actor is the coordinator as /api/act uses it.
type actor interface {
	Act(user string, on coord.ActOn) error
}

// act does what a notice's button says, for the one signed in if the token is theirs: the service worker sends it
// with the page's cookie. Someone else answered first: request_gone, with their name.
func (s *Server) act(w http.ResponseWriter, r *http.Request, c caller) {
	if s.opt.Act == nil {
		apiError(w, http.StatusServiceUnavailable, "act_key")
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	if !decode(w, r, &in) {
		return
	}
	if u, ok, err := s.team().User(c.user.ID); err != nil || !ok || u.Disabled {
		apiError(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	claim, err := s.opt.Act.open(in.Token, time.Now())
	if err == nil && claim.User != c.user.ID {
		err = errActToken
	}
	switch {
	case errors.Is(err, errActExpired):
		apiError(w, http.StatusGone, "expired")
		return
	case err != nil:
		s.audit(r, c.user.ID, "denied", "push.act")
		apiError(w, http.StatusForbidden, "token")
		return
	}
	err = s.acts.Act(c.user.ID, coord.ActOn{Task: claim.Task, Item: claim.Item, Version: claim.Version, Action: claim.Action})
	result := "ok"
	if err != nil {
		result = wire.Code(err)
	}
	s.audit(r, c.user.ID, "push.act", fmt.Sprintf("device=%s task=%s item=%s action=%s result=%s", claim.Device, claim.Task, claim.Item, claim.Action, result))
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case wire.Code(err) == wire.CodeRequestGone:
		out := map[string]string{"error": wire.CodeRequestGone}
		var we *wire.Error
		if errors.As(err, &we) && we.Detail != "" {
			if u, ok, _ := s.team().User(we.Detail); ok {
				out["by"] = displayName(u)
			}
		}
		writeJSON(w, http.StatusConflict, out)
	default:
		apiError(w, wireStatus(err), wire.Code(err))
	}
}
