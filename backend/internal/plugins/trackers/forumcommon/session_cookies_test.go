package forumcommon

import (
	"net/http"
	"net/url"
	"testing"
)

func TestCookiesByName_ReturnsRequestedCookies(t *testing.T) {
	sess := New().GetOrCreate("k", "ua")
	u, _ := url.Parse("https://example.com/")
	sess.Client.Jar.SetCookies(u, []*http.Cookie{
		{Name: "lf_session", Value: "abc"},
		{Name: "PHPSESSID", Value: "xyz"},
	})
	got := CookiesByName(sess, u, []string{"lf_session"})
	if got["lf_session"] != "abc" {
		t.Errorf("lf_session = %q, want abc", got["lf_session"])
	}
	if _, ok := got["PHPSESSID"]; ok {
		t.Error("PHPSESSID should not be harvested")
	}
}
