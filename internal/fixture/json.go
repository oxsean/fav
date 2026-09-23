package fixture

import (
	"bytes"
	"encoding/json"
)

type kv struct {
	k string
	v any
}

// obj keeps key order: transcripts are written the way the CLIs write them, compact and in their field order.
type obj []kv

func (o obj) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, p := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(p.k)
		b.Write(k)
		b.WriteByte(':')
		v, err := encode(p.v)
		if err != nil {
			return nil, err
		}
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (o obj) with(k string, v any) obj { return append(o, kv{k, v}) }

func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	if err := e.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(b.Bytes(), "\n"), nil
}
