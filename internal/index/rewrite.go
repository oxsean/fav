package index

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"time"

	"github.com/oxsean/fav/internal/fileio"
	"github.com/oxsean/fav/internal/paths"
)

var cwdKey = []byte(`"cwd":"`)

// RewriteFile writes src into dst through RewriteCwd (mapCwd nil: as it is), each byte also to tee when set; dst keeps
// src's mtime. lines are RewriteCwd's, 0 for a copy.
func RewriteFile(src, dst string, tee io.Writer, mapCwd func(string) (string, bool)) (lines int, err error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	err = fileio.WriteAtomic(dst, 0o600, func(w io.Writer) error {
		if tee != nil {
			w = io.MultiWriter(w, tee)
		}
		if mapCwd == nil {
			_, err := io.Copy(w, in)
			return err
		}
		lines, err = RewriteCwd(in, w, mapCwd)
		return err
	})
	if err != nil {
		return lines, err
	}
	if st, err := in.Stat(); err == nil {
		os.Chtimes(dst, time.Now(), st.ModTime())
	}
	return lines, nil
}

// RewriteCwd copies r to w line by line with every "cwd" value given to mapCwd decoded (a file:// prefix taken off and
// put back) and written back encoded when it answers true; lines is how many it wrote, a last one without a newline
// counted. ⚠️ Only the cwd field: paths in message bodies are history and must match what happened.
func RewriteCwd(r io.Reader, w io.Writer, mapCwd func(string) (string, bool)) (lines int, err error) {
	br := bufio.NewReaderSize(r, 1<<20)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			lines++
			if _, werr := w.Write(rewriteLine(line, mapCwd)); werr != nil {
				return lines, werr
			}
		}
		if err == io.EOF {
			return lines, nil
		}
		if err != nil { // a read error must not look like EOF: renaming a partial file loses data
			return lines, err
		}
	}
}

func rewriteLine(line []byte, mapCwd func(string) (string, bool)) []byte {
	if !bytes.Contains(line, cwdKey) {
		return line
	}
	var out []byte
	done, i := 0, 0
	for {
		j := bytes.Index(line[i:], cwdKey)
		if j < 0 {
			break
		}
		start := i + j + len(cwdKey) - 1
		end := stringEnd(line, start)
		if end < 0 {
			break
		}
		i = end
		var v string
		if json.Unmarshal(line[start:end], &v) != nil {
			continue
		}
		prefix := ""
		if rest, ok := strings.CutPrefix(v, "file://"); ok {
			prefix, v = "file://", rest
		}
		to, ok := mapCwd(v)
		if !ok || to == v {
			continue
		}
		out = append(append(append(out, line[done:start]...), '"'), paths.JSON(prefix+to)...)
		out = append(out, '"')
		done = end
	}
	if out == nil {
		return line
	}
	return append(out, line[done:]...)
}

// stringEnd is the index just past the JSON string opening at line[start], -1 when it does not close.
func stringEnd(line []byte, start int) int {
	for k := start + 1; k < len(line); k++ {
		switch line[k] {
		case '\\':
			k++
		case '"':
			return k + 1
		}
	}
	return -1
}
