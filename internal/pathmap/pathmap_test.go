package pathmap

import "testing"

var (
	mac   = End{OS: "darwin", Home: "/Users/ozn", Host: "mbp"}
	linux = End{OS: "linux", Home: "/root", Host: "opsbox"}
	win   = End{OS: "windows", Home: `C:\Users\Administrator`, Host: "lg-win"}
	wsl   = End{OS: "linux", Home: "/home/ozn", Host: "lg-win", WSL: true}
)

func TestEveryPairMapsUnderHome(t *testing.T) {
	ends := map[string]End{"mac": mac, "linux": linux, "win": win, "wsl": wsl}
	under := map[string]string{
		"mac":   "/Users/ozn/dev/中文 项目/a b.txt",
		"linux": "/root/dev/中文 项目/a b.txt",
		"win":   `C:\Users\Administrator\dev\中文 项目\a b.txt`,
		"wsl":   "/home/ozn/dev/中文 项目/a b.txt",
	}
	for fn, from := range ends {
		for tn, to := range ends {
			got, ok := Map(under[fn], from, to)
			want := under[tn]
			if fn == "win" && tn == "wsl" {
				want = "/mnt/c/Users/Administrator/dev/中文 项目/a b.txt" // the same disk wins over the home rule
			}
			if !ok || got != want {
				t.Errorf("%s → %s: %q %v, want %q", fn, tn, got, ok, want)
			}
		}
	}
}

func TestMap(t *testing.T) {
	far := win
	far.Host = "other-pc"
	for _, c := range []struct {
		name     string
		p        string
		from, to End
		want     string
		ok       bool
	}{
		{"home itself", "/Users/ozn", mac, linux, "/root", true},
		{"trailing separator", "/Users/ozn/x/", mac, win, `C:\Users\Administrator\x`, true},
		{"forward slashes on Windows", "C:/Users/Administrator/x", win, mac, "/Users/ozn/x", true},
		{"Windows home ignores case", `c:\users\ADMINISTRATOR\x`, win, linux, "/root/x", true},
		{"POSIX home keeps case", "/users/ozn/x", mac, linux, "", false},
		{"outside home", "/opt/x", linux, mac, "", false},
		{"home prefix is not a parent", "/Users/oznx/a", mac, linux, "", false},
		{"dot dot", "/Users/ozn/../other", mac, linux, "", false},
		{"relative", "dev/x", mac, linux, "", false},
		{"relative on Windows", `dev\x`, win, mac, "", false},
		{"drive-relative", `C:dev\x`, win, mac, "", false},
		{"bare drive", `C:`, win, wsl, "", false},
		{"drive root", `C:\`, win, wsl, "/mnt/c", true},
		{"rooted without a drive", `\Users\Administrator\x`, win, mac, "", false},
		{"UNC", `\\server\share\x`, win, wsl, "", false},
		{"UNC with forward slashes", "//server/share/x", win, mac, "", false},
		{"drive to WSL", `D:\work\app`, win, wsl, "/mnt/d/work/app", true},
		{"lowercase drive to WSL", `d:\work`, win, wsl, "/mnt/d/work", true},
		{"drive root to WSL", `C:\`, win, wsl, "/mnt/c", true},
		{"WSL mount to drive", "/mnt/d/work/中文 x", wsl, win, `D:\work\中文 x`, true},
		{"WSL mount root", "/mnt/c", wsl, win, `C:\`, true},
		{"WSL home to Windows home", "/home/ozn/x", wsl, win, `C:\Users\Administrator\x`, true},
		{"WSL mount to another machine", "/mnt/d/work", wsl, far, "", false},
		{"other drive to another machine", `D:\work`, win, End{OS: "linux", Home: "/home/ozn", Host: "elsewhere", WSL: true}, "", false},
		{"a name Windows cannot hold", "/Users/ozn/a:b", mac, win, "", false},
		{"a backslash in a POSIX name", `/Users/ozn/a\b`, mac, win, "", false},
		{"no target home", "/Users/ozn/x", mac, End{OS: "linux"}, "", false},
	} {
		got, ok := Map(c.p, c.from, c.to)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: Map(%q) = %q %v, want %q %v", c.name, c.p, got, ok, c.want, c.ok)
		}
	}
}
