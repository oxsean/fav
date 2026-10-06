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

func TestUnder(t *testing.T) {
	for _, c := range []struct {
		name, path, dir, goos string
		want                  bool
	}{
		{"the directory itself", "/srv/app", "/srv/app", "linux", true},
		{"a child", "/srv/app/cmd/x", "/srv/app", "linux", true},
		{"trailing separators", "/srv/app/", "/srv/app//", "linux", true},
		{"a doubled separator and a dot", "/srv//app/./x", "/srv/app", "linux", true},
		{"a sibling with a common prefix", "/srv/appx", "/srv/app", "linux", false},
		{"the parent", "/srv", "/srv/app", "linux", false},
		{"the root holds everything", "/srv/app", "/", "darwin", true},
		{"POSIX keeps case", "/srv/App/x", "/srv/app", "linux", false},
		{"macOS keeps case too", "/Users/me/Dev/x", "/Users/me/dev", "darwin", false},
		{"a backslash is a name on POSIX", `/srv/app\x`, "/srv/app", "linux", false},
		{"dot dot", "/srv/app/../app/x", "/srv/app", "linux", false},
		{"dot dot in the directory", "/srv/app/x", "/srv/x/../app", "linux", false},
		{"relative", "srv/app/x", "srv/app", "linux", false},
		{"empty path", "", "/srv", "linux", false},
		{"empty directory", "/srv", "", "linux", false},
		{"a Windows path on POSIX", `C:\work\app`, `C:\work`, "linux", false},

		{"WSL mount", "/mnt/c/work/app", "/mnt/c/work", "linux", true},
		{"WSL mount root", "/mnt/c/work", "/mnt/c", "linux", true},
		{"WSL mount keeps case", "/mnt/c/Work/app", "/mnt/c/work", "linux", false},
		{"another WSL mount", "/mnt/d/work", "/mnt/c", "linux", false},
		{"WSL mount against its drive", "/mnt/c/work/app", `C:\work`, "linux", false},

		{"Windows child", `C:\work\app\x`, `C:\work\app`, "windows", true},
		{"Windows ignores case", `c:\WORK\App\x`, `C:\work\app`, "windows", true},
		{"forward slashes on Windows", "C:/work/app/x", `C:\work\app`, "windows", true},
		{"mixed separators", `C:\work/app\x`, "C:/work/app/", "windows", true},
		{"Windows trailing separator", `C:\work\app\`, `C:\work\app`, "windows", true},
		{"Windows sibling with a common prefix", `C:\work\appx`, `C:\work\app`, "windows", false},
		{"another drive", `D:\work\app`, `C:\work\app`, "windows", false},
		{"drive root", `C:\work`, `C:\`, "windows", true},
		{"bare drive", `C:\work`, "C:", "windows", false},
		{"drive-relative", `C:work\app`, `C:\work`, "windows", false},
		{"rooted without a drive", `\work\app`, `\work`, "windows", false},
		{"Windows relative", `work\app`, "work", "windows", false},
		{"Windows dot dot", `C:\work\..\work\app`, `C:\work`, "windows", false},
		{"UNC share itself", `\\srv\share`, `\\srv\share\`, "windows", true},
		{"under a UNC share", `\\srv\share\app\x`, `\\srv\share\app`, "windows", true},
		{"UNC ignores case", `\\SRV\Share\App`, `\\srv\share`, "windows", true},
		{"UNC with forward slashes", "//srv/share/app", `\\srv\share`, "windows", true},
		{"another share", `\\srv\share2\app`, `\\srv\share`, "windows", false},
		{"another server", `\\srv2\share\app`, `\\srv\share`, "windows", false},
		{"a server without a share", `\\srv\share`, `\\srv`, "windows", false},
		{"UNC against a drive", `\\srv\share\app`, `C:\share\app`, "windows", false},
		{"a POSIX path on Windows", "/mnt/c/work/app", "/mnt/c/work", "windows", false},
		{"a WSL path against its drive on Windows", "/mnt/c/work/app", `C:\work`, "windows", false},
	} {
		if got := Under(c.path, c.dir, c.goos); got != c.want {
			t.Errorf("%s: Under(%q, %q, %s) = %v, want %v", c.name, c.path, c.dir, c.goos, got, c.want)
		}
	}
}

func TestUnderAcrossMachines(t *testing.T) {
	under := map[string][2]string{
		"darwin":  {"/Users/me/dev/中文 项目/x", "/Users/me/dev/中文 项目"},
		"linux":   {"/home/me/dev/中文 项目/x", "/home/me/dev/中文 项目"},
		"wsl":     {"/mnt/c/dev/中文 项目/x", "/mnt/c/dev/中文 项目"},
		"windows": {`C:\dev\中文 项目\x`, `C:\dev\中文 项目`},
	}
	goos := map[string]string{"darwin": "darwin", "linux": "linux", "wsl": "linux", "windows": "windows"}
	for on, g := range goos {
		for pn, p := range under {
			for dn, d := range under {
				want := pn == dn && (goos[pn] == "windows") == (g == "windows")
				if got := Under(p[0], d[1], g); got != want {
					t.Errorf("on %s: Under(%q, %q) = %v, want %v", on, p[0], d[1], got, want)
				}
			}
		}
	}
}
