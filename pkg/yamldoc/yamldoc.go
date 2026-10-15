// Package yamldoc is a reflection-based commented-YAML encoder for
// generated files: typed Go values in, deterministic YAML with comment
// blocks out.
package yamldoc

import (
	"bytes"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The generated values files are typed Go values marshaled by yaml.v3,
// never hand-written YAML. A file is a Doc: its top-level sections in
// file order, each a struct (or map) whose keys it contributes, behind the
// comment block a reader of the file sees above them.
//
// Struct fields take the `yaml` tag yaml.v3 knows (name, "-", omitempty,
// flow) and a `style` tag of this encoder's own:
//
//   - quoted: string scalars are double-quoted (the field's own, or a
//     slice's or map's elements);
//   - quotekeys: a map's keys are double-quoted;
//   - literal: a string is a literal block scalar (`|`).
//
// Maps are emitted in sorted key order. A struct may name a comment above
// any of its keys (KeyCommenter), a sequence item one above itself
// (ItemCommenter). A value of type Passthrough, an `any`, or a
// map[string]any is handed to yaml.v3 unchanged: it is someone else's
// document (a chart preset, a product's own values).
type (
	// Doc is a document under construction: Add sections, then Bytes.
	Doc struct {
		parts []docPart
		err   error
	}

	docPart struct {
		blank  bool
		node   *yaml.Node
		indent int
		raw    []byte
	}

	// KeyCommenter names a comment block ("# ..." lines) above some of a
	// struct's keys, by YAML key.
	KeyCommenter interface {
		YAMLKeyComments() map[string]string
	}

	// ItemCommenter names a comment block above a sequence item.
	ItemCommenter interface {
		YAMLItemComment() string
	}

	// Noder builds its own node.
	Noder interface {
		YAMLNode() (*yaml.Node, error)
	}

	// Passthrough is marshaled by yaml.v3 exactly as yaml.Marshal would.
	Passthrough struct{ Value any }

	// keyedValue is a one-key mapping.
	keyedValue struct {
		key   string
		value any
	}

	// yamlTextValue is YAML a library wrote as text.
	yamlTextValue string

	fieldOptions struct {
		omitEmpty, flow, quoted, quoteKeys, literal bool
	}
)

// Add appends v's keys behind comment: "# ..." lines, a leading newline
// for a blank line above them, empty for none. A v that renders no key
// renders nothing, its comment included.
func (d *Doc) Add(comment string, v any) {
	d.AddIndented(2, comment, v)
}

// AddIndented is Add at another indentation (yaml.Marshal's 4, which
// some sections have always had).
func (d *Doc) AddIndented(indent int, comment string, v any) {
	if d.err != nil {
		return
	}

	node, err := encodeNode(reflect.ValueOf(v), fieldOptions{})
	if err != nil {
		d.err = err

		return
	}

	if node.Kind != yaml.MappingNode {
		d.err = fmt.Errorf("yamldoc: a section must be a mapping, not %T", v)

		return
	}

	if len(node.Content) == 0 {
		return
	}

	blank := strings.HasPrefix(comment, "\n")
	if c := strings.TrimPrefix(comment, "\n"); c != "" {
		node.Content[0].HeadComment = joinComments(c, node.Content[0].HeadComment)
	}

	d.parts = append(d.parts, docPart{blank: blank, node: node, indent: indent})
}

// Raw appends YAML another package rendered (a library's own marshaller).
func (d *Doc) Raw(b []byte) {
	d.parts = append(d.parts, docPart{raw: b})
}

// Bytes renders the document.
func (d *Doc) Bytes() ([]byte, error) {
	if d.err != nil {
		return nil, d.err
	}

	var buf bytes.Buffer

	for _, part := range d.parts {
		if part.blank {
			buf.WriteString("\n")
		}

		if part.raw != nil {
			buf.Write(part.raw)

			continue
		}

		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(part.indent)

		if err := enc.Encode(part.node); err != nil {
			return nil, err
		}

		if err := enc.Close(); err != nil {
			return nil, err
		}
	}

	return buf.Bytes(), nil
}

// Keyed is a one-key mapping, for a section that is a single key.
func Keyed(key string, value any) Noder {
	return keyedValue{key: key, value: value}
}

// YAMLNode builds the one-key mapping.
func (k keyedValue) YAMLNode() (*yaml.Node, error) {
	value, err := encodeNode(reflect.ValueOf(k.value), fieldOptions{})
	if err != nil {
		return nil, err
	}

	return &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{plainScalar(k.key), value}}, nil
}

// Text parses YAML a library wrote as text, so it can sit inside a
// typed value.
func Text(write func(sb *strings.Builder)) Noder {
	var sb strings.Builder

	write(&sb)

	return yamlTextValue(sb.String())
}

// YAMLNode parses the text.
func (t yamlTextValue) YAMLNode() (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(t), &doc); err != nil {
		return nil, fmt.Errorf("yamldoc: parse library YAML: %w", err)
	}

	if len(doc.Content) != 1 {
		return &yaml.Node{Kind: yaml.MappingNode}, nil
	}

	return doc.Content[0], nil
}

// YAMLNode encodes the wrapped value the way yaml.Marshal would.
func (p Passthrough) YAMLNode() (*yaml.Node, error) {
	var n yaml.Node
	if err := n.Encode(p.Value); err != nil {
		return nil, err
	}

	return &n, nil
}

const (
	nullTag = "!!null"
)

func nullNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: nullTag, Value: "null"}
}

func joinComments(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "\n" + b
	}
}

func plainScalar(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Value: value}
}

func stringScalar(value string, o fieldOptions) *yaml.Node {
	n := plainScalar(value)

	switch {
	case o.literal:
		n.Style = yaml.LiteralStyle
	case o.quoted:
		n.Style = yaml.DoubleQuotedStyle
	}

	return n
}

var (
	noderType   = reflect.TypeFor[Noder]()
	marshalType = reflect.TypeFor[yaml.Marshaler]()
)

// encodeNode is the typed value → node walk.
func encodeNode(v reflect.Value, o fieldOptions) (*yaml.Node, error) {
	if !v.IsValid() {
		return nullNode(), nil
	}

	if v.Type().Implements(noderType) {
		if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
			return nullNode(), nil
		}

		return v.Interface().(Noder).YAMLNode()
	}

	if v.Type().Implements(marshalType) {
		return Passthrough{Value: v.Interface()}.YAMLNode()
	}

	switch v.Kind() {
	case reflect.Interface, reflect.Pointer:
		if v.IsNil() {
			return nullNode(), nil
		}

		return encodeNode(v.Elem(), o)
	case reflect.Struct:
		return encodeStruct(v)
	case reflect.Map:
		if v.Type().Elem().Kind() == reflect.Interface {
			return Passthrough{Value: v.Interface()}.YAMLNode()
		}

		return encodeMap(v, o)
	case reflect.Slice, reflect.Array:
		n := &yaml.Node{Kind: yaml.SequenceNode}
		if o.flow {
			n.Style = yaml.FlowStyle
		}

		for i := range v.Len() {
			item, err := encodeNode(v.Index(i), o)
			if err != nil {
				return nil, err
			}

			if c, ok := v.Index(i).Interface().(ItemCommenter); ok {
				item.HeadComment = c.YAMLItemComment()
			}

			n.Content = append(n.Content, item)
		}

		return n, nil
	case reflect.String:
		return stringScalar(v.String(), o), nil
	case reflect.Bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(v.Bool())}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(v.Int(), 10)}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatUint(v.Uint(), 10)}, nil
	default:
		return nil, fmt.Errorf("yamldoc: cannot encode a %s", v.Type())
	}
}

func encodeMap(v reflect.Value, o fieldOptions) (*yaml.Node, error) {
	// A nil map is YAML's null, written as nothing after the key; an empty
	// one is {}.
	if v.IsNil() {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: nullTag}, nil
	}

	if v.Type().Key().Kind() != reflect.String {
		return nil, fmt.Errorf("yamldoc: map keys must be strings, not %s", v.Type().Key())
	}

	keys := v.MapKeys()
	slices.SortFunc(keys, func(a, b reflect.Value) int { return strings.Compare(a.String(), b.String()) })

	n := &yaml.Node{Kind: yaml.MappingNode}

	for _, key := range keys {
		value, err := encodeNode(v.MapIndex(key), o)
		if err != nil {
			return nil, err
		}

		n.Content = append(n.Content, stringScalar(key.String(), fieldOptions{quoted: o.quoteKeys}), value)
	}

	return n, nil
}

func encodeStruct(v reflect.Value) (*yaml.Node, error) {
	var comments map[string]string
	if c, ok := v.Interface().(KeyCommenter); ok {
		comments = c.YAMLKeyComments()
	}

	n := &yaml.Node{Kind: yaml.MappingNode}
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		name, o := parseYAMLTag(field)
		if name == "-" {
			continue
		}

		value := v.Field(i)
		if o.omitEmpty && isEmptyValue(value) {
			continue
		}

		node, err := encodeNode(value, o)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		key := plainScalar(name)
		key.HeadComment = comments[name]
		n.Content = append(n.Content, key, node)
	}

	return n, nil
}

func parseYAMLTag(field reflect.StructField) (string, fieldOptions) {
	var o fieldOptions

	name, opts, _ := strings.Cut(field.Tag.Get("yaml"), ",")
	if name == "" {
		name = strings.ToLower(field.Name)
	}

	for opt := range strings.SplitSeq(opts+","+field.Tag.Get("style"), ",") {
		switch opt {
		case "omitempty":
			o.omitEmpty = true
		case "flow":
			o.flow = true
		case "quoted":
			o.quoted = true
		case "quotekeys":
			o.quoteKeys = true
		case "literal":
			o.literal = true
		}
	}

	return name, o
}

func isEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	default:
		return v.IsZero()
	}
}

// Fail records err; every later Add stops and Bytes returns it. A section
// builder that fails outside Add uses it to keep the chain of calls flat.
func (d *Doc) Fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// Encode is the typed value → node walk for a value with default options.
// A type that implements Noder or yaml.Marshaler builds its own node.
func Encode(v any) (*yaml.Node, error) {
	return encodeNode(reflect.ValueOf(v), fieldOptions{})
}

// PlainScalar is an unquoted scalar node.
func PlainScalar(value string) *yaml.Node { return plainScalar(value) }

// QuotedScalar is a double-quoted string scalar node.
func QuotedScalar(value string) *yaml.Node {
	return stringScalar(value, fieldOptions{quoted: true})
}
