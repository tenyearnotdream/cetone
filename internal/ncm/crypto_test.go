package ncm

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPKCS7RoundTrip(t *testing.T) {
	for _, size := range []int{0, 1, 15, 16, 17, 31, 32} {
		plain := make([]byte, size)
		rand.Read(plain)
		padded := pkcs7Pad(plain, 16)
		if len(padded)%16 != 0 || len(padded) <= len(plain) {
			t.Fatalf("size %d: padded to %d bytes", size, len(padded))
		}
		if got := pkcs7Unpad(padded); string(got) != string(plain) {
			t.Fatalf("size %d: round trip changed the data", size)
		}
	}
}

func TestAESCBCCiphertextIsNotPlaintext(t *testing.T) {
	plain := []byte(`{"s":"海阔天空","type":1}`)
	enc, err := aesCBCEncrypt(presetKey, baseIV, plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(enc), "海阔天空") {
		t.Fatal("the ciphertext contains the plaintext")
	}
	dec, err := aesCBCDecrypt(presetKey, baseIV, enc)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Fatalf("decrypted %q, want %q", dec, plain)
	}
}

func TestECBRoundTrip(t *testing.T) {
	plain := []byte("body=1&queryString=e_r%3Dtrue")
	enc, err := aesECBEncrypt(eapiKey, plain)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := aesECBDecrypt(eapiKey, enc)
	if err != nil {
		t.Fatal(err)
	}
	if string(dec) != string(plain) {
		t.Fatalf("decrypted %q, want %q", dec, plain)
	}
}

// TestRSANoPadMatchesTextbookRSA checks the raw exponentiation against a key
// pair whose private half we own: c = m^e mod n, and m must come back.
func TestRSANoPadMatchesTextbookRSA(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("0123456789abcdef")
	enc, err := rsaNoPad(&key.PublicKey, msg)
	if err != nil {
		t.Fatal(err)
	}
	if len(enc) != key.Size() {
		t.Fatalf("ciphertext is %d bytes, want the %d-byte modulus", len(enc), key.Size())
	}
	plain := new(big.Int).Exp(new(big.Int).SetBytes(enc), key.D, key.N)
	want := new(big.Int).SetBytes(msg)
	if plain.Cmp(want) != 0 {
		t.Fatal("the textbook inverse does not return the message")
	}
}

// TestWeapiPayloadLayout decrypts what the payload carries, which is what the
// server does: AES-CBC under the secret, then AES-CBC under the preset key,
// and the secret itself reverse-encrypted as encSecKey.
func TestWeapiPayloadLayout(t *testing.T) {
	body, err := json.Marshal(map[string]string{"s": "海阔天空", "type": "1", "csrf_token": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("abcdefghijklmnop")
	values, err := weapiPayloadWithSecret(body, secret)
	if err != nil {
		t.Fatal(err)
	}
	second, err := base64.StdEncoding.DecodeString(values.Get("params"))
	if err != nil {
		t.Fatal(err)
	}
	firstB64, err := aesCBCDecrypt(secret, baseIV, second)
	if err != nil {
		t.Fatal(err)
	}
	first, err := base64.StdEncoding.DecodeString(string(firstB64))
	if err != nil {
		t.Fatal(err)
	}
	plain, err := aesCBCDecrypt(presetKey, baseIV, first)
	if err != nil {
		t.Fatal(err)
	}
	if string(plain) != string(body) {
		t.Fatalf("recovered %s, want %s", plain, body)
	}
	encSecKey := values.Get("encSecKey")
	if len(encSecKey) != 256 {
		t.Fatalf("encSecKey is %d hex characters, want 256", len(encSecKey))
	}
}

// TestEapiEnvelope round-trips a payload the way the server parses it: the
// params value is uppercase hex of the eapi-key ciphertext, whose body is
// "<path>-36cd479b6b5-<json>-36cd479b6b5-<signed digest>".
func TestEapiEnvelope(t *testing.T) {
	path := "/api/song/enhance/player/url/v1"
	body := `{"ids":"[1,2]","level":"exhigh","csrf_token":"x"}`
	values := eapiPayload(path, []byte(body))
	params := values.Get("params")
	if params != strings.ToUpper(params) {
		t.Fatal("params is not uppercase hex")
	}
	ciphertext, err := hex.DecodeString(params)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := aesECBDecrypt(eapiKey, ciphertext)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(plain), "-36cd479b6b5-")
	if len(parts) != 3 {
		t.Fatalf("envelope has %d parts, want 3: %q", len(parts), plain)
	}
	if parts[0] != path {
		t.Fatalf("envelope path is %q, want %q", parts[0], path)
	}
	if parts[1] != body {
		t.Fatalf("envelope body is %q, want %q", parts[1], body)
	}
	if len(parts[2]) != 32 {
		t.Fatalf("digest is %d characters, want 32", len(parts[2]))
	}
}

// TestRequestAnswerCodes covers the envelope check that turns a wrong code into
// an *APIError rather than a silent zero value.
func TestRequestAnswerCodes(t *testing.T) {
	err := (&APIError{Path: "/weapi/x", Code: 301, Msg: "需要登录"}).Error()
	if !strings.Contains(err, "301") || !strings.Contains(err, "需要登录") {
		t.Fatalf("APIError reads %q", err)
	}
}

func TestCoverURL(t *testing.T) {
	raw := "https://p1.music.126.net/abc/def.jpg"
	got := CoverURL(raw, 200)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Query().Get("param") != "200y200" {
		t.Fatalf("cover url %q does not carry the size request", got)
	}
	if other := "https://example.com/a.jpg"; CoverURL(other, 100) != other {
		t.Fatal("a non-NetEase url was rewritten")
	}
}

func TestParseLRC(t *testing.T) {
	raw := "[ti:海阔天空]\n[offset:100]\n[00:01.50]今天我\n[00:05.20][00:09.20]寒夜里看雪飘过\n[00:12.00]\n"
	trans := "[00:01.50]Today I\n[00:05.20]Watching the snow drift in the cold night\n"
	lines := ParseLRC(raw, trans)
	if len(lines) != 3 {
		t.Fatalf("parsed %d lines, want 3: %+v", len(lines), lines)
	}
	if lines[0].Text != "今天我" {
		t.Fatalf("first line is %q", lines[0].Text)
	}
	if lines[0].At != 1600*time.Millisecond {
		t.Fatalf("first line starts at %v, want 1.6s (with the file offset)", lines[0].At)
	}
	if lines[0].Translation != "Today I" {
		t.Fatalf("first translation is %q", lines[0].Translation)
	}
	if lines[1].At != 5300*time.Millisecond || lines[2].At != 9300*time.Millisecond {
		t.Fatalf("the two timestamps of one line became %v and %v", lines[1].At, lines[2].At)
	}
	if lines[1].Translation == "" || lines[2].Translation != "" {
		t.Fatal("the translation was not attached to its one timestamp")
	}

	lyric := Lyric{Lines: lines}
	idx, ok := lyric.LineAt(4000 * time.Millisecond)
	if !ok || idx != 0 {
		t.Fatalf("at 4s the parser chose line %d (%v)", idx, ok)
	}
	if idx, ok = lyric.LineAt(6000 * time.Millisecond); !ok || idx != 1 {
		t.Fatalf("at 6s the parser chose line %d (%v)", idx, ok)
	}
	if _, ok := lyric.LineAt(500 * time.Millisecond); ok {
		t.Fatal("a time before the first line chose a line")
	}
}
