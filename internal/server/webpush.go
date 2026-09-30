package server

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Web Push: the message encrypted for one browser (RFC 8291, aes128gcm of RFC 8188 in one record) and the server
// named to its push service (VAPID, RFC 8292).

// ⚠️ A push service takes a body of at most 4096 bytes: the 86-byte header, the 16-byte tag and the padding
// delimiter leave this much for the message.
const maxPushPlain = 4096 - 86 - 16 - 1

// webSubscription is a browser's push subscription as PushSubscription.toJSON gives it.
type webSubscription struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

func (s webSubscription) check() error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(s.Endpoint) > 2048 {
		return errors.New("push subscription: the endpoint is not an https address")
	}
	ua, err := base64.RawURLEncoding.DecodeString(s.Keys.P256dh)
	if err == nil {
		_, err = ecdh.P256().NewPublicKey(ua)
	}
	if err != nil {
		return fmt.Errorf("push subscription: p256dh: %w", err)
	}
	if auth, err := base64.RawURLEncoding.DecodeString(s.Keys.Auth); err != nil || len(auth) != 16 {
		return errors.New("push subscription: auth is not 16 bytes")
	}
	return nil
}

// sealPush encrypts plain for the browser that subscribed with sub, with a fresh sender key and salt.
func sealPush(sub webSubscription, plain []byte) ([]byte, error) {
	if err := sub.check(); err != nil {
		return nil, err
	}
	ua, _ := base64.RawURLEncoding.DecodeString(sub.Keys.P256dh)
	auth, _ := base64.RawURLEncoding.DecodeString(sub.Keys.Auth)
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	return encryptPush(as, salt, ua, auth, plain)
}

// encryptPush is RFC 8291 section 3 with the sender key as and salt given.
func encryptPush(as *ecdh.PrivateKey, salt, uaPublic, auth, plain []byte) ([]byte, error) {
	if len(plain) > maxPushPlain {
		return nil, fmt.Errorf("push message: %d bytes, at most %d", len(plain), maxPushPlain)
	}
	ua, err := ecdh.P256().NewPublicKey(uaPublic)
	if err != nil {
		return nil, err
	}
	secret, err := as.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPublic := as.PublicKey().Bytes()
	prkKey, err := hkdf.Extract(sha256.New, secret, auth)
	if err != nil {
		return nil, err
	}
	ikm, err := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(uaPublic)+string(asPublic), 32)
	if err != nil {
		return nil, err
	}
	prk, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		return nil, err
	}
	cek, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	if err != nil {
		return nil, err
	}
	nonce, err := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 0, 86)
	head = append(head, salt...)
	head = binary.BigEndian.AppendUint32(head, 4096)
	head = append(head, byte(len(asPublic)))
	head = append(head, asPublic...)
	return gcm.Seal(head, nonce, append(plain, 2), nil), nil
}

// vapid is the Authorization header that names this server to the push service at endpoint, valid 12 hours from now.
func (k *PushKey) vapid(endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	head := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": subject})
	input := head + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, k.priv, sum[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + input + "." + enc.EncodeToString(sig) + ", k=" + enc.EncodeToString(k.pub), nil
}
