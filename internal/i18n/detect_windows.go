//go:build windows

package i18n

import "golang.org/x/sys/windows"

var procUILang = windows.NewLazySystemDLL("kernel32.dll").NewProc("GetUserDefaultUILanguage")

// Windows shells rarely set LANG: use the UI language.
func detectPlatform() string {
	if id, _, _ := procUILang.Call(); id&0x3ff == 0x04 { // LANG_CHINESE
		return ZH
	}
	return EN
}
