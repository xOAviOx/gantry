package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestValidSignature(t *testing.T) {
	secret := "s3cr3t"
	body := []byte(`{"ref":"refs/heads/main"}`)
	good := sign(secret, body)

	if !validSignature(secret, body, good) {
		t.Fatal("correct signature rejected")
	}
	if validSignature("wrong-secret", body, good) {
		t.Fatal("signature accepted under wrong secret")
	}
	if validSignature(secret, []byte(`{"ref":"tampered"}`), good) {
		t.Fatal("signature accepted for tampered body")
	}
	if validSignature(secret, body, "") {
		t.Fatal("empty header accepted")
	}
	if validSignature(secret, body, "sha1=deadbeef") {
		t.Fatal("wrong-prefix header accepted")
	}
	if validSignature(secret, body, "sha256=nothex") {
		t.Fatal("non-hex header accepted")
	}
}

func TestNormalizeRepo(t *testing.T) {
	want := "github.com/acme/widgets"
	forms := []string{
		"https://github.com/acme/widgets",
		"https://github.com/acme/widgets.git",
		"http://github.com/acme/widgets/",
		"git@github.com:acme/widgets.git",
		"ssh://git@github.com/acme/widgets.git",
		"https://x-access-token:TOKEN@github.com/acme/widgets.git",
		"GIT+HTTPS://GitHub.com/ACME/Widgets.git",
	}
	for _, f := range forms {
		if got := normalizeRepo(f); got != want {
			t.Errorf("normalizeRepo(%q) = %q, want %q", f, got, want)
		}
	}
}

func TestRepoMatches(t *testing.T) {
	var p githubPushPayload
	p.Repository.FullName = "acme/widgets"
	p.Repository.CloneURL = "https://github.com/acme/widgets.git"
	p.Repository.HTMLURL = "https://github.com/acme/widgets"
	p.Repository.SSHURL = "git@github.com:acme/widgets.git"

	for _, projURL := range []string{
		"https://github.com/acme/widgets",
		"git@github.com:acme/widgets.git",
		"https://github.com/acme/widgets.git",
	} {
		if !repoMatches(projURL, p) {
			t.Errorf("expected match for project repo_url %q", projURL)
		}
	}
	if repoMatches("https://github.com/acme/other", p) {
		t.Error("unexpected match for a different repo")
	}
	if repoMatches("", p) {
		t.Error("empty project repo_url should not match")
	}
	if repoMatches("/local/path/repo", p) {
		t.Error("local path should not match a github push")
	}
}
