package capture

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var assocQueryString = windows.NewLazySystemDLL("shlwapi.dll").NewProc("AssocQueryStringW")

// ⚠️ ASSOCF_IS_PROTOCOL; ASSOCSTR_FRIENDLYAPPNAME, ASSOCSTR_EXECUTABLE (packaged apps may only have the name).
const (
	assocfIsProtocol   = 0x1000
	assocstrAppName    = 4
	assocstrExecutable = 2
)

func schemeHandler(scheme string) string {
	s, err := windows.UTF16PtrFromString(scheme)
	if err != nil || assocQueryString.Find() != nil {
		return ""
	}
	var parts []string
	for _, what := range []uintptr{assocstrAppName, assocstrExecutable} {
		buf := make([]uint16, 1024)
		n := uint32(len(buf))
		hr, _, _ := assocQueryString.Call(assocfIsProtocol, what, uintptr(unsafe.Pointer(s)), 0,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)))
		if hr == 0 {
			parts = append(parts, windows.UTF16ToString(buf[:n]))
		}
	}
	return strings.Join(parts, " ")
}

func openURL(u string) error {
	verb, _ := windows.UTF16PtrFromString("open")
	file, err := windows.UTF16PtrFromString(u)
	if err != nil {
		return err
	}
	return windows.ShellExecute(0, verb, file, nil, nil, windows.SW_SHOWNORMAL)
}
