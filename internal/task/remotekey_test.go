package task

import "testing"

func TestRemoteKey(t *testing.T) {
	for _, c := range []struct{ url, want string }{
		{"git@example.com:acme/shop.git", "example.com/acme/shop"},
		{"git@Example.COM:acme/shop", "example.com/acme/shop"},
		{"ssh://git@example.com/acme/shop", "example.com/acme/shop"},
		{"ssh://git@example.com:2222/acme/shop.git", "example.com/acme/shop"},
		{"https://example.com/acme/shop", "example.com/acme/shop"},
		{"https://example.com/acme/shop.git", "example.com/acme/shop"},
		{"https://user:secret@example.com:8443/acme/shop.git/", "example.com/acme/shop"},
		{"http://example.com/acme/shop/", "example.com/acme/shop"},
		{"git://example.com/acme/shop.git", "example.com/acme/shop"},
		{"ssh://example.com/~user/shop", "example.com/~user/shop"},
		{"  https://example.com/group/sub/shop.git  ", "example.com/group/sub/shop"},
		{"https://example.com/Acme/Shop", "example.com/Acme/Shop"},
		{"", ""},
		{"   ", ""},
		{"/srv/git/shop.git", "/srv/git/shop"},
		{"file:///srv/git/shop.git", "/srv/git/shop"},
		{"git@example.com:", ""},
		{"https://", ""},
	} {
		if got := RemoteKey(c.url); got != c.want {
			t.Errorf("RemoteKey(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}
