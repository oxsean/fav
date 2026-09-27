package task

import (
	"strings"
	"testing"
)

func TestAPlanIsReadAndChecked(t *testing.T) {
	good := `{"tasks":[{"key":"model","title":"Model","brief":"add the model","size":"S"},
		{"key":"api","title":"API","brief":"serve it","after":["model"],"acceptance":["GET /x works"],"workflow":"feature"},
		{"key":"api-tests","title":"API tests","brief":"test it","parent":"api"}],
		"questions":["CSV or TSV?"]}`
	p, err := ParsePlan([]byte(good))
	if err != nil || len(p.Tasks) != 3 || p.Tasks[1].After[0] != "model" || p.Questions[0] != "CSV or TSV?" || p.Tasks[2].Parent != "api" {
		t.Fatalf("%+v %v", p, err)
	}
	for name, bad := range map[string]string{
		"not json":         `tasks:`,
		"no tasks":         `{"tasks":[]}`,
		"no title":         `{"tasks":[{"key":"a","title":" "}]}`,
		"bad key":          `{"tasks":[{"key":"A B","title":"x"}]}`,
		"twice":            `{"tasks":[{"key":"a","title":"x"},{"key":"a","title":"y"}]}`,
		"unknown after":    `{"tasks":[{"key":"a","title":"x","after":["b"]}]}`,
		"after itself":     `{"tasks":[{"key":"a","title":"x","after":["a"]}]}`,
		"a cycle":          `{"tasks":[{"key":"a","title":"x","after":["b"]},{"key":"b","title":"y","after":["a"]}]}`,
		"unknown parent":   `{"tasks":[{"key":"a","title":"x","parent":"z"}]}`,
		"three levels":     `{"tasks":[{"key":"a","title":"x"},{"key":"b","title":"y","parent":"a"},{"key":"c","title":"z","parent":"b"}]}`,
		"after its parent": `{"tasks":[{"key":"a","title":"x"},{"key":"b","title":"y","parent":"a","after":["a"]}]}`,
		"a bad size":       `{"tasks":[{"key":"a","title":"x","size":"XXL"}]}`,
		"an unknown field": `{"tasks":[{"key":"a","title":"x","owner":"bob"}]}`,
		"too long a title": `{"tasks":[{"key":"a","title":"` + strings.Repeat("x", 1025) + `"}]}`,
	} {
		if _, err := ParsePlan([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	many := `{"tasks":[` + strings.TrimSuffix(strings.Repeat(`{"key":"k","title":"x"},`, 51), ",") + `]}`
	if _, err := ParsePlan([]byte(many)); err == nil || !strings.Contains(err.Error(), "50") {
		t.Errorf("at most 50 tasks: %v", err)
	}
}

func TestAPlansTasksComeParentsFirst(t *testing.T) {
	p, err := ParsePlan([]byte(`{"tasks":[{"key":"b","title":"b","parent":"a"},{"key":"a","title":"a"},{"key":"c","title":"c","after":["b"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, x := range p.Ordered() {
		keys = append(keys, x.Key)
	}
	if strings.Join(keys, " ") != "a b c" {
		t.Fatalf("%v", keys)
	}
	if p.Depth() != 2 {
		t.Fatalf("depth %d", p.Depth())
	}
}
