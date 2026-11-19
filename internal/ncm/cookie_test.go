package ncm

import (
	"strings"
	"sync"
	"testing"
)

// The device identity must survive a restart. The regression: loading a saved
// session did not mark the device fields as established, so the first request
// generated a new id and the server saw a new device on every launch.
func TestALoadedSessionKeepsItsDeviceIdentity(t *testing.T) {
	store := newCookieStore()
	saved := map[string]string{
		"_ntes_nuid":    "3d1bf8a54a9cfeee9924553844c889d1",
		"_ntes_nnid":    "3d1bf8a54a9cfeee9924553844c889d1,1794990000000",
		"NMTID":         "925cbb580a57bef2",
		"WNMCID":        "0d1658.1794990000000",
		"MUSIC_U":       "0037F2C2",
		"__csrf":        "d7ddd9aa",
		"WEVNSM":        "1.0.0",
		"__remember_me": "true",
	}
	store.load(saved)

	got := store.header(modeWeapi)
	if !strings.Contains(got, "_ntes_nuid=3d1bf8a54a9cfeee9924553844c889d1") {
		t.Fatalf("the loaded device id was replaced: %s", got)
	}
	if !strings.Contains(got, "NMTID=925cbb580a57bef2") {
		t.Fatalf("the loaded NMTID was replaced: %s", got)
	}
	if !strings.Contains(got, "MUSIC_U=0037F2C2") {
		t.Fatalf("the login token is missing from the header: %s", got)
	}
}

// A client with no saved session creates one device identity, and keeps it for
// every request it makes afterwards.
func TestAFreshClientKeepsTheDeviceItGenerated(t *testing.T) {
	store := newCookieStore()
	first := store.header(modeWeapi)
	device := fieldValue(t, first, "_ntes_nuid")
	if device == "" {
		t.Fatalf("no device id was generated: %s", first)
	}
	for i := 0; i < 5; i++ {
		if got := fieldValue(t, store.header(modeWeapi), "_ntes_nuid"); got != device {
			t.Fatalf("request %d used device id %q, want %q", i, got, device)
		}
		if got := fieldValue(t, store.header(modeEapi), "_ntes_nuid"); got != device {
			t.Fatalf("an eapi request used device id %q, want %q", got, device)
		}
	}
}

// The transport's own fields belong to one request: they change with the mode and
// are never stored, so they do not reach the session file.
func TestTheTransportFieldsAreNotStored(t *testing.T) {
	store := newCookieStore()
	weapi := store.header(modeWeapi)
	if got := fieldValue(t, weapi, "os"); got != "pc" {
		t.Fatalf("weapi sends os=%q", got)
	}
	eapi := store.header(modeEapi)
	if got := fieldValue(t, eapi, "os"); got != "iphone" {
		t.Fatalf("eapi sends os=%q", got)
	}
	// Back to weapi: the eapi values must not have stuck.
	if got := fieldValue(t, store.header(modeWeapi), "os"); got != "pc" {
		t.Fatalf("after an eapi request weapi sends os=%q", got)
	}
	saved := store.snapshot()
	for _, name := range []string{"os", "appver", "osver", "deviceId"} {
		if _, present := saved[name]; present {
			t.Fatalf("the snapshot for the session file carries %q", name)
		}
	}
	if saved["_ntes_nuid"] == "" {
		t.Fatal("the snapshot for the session file lost the device id")
	}
}

// Concurrent requests share one cookie jar, so building a header must not write
// to it. The regression was a map write under a read lock: several requests run
// at once when a page loads, and Go panics on a concurrent map write.
func TestHeadersCanBeBuiltConcurrently(t *testing.T) {
	store := newCookieStore()
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			mode := modeWeapi
			if i%2 == 0 {
				mode = modeEapi
			}
			if header := store.header(mode); header == "" {
				t.Error("an empty header")
			}
			_ = store.snapshot()
		}(i)
	}
	wg.Wait()
}

// fieldValue reads one field out of a Cookie header.
func fieldValue(t *testing.T, header, name string) string {
	t.Helper()
	for _, part := range strings.Split(header, "; ") {
		if key, value, ok := strings.Cut(part, "="); ok && key == name {
			return value
		}
	}
	return ""
}
