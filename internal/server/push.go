package server

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"

	"github.com/oxsean/fav/internal/store"
)

// pushKeyName is the Web Push (VAPID) key pair's name among the server's sealed secrets.
const pushKeyName = "vapid"

// PushKey is the server's Web Push key pair: browsers bind their subscriptions to its public key.
type PushKey struct {
	priv *ecdsa.PrivateKey
	pub  []byte
}

// LoadPushKey reads the key pair from the team's database, making and keeping one (sealed) the first time. A kept one
// seal cannot open stays as it is and is an error: the server goes without push until its key comes back.
func LoadPushKey(team *store.Team, seal *Sealer) (*PushKey, error) {
	fresh, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	der, err := x509.MarshalPKCS8PrivateKey(fresh)
	if err != nil {
		return nil, err
	}
	kept, err := team.KeepSecret(pushKeyName, seal.Seal(der))
	if err != nil {
		return nil, err
	}
	plain, err := seal.Open(kept)
	if err != nil {
		return nil, fmt.Errorf("the Web Push key: %w", err)
	}
	k, err := x509.ParsePKCS8PrivateKey(plain)
	priv, ok := k.(*ecdsa.PrivateKey)
	if err != nil || !ok || priv.Curve != elliptic.P256() {
		return nil, errors.Join(errors.New("the Web Push key is not a P-256 key"), err)
	}
	e, err := priv.PublicKey.ECDH()
	if err != nil {
		return nil, err
	}
	return &PushKey{priv: priv, pub: e.Bytes()}, nil
}

// Public is the public key as an uncompressed point, what a browser's applicationServerKey takes.
func (k *PushKey) Public() []byte { return k.pub }

// pushKey answers the public key for a page subscribing to push, base64url without padding.
func (s *Server) pushKey(w http.ResponseWriter, r *http.Request, c caller) {
	if s.opt.Push == nil {
		apiError(w, http.StatusServiceUnavailable, "push_key")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"key": base64.RawURLEncoding.EncodeToString(s.opt.Push.Public())})
}
