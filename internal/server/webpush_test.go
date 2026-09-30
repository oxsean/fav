package server

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"
)

func b64(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(strings.ReplaceAll(s, " ", ""))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// RFC 8291 section 5 and appendix A: the same keys and salt give the same message, byte for byte.
func TestAPushMessageIsEncryptedAsRFC8291Says(t *testing.T) {
	as, err := ecdh.P256().NewPrivateKey(b64(t, "yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	ua := b64(t, "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4")
	got, err := encryptPush(as, b64(t, "DGv6ra1nlYgDCS1FRnbzlw"), ua, b64(t, "BTBZMqHH6r4Tts7J_aSIgg"), []byte("When I grow up, I want to be a watermelon"))
	if err != nil {
		t.Fatal(err)
	}
	want := append(b64(t, "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8"),
		b64(t, "8pfeW0KbunFT06SuDKoJH9Ql87S1QUrdirN6GcG7sFz1y1sqLgVi1VhjVkHsUoEsbI_0LpXMuGvnzQ")...)
	if !bytes.Equal(got, want) {
		t.Fatalf("got  %s\nwant %s", base64.RawURLEncoding.EncodeToString(got), base64.RawURLEncoding.EncodeToString(want))
	}
}

// decryptPush is the browser's side of RFC 8291.
func decryptPush(t *testing.T, ua *ecdh.PrivateKey, auth, body []byte) []byte {
	t.Helper()
	salt, rs, idlen := body[:16], binary.BigEndian.Uint32(body[16:20]), int(body[20])
	if rs != 4096 || idlen != 65 {
		t.Fatalf("header rs %d idlen %d", rs, idlen)
	}
	asPub, err := ecdh.P256().NewPublicKey(body[21 : 21+idlen])
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := ua.ECDH(asPub)
	prkKey, _ := hkdf.Extract(sha256.New, secret, auth)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(ua.PublicKey().Bytes())+string(asPub.Bytes()), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	plain = bytes.TrimRight(plain, "\x00")
	if len(plain) == 0 || plain[len(plain)-1] != 2 {
		t.Fatalf("no padding delimiter: %x", plain)
	}
	return plain[:len(plain)-1]
}

// Each message has its own salt and sender key, and the browser reads back what was sent; one too long for a push
// service is refused before it goes.
func TestAPushMessageIsFreshEachTimeAndFitsAPushService(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	rand.Read(auth)
	sub := webSubscription{Endpoint: "https://push.example/x"}
	sub.Keys.P256dh = base64.RawURLEncoding.EncodeToString(ua.PublicKey().Bytes())
	sub.Keys.Auth = base64.RawURLEncoding.EncodeToString(auth)
	a, err := sealPush(sub, []byte(`{"v":1}`))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := sealPush(sub, []byte(`{"v":1}`))
	if bytes.Equal(a[:16], b[:16]) || bytes.Equal(a[21:86], b[21:86]) {
		t.Fatal("two messages share a salt or a sender key")
	}
	if got := decryptPush(t, ua, auth, a); string(got) != `{"v":1}` {
		t.Fatalf("%q", got)
	}
	if _, err := sealPush(sub, bytes.Repeat([]byte("x"), maxPushPlain)); err != nil {
		t.Fatalf("the largest message: %v", err)
	}
	if _, err := sealPush(sub, bytes.Repeat([]byte("x"), maxPushPlain+1)); err == nil {
		t.Fatal("a message longer than a push service takes was sealed")
	}
	sub.Keys.Auth = "AAAA"
	if _, err := sealPush(sub, []byte("x")); err == nil {
		t.Fatal("a subscription with a short auth secret was taken")
	}
}

// RFC 8292: the push service's origin as the audience, an expiry within a day, the subject, signed ES256 with the
// server's key, whose public half goes beside it.
func TestTheVAPIDHeaderIsSignedByTheServersKey(t *testing.T) {
	priv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	e, _ := priv.PublicKey.ECDH()
	k := &PushKey{priv: priv, pub: e.Bytes()}
	now := time.Unix(1_800_000_000, 0)
	h, err := k.vapid("https://fcm.googleapis.com/fcm/send/abc?x=1", "https://tend.example", now)
	if err != nil {
		t.Fatal(err)
	}
	var jwt, key string
	for _, part := range strings.Split(strings.TrimPrefix(h, "vapid "), ", ") {
		if v, ok := strings.CutPrefix(part, "t="); ok {
			jwt = v
		} else if v, ok := strings.CutPrefix(part, "k="); ok {
			key = v
		}
	}
	if !strings.HasPrefix(h, "vapid ") || key != base64.RawURLEncoding.EncodeToString(k.pub) {
		t.Fatalf("header %q", h)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q", jwt)
	}
	var head, claims map[string]any
	json.Unmarshal(b64(t, parts[0]), &head)
	json.Unmarshal(b64(t, parts[1]), &claims)
	if head["alg"] != "ES256" || head["typ"] != "JWT" {
		t.Fatalf("head %v", head)
	}
	if claims["aud"] != "https://fcm.googleapis.com" || claims["sub"] != "https://tend.example" || claims["exp"] != float64(now.Add(12*time.Hour).Unix()) {
		t.Fatalf("claims %v", claims)
	}
	sig := b64(t, parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&priv.PublicKey, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("the signature does not verify with the server's key")
	}
}
