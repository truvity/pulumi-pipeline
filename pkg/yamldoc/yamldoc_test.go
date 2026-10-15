package yamldoc_test

import (
	"errors"
	"testing"

	"github.com/truvity/pulumi-pipeline/pkg/yamldoc"
)

type item struct {
	Name string `yaml:"name"`
}

func (item) YAMLItemComment() string { return "# an item" }

type section struct {
	Title  string            `yaml:"title" style:"quoted"`
	Skip   string            `yaml:"skip,omitempty"`
	Labels map[string]string `yaml:"labels,omitempty"`
	Items  []item            `yaml:"items,omitempty"`
	Text   string            `yaml:"text,omitempty" style:"literal"`
}

func (section) YAMLKeyComments() map[string]string {
	return map[string]string{"labels": "# the labels"}
}

func TestDoc(t *testing.T) {
	var d yamldoc.Doc

	d.Add("# head", section{
		Title:  "t",
		Labels: map[string]string{"b": "2", "a": "1"},
		Items:  []item{{Name: "x"}},
		Text:   "l1\nl2\n",
	})
	d.Add("\n# passthrough", yamldoc.Passthrough{Value: map[string]any{"k": 1}})
	d.Add("", yamldoc.Keyed("one", "v"))
	d.Raw([]byte("raw: true\n"))

	got, err := d.Bytes()
	if err != nil {
		t.Fatal(err)
	}

	want := `# head
title: "t"
# the labels
labels:
  a: 1
  b: 2
items:
  # an item
  - name: x
text: |
  l1
  l2

# passthrough
k: 1
one: v
raw: true
`
	if string(got) != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestDocErrorsAreSticky(t *testing.T) {
	var d yamldoc.Doc

	d.Add("", 3) // not a mapping
	d.Fail(errors.New("later"))

	if _, err := d.Bytes(); err == nil {
		t.Fatal("want error")
	}
}
