//go:build live

package ncm

// The transport a request uses decides which parameters the API accepts. This
// asks the same song for its highest tier over both: weapi (the older PC one,
// which speaks in bit rates) and eapi (the phone one, which speaks in levels).
//
// What it shows is which one can even name 环绕声 / 沉浸声 / 母带. The login is not
// part of it: the session was created by a QR code on the web.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// parseSessionCookies reads the cookie map out of a saved session file.
func parseSessionCookies(t *testing.T, data []byte) map[string]string {
	t.Helper()
	var file struct {
		Cookies map[string]string `json:"cookies"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("session file: %v", err)
	}
	return file.Cookies
}

func liveSession(t *testing.T) (*Client, map[string]string) {
	t.Helper()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	path := filepath.Join(home, ".config", "cetone", "session.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no saved session: %v", err)
	}
	cookies := parseSessionCookies(t, data)
	if cookies["MUSIC_U"] == "" {
		t.Skip("the saved session carries no login token")
	}
	c := NewClient()
	c.LoadCookies(cookies)
	return c, cookies
}

// The modern endpoint names levels, and answers with the best tier the account
// holds.
func TestEapiNamesTheHighTiers(t *testing.T) {
	c, _ := liveSession(t)
	for _, level := range []string{"exhigh", "lossless", "hires", "jyeffect", "sky", "jymaster"} {
		var out struct {
			Data []struct {
				Level string `json:"level"`
				Type  string `json:"type"`
				Size  int64  `json:"size"`
				Br    int    `json:"br"`
				URL   string `json:"url"`
			} `json:"data"`
		}
		params := map[string]string{"ids": "[1970303952]", "level": level, "encodeType": "flac"}
		if level == "exhigh" {
			params["encodeType"] = "mp3"
		}
		if err := c.call(context.Background(), modeEapi, "/api/song/enhance/player/url/v1", params, &out); err != nil {
			t.Fatalf("%s: %v", level, err)
		}
		if len(out.Data) == 0 || out.Data[0].URL == "" {
			t.Logf("eapi %-9s -> no url", level)
			continue
		}
		d := out.Data[0]
		t.Logf("eapi %-9s -> level=%-9s type=%-4s br=%-7d size=%d", level, d.Level, d.Type, d.Br, d.Size)
	}
}

// The older PC endpoint speaks in bit rates, so it cannot ask for a tier above
// 320 kb/s by name at all.
func TestWeapiOnlySpeaksBitRates(t *testing.T) {
	c, _ := liveSession(t)
	for _, br := range []string{"320000", "999000"} {
		var out struct {
			Data []struct {
				Level string `json:"level"`
				Type  string `json:"type"`
				Size  int64  `json:"size"`
				Br    int    `json:"br"`
				URL   string `json:"url"`
			} `json:"data"`
		}
		params := map[string]string{"ids": "[1970303952]", "br": br}
		if err := c.call(context.Background(), modeWeapi, "/weapi/song/enhance/player/url", params, &out); err != nil {
			t.Logf("weapi br=%s -> error %v", br, err)
			continue
		}
		if len(out.Data) == 0 || out.Data[0].URL == "" {
			t.Logf("weapi br=%s -> no url", br)
			continue
		}
		d := out.Data[0]
		t.Logf("weapi br=%-7s -> level=%-9s type=%-4s br=%-7d size=%d", br, d.Level, d.Type, d.Br, d.Size)
	}
}

// The same session answers the same account over both transports: the login was
// created by a web QR code, and neither transport changes who it is.
func TestTheSameSessionOnBothModes(t *testing.T) {
	c, _ := liveSession(t)
	for _, mode := range []struct {
		name string
		m    encryptMode
	}{{"weapi", modeWeapi}, {"eapi", modeEapi}} {
		var out struct {
			Code    int `json:"code"`
			Account *struct {
				ID int64 `json:"id"`
			} `json:"account"`
			Profile *struct {
				UserID   int64  `json:"userId"`
				Nickname string `json:"nickname"`
				VipType  int    `json:"vipType"`
			} `json:"profile"`
		}
		if err := c.call(context.Background(), mode.m, "/api/nuser/account/get", nil, &out); err != nil {
			t.Fatalf("%s: %v", mode.name, err)
		}
		var uid int64
		var nick string
		var vip int
		if out.Profile != nil {
			uid, nick, vip = out.Profile.UserID, out.Profile.Nickname, out.Profile.VipType
		}
		t.Logf("%s -> code=%d uid=%d nick=%q vip=%d", mode.name, out.Code, uid, nick, vip)
	}
}
