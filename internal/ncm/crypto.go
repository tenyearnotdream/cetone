// Package ncm is a client for the NetEase Cloud Music web API.
//
// Only the protocol layer lives here: request encryption (weapi/eapi), the
// cookie session, and one Go function per endpoint the application calls. It
// has no UI and no global state, so it is testable without a window.
package ncm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/url"
	"strings"
)

// The protocol's fixed material. They are constant in the web client and in
// every open-source client, not secrets: the server cannot tell them from the
// browser's own copy.
var (
	presetKey = []byte("0CoJUm6Qyw8W8jud")
	baseIV    = []byte("0102030405060708")
	eapiKey   = []byte("e82ckenh8dichen8")

	rsaPublicKeyPEM = []byte(`-----BEGIN PUBLIC KEY-----
MIGfMA0GCSqGSIb3DQEBAQUAA4GNADCBiQKBgQDgtQn2JZ34ZC28NWYpAUd98iZ37BUrX/aKzmFbt7clFSs6sXqHauqKWqdtLkF2KexO40H1YTX8z2lSgBBOAxLsvaklV8k4cBFK9snQXE9/DDaFt6Rr7iVZMldczhC0JNgTz+SHXT6CBHuX3e9SdB1Ua44oncaTWz7OBGLbCiK45wIDAQAB
-----END PUBLIC KEY-----`)

	base62 = []byte("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789")
)

// encryptMode is the wire format a request uses. The two are two different
// clients to the same service, not two versions of one:
//
//   - weapi is the web/PC client: a form post, and an "os=pc" cookie. It is what
//     the login uses, so the phone shows the sign-in as a PC one.
//   - eapi is the phone client: a hex payload to interface.music.163.com and an
//     "os=iphone" cookie. It is what the player-url endpoint is asked over.
//
// Which one a request uses is decided per endpoint, and the login is not part of
// it: a session created by the web QR code answers the same account, the same VIP
// level and the same tiers over both (measured). What the transport does decide is
// which parameters the endpoint accepts, and that is why the player URL is asked
// over eapi: /api/song/enhance/player/url/v1 names a level ("hires", "jyeffect",
// "jymaster"), while the weapi form of the same call speaks only in bit rates and
// stops at the Hi-Res file.
type encryptMode int

const (
	modeWeapi encryptMode = iota // www-form with params/encSecKey
	modeEapi                     // eapi hex payload, sent to interface.music.163.com
)

// pkcs7Pad appends the PKCS#7 padding block ciphers in the Go standard library
// do not add themselves. Every protocol payload needs it.
func pkcs7Pad(data []byte, blockSize int) []byte {
	n := blockSize - len(data)%blockSize
	out := make([]byte, len(data)+n)
	copy(out, data)
	for i := len(data); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

// pkcs7Unpad removes PKCS#7 padding, reporting whether the block was padded
// at all: a payload that is not a whole block count is returned unchanged.
func pkcs7Unpad(data []byte) []byte {
	if len(data) == 0 {
		return data
	}
	n := int(data[len(data)-1])
	if n == 0 || n > aes.BlockSize || n > len(data) {
		return data
	}
	for _, b := range data[len(data)-n:] {
		if int(b) != n {
			return data
		}
	}
	return data[:len(data)-n]
}

func aesCBCEncrypt(key, iv, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(out, padded)
	return out, nil
}

func aesCBCDecrypt(key, iv, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("ncm: ciphertext is not a whole number of blocks")
	}
	out := make([]byte, len(data))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(out, data)
	return pkcs7Unpad(out), nil
}

func aesECBEncrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += block.BlockSize() {
		block.Encrypt(out[i:i+block.BlockSize()], padded[i:i+block.BlockSize()])
	}
	return out, nil
}

func aesECBDecrypt(key, data []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(data)%block.BlockSize() != 0 {
		return nil, fmt.Errorf("ncm: ciphertext is not a whole number of blocks")
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += block.BlockSize() {
		block.Decrypt(out[i:i+block.BlockSize()], data[i:i+block.BlockSize()])
	}
	return pkcs7Unpad(out), nil
}

// rsaNoPad applies the textbook RSA operation the web client uses for
// encSecKey: no PKCS#1 padding, the message left-padded with zeros to the
// modulus size. Go's rsa package only offers padded operations, so the
// exponentiation is done directly.
func rsaNoPad(pub *rsa.PublicKey, msg []byte) ([]byte, error) {
	size := pub.Size()
	if len(msg) > size {
		return nil, fmt.Errorf("ncm: message of %d bytes exceeds the %d-byte modulus", len(msg), size)
	}
	padded := make([]byte, size)
	copy(padded[size-len(msg):], msg)
	m := new(big.Int).SetBytes(padded)
	if m.Cmp(pub.N) >= 0 {
		return nil, fmt.Errorf("ncm: message is not smaller than the modulus")
	}
	c := new(big.Int).Exp(m, big.NewInt(int64(pub.E)), pub.N)
	return c.FillBytes(make([]byte, size)), nil
}

func parseRSAPublicKey(pemBytes []byte) (*rsa.PublicKey, error) {
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("ncm: public key is not PEM")
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("ncm: parse public key: %w", err)
	}
	pub, ok := key.(*rsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("ncm: public key is not RSA")
	}
	return pub, nil
}

// weapiSecret draws the per-request 16-byte key: random bytes mapped onto the
// base62 alphabet, which keeps the key printable for the RSA step.
func weapiSecret() ([]byte, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, err
	}
	secret := make([]byte, 16)
	for i, b := range raw {
		secret[i] = base62[int(b)%len(base62)]
	}
	return secret, nil
}

// weapiPayload encrypts a JSON body the way the web page does: AES-CBC under
// the preset key, AES-CBC again under a random 16-byte base62 secret, with the
// secret itself reversed, zero-padded and RSA-encrypted as encSecKey.
func weapiPayload(payload []byte) (url.Values, error) {
	secret, err := weapiSecret()
	if err != nil {
		return nil, err
	}
	return weapiPayloadWithSecret(payload, secret)
}

// weapiPayloadWithSecret is weapiPayload on a key the caller chose, so a test
// can decrypt what it produced.
func weapiPayloadWithSecret(payload, secret []byte) (url.Values, error) {
	first, err := aesCBCEncrypt(presetKey, baseIV, payload)
	if err != nil {
		return nil, err
	}
	encoded := base64.StdEncoding.EncodeToString(first)
	second, err := aesCBCEncrypt(secret, baseIV, []byte(encoded))
	if err != nil {
		return nil, err
	}

	reversed := make([]byte, len(secret))
	for i := range secret {
		reversed[i] = secret[len(secret)-1-i]
	}
	pub, err := parseRSAPublicKey(rsaPublicKeyPEM)
	if err != nil {
		return nil, err
	}
	encSecKey, err := rsaNoPad(pub, reversed)
	if err != nil {
		return nil, err
	}

	return url.Values{
		"params":    {base64.StdEncoding.EncodeToString(second)},
		"encSecKey": {hex.EncodeToString(encSecKey)},
	}, nil
}

// eapiPayload encrypts a body for the eapi endpoints. The body is wrapped in
// a signed envelope ("nobody<path>use<body>md5forencrypt") so the server can
// verify the JSON it receives is the JSON the client signed.
func eapiPayload(path string, payload []byte) url.Values {
	body := string(payload)
	message := "nobody" + path + "use" + body + "md5forencrypt"
	sum := md5.Sum([]byte(message))
	envelope := path + "-36cd479b6b5-" + body + "-36cd479b6b5-" + hex.EncodeToString(sum[:])
	encrypted, err := aesECBEncrypt(eapiKey, []byte(envelope))
	if err != nil {
		// aes.NewCipher only fails on a wrong key length, and eapiKey is a
		// constant 16 bytes; an error here is a programming mistake.
		panic("ncm: eapi key: " + err.Error())
	}
	return url.Values{"params": {strings.ToUpper(hex.EncodeToString(encrypted))}}
}

// eapiDecryptResponse decrypts a response body that the server encrypted with
// the eapi key. Most endpoints answer in plain JSON; the eapi ones that come
// back as hex are routed through here.
func eapiDecryptResponse(body []byte) ([]byte, error) {
	ciphertext, err := hex.DecodeString(strings.TrimSpace(string(body)))
	if err != nil {
		return nil, fmt.Errorf("ncm: eapi response is not hex: %w", err)
	}
	return aesECBDecrypt(eapiKey, ciphertext)
}

// looksEncrypted reports whether a body is eapi ciphertext (hex only) rather
// than JSON, so a caller can decrypt where a response turns out encrypted.
func looksEncrypted(body []byte) bool {
	if len(body) < 32 || len(body)%2 != 0 {
		return false
	}
	for _, b := range body {
		switch {
		case b >= '0' && b <= '9', b >= 'a' && b <= 'f', b >= 'A' && b <= 'F':
		default:
			return false
		}
	}
	return true
}
