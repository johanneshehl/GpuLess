package main

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPasswordHashing(t *testing.T) {
	if _, err := hashPassword("short"); err != errWeakPassword {
		t.Errorf("a nine-character password should be refused, got %v", err)
	}
	// bcrypt silently ignores anything past 72 bytes, so a longer passphrase
	// would be weaker than it looks. Refuse rather than mislead.
	if _, err := hashPassword(strings.Repeat("a", 73)); err == nil {
		t.Error("a password past bcrypt's 72-byte limit should be refused")
	}

	hash, err := hashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(hash, "correct") {
		t.Fatal("the hash contains the password")
	}
	if !checkPassword(hash, "correct horse battery") {
		t.Error("the right password did not verify")
	}
	if checkPassword(hash, "Correct horse battery") {
		t.Error("verification is case-insensitive")
	}
	if checkPassword("not a hash", "correct horse battery") {
		t.Error("a malformed hash must not verify")
	}
}

func TestTokensAreDistinctAndHashed(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		tok := randomToken(32)
		if seen[tok] {
			t.Fatal("randomToken repeated itself")
		}
		seen[tok] = true
	}
	tok := randomToken(32)
	if hashToken(tok) == tok {
		t.Error("the session table must hold a hash, not the cookie value")
	}
	if hashToken(tok) != hashToken(tok) {
		t.Error("hashToken must be stable")
	}
}

func TestThrottle(t *testing.T) {
	th := newThrottle(3, 50*time.Millisecond)

	if locked, _ := th.Locked("a"); locked {
		t.Fatal("a fresh address should not be locked")
	}
	th.Fail("a")
	th.Fail("a")
	if locked, _ := th.Locked("a"); locked {
		t.Error("locked one failure too early")
	}
	th.Fail("a")
	locked, wait := th.Locked("a")
	if !locked || wait <= 0 {
		t.Fatalf("should be locked with a wait, got %v %v", locked, wait)
	}
	// Another address is unaffected.
	if l, _ := th.Locked("b"); l {
		t.Error("one address locked another out")
	}

	time.Sleep(60 * time.Millisecond)
	if l, _ := th.Locked("a"); l {
		t.Error("the lock should expire")
	}

	th.Fail("c")
	th.Reset("c")
	th.Fail("c")
	th.Fail("c")
	if l, _ := th.Locked("c"); l {
		t.Error("Reset should clear the count")
	}
}

func TestClientIPHonoursTheProxyFlagOnly(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.0.2.10:5555"
	r.Header.Set("X-Forwarded-For", "203.0.113.9, 70.41.3.18")

	trustProxy = false
	t.Cleanup(func() { trustProxy = false })
	if got := clientIP(r); got != "192.0.2.10" {
		t.Errorf("without -behind-proxy the direct address must win, got %q", got)
	}

	trustProxy = true
	if got := clientIP(r); got != "203.0.113.9" {
		t.Errorf("behind a proxy the first forwarded address should win, got %q", got)
	}
}

func TestIsHTTPS(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Forwarded-Proto", "https")

	trustProxy = false
	t.Cleanup(func() { trustProxy = false })
	if isHTTPS(r) {
		t.Error("an untrusted header must not mark the connection secure")
	}
	trustProxy = true
	if !isHTTPS(r) {
		t.Error("behind a trusted proxy the header should count")
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("got %q", got)
	}
	if got := truncate("hello", 3); got != "hel" {
		t.Errorf("got %q", got)
	}
}
