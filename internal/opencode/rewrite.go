package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// applyEdit dispatches a recorded editOp onto the raw config bytes, returning
// the spliced bytes. Only the region described by the edit is replaced; every
// other byte is preserved exactly.
func applyEdit(data []byte, e editOp) ([]byte, error) {
	switch e.kind {
	case "models":
		return applyModelsEdit(data, e.key, e.models, e.modelOrder)
	case "provider":
		return applyProviderEdit(data, e.key, e.entry)
	default:
		return nil, fmt.Errorf("unknown opencode edit kind %q", e.kind)
	}
}

// jnode is a JSON value with its byte range and depth in the document.
type jnode struct {
	kind     byte // '{', '[' or 's' (scalar)
	start    int
	end      int
	depth    int
	children []jchild
}

type jchild struct {
	key          string
	valStart     int
	valEnd       int
	valNode      *jnode
}

// parseNode parses one JSON value starting at i. Comments are treated as
// whitespace so JSONC files work too.
func parseNode(data []byte, i, depth int) (*jnode, int, error) {
	i = skipJSONWS(data, i)
	if i >= len(data) {
		return nil, 0, errors.New("unexpected end of config")
	}
	switch data[i] {
	case '{':
		node := &jnode{kind: '{', start: i, depth: depth}
		i++
		i = skipJSONWS(data, i)
		if i < len(data) && data[i] == '}' {
			node.end = i + 1
			return node, i + 1, nil
		}
		for i < len(data) {
			i = skipJSONWS(data, i)
			if data[i] != '"' {
				return nil, 0, errors.New("expected object key")
			}
			_, key, ni, err := parseJSONString(data, i)
			if err != nil {
				return nil, 0, err
			}
			i = skipJSONWS(data, ni)
			if i >= len(data) || data[i] != ':' {
				return nil, 0, errors.New("expected ':'")
			}
			i++
			child, ni2, err := parseNode(data, i, depth+1)
			if err != nil {
				return nil, 0, err
			}
			node.children = append(node.children, jchild{key: key, valStart: child.start, valEnd: child.end, valNode: child})
			i = skipJSONWS(data, ni2)
			if i < len(data) && data[i] == ',' {
				i++
				continue
			}
			if i < len(data) && data[i] == '}' {
				node.end = i + 1
				return node, i + 1, nil
			}
			return nil, 0, errors.New("expected ',' or '}'")
		}
	case '[':
		node := &jnode{kind: '[', start: i, depth: depth}
		i++
		i = skipJSONWS(data, i)
		if i < len(data) && data[i] == ']' {
			node.end = i + 1
			return node, i + 1, nil
		}
		for i < len(data) {
			child, ni, err := parseNode(data, i, depth+1)
			if err != nil {
				return nil, 0, err
			}
			node.children = append(node.children, jchild{valStart: child.start, valEnd: child.end, valNode: child})
			i = skipJSONWS(data, ni)
			if i < len(data) && data[i] == ',' {
				i++
				continue
			}
			if i < len(data) && data[i] == ']' {
				node.end = i + 1
				return node, i + 1, nil
			}
			return nil, 0, errors.New("expected ',' or ']'")
		}
	default:
		if data[i] == '"' {
			_, _, ni, err := parseJSONString(data, i)
			if err != nil {
				return nil, 0, err
			}
			return &jnode{kind: 's', start: i, end: ni, depth: depth}, ni, nil
		}
		start := i
		for i < len(data) {
			c := data[i]
			if c == ',' || c == '}' || c == ']' || c == ' ' || c == '\t' || c == '\r' || c == '\n' {
				break
			}
			if c == '/' && i+1 < len(data) && (data[i+1] == '/' || data[i+1] == '*') {
				break
			}
			i++
		}
		return &jnode{kind: 's', start: start, end: i, depth: depth}, i, nil
	}
	return nil, 0, errors.New("unexpected end of config")
}

// skipJSONWS advances past whitespace and JSONC comments.
func skipJSONWS(data []byte, i int) int {
	for i < len(data) {
		c := data[i]
		switch {
		case c == ' ' || c == '\t' || c == '\r' || c == '\n':
			i++
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			i += 2
			for i < len(data) && data[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			i += 2
			for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
				i++
			}
			if i+1 < len(data) {
				i += 2
			}
		default:
			return i
		}
	}
	return i
}

// parseJSONString reads a quoted string at i (data[i] == '"') and returns its
// raw bytes, decoded value and the index just past the closing quote.
func parseJSONString(data []byte, i int) (raw []byte, val string, next int, err error) {
	start := i
	i++
	for i < len(data) {
		c := data[i]
		if c == '\\' {
			i += 2
			continue
		}
		if c == '"' {
			i++
			break
		}
		i++
	}
	if i > len(data) || data[i-1] != '"' {
		return nil, "", i, errors.New("unterminated string")
	}
	raw = data[start:i]
	if err := json.Unmarshal(raw, &val); err != nil {
		return nil, "", i, fmt.Errorf("invalid string: %w", err)
	}
	return raw, val, i, nil
}

func findChild(node *jnode, key string) *jchild {
	for i := range node.children {
		if node.children[i].key == key {
			return &node.children[i]
		}
	}
	return nil
}

// splice replaces data[start:end] with repl.
func splice(data []byte, start, end int, repl []byte) []byte {
	out := make([]byte, 0, len(data)-(end-start)+len(repl))
	out = append(out, data[:start]...)
	out = append(out, repl...)
	out = append(out, data[end:]...)
	return out
}

// applyModelsEdit replaces (or inserts) the models value of provider key.
func applyModelsEdit(data []byte, providerKey string, models map[string]any, order ...[]string) ([]byte, error) {
	root, _, err := parseNode(data, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("opencode config: %w", err)
	}
	provCh := findChild(root, "provider")
	if provCh == nil {
		return nil, errors.New("provider section missing in opencode config")
	}
	entry := findChild(provCh.valNode, providerKey)
	if entry == nil {
		return nil, fmt.Errorf("provider %q missing in opencode config", providerKey)
	}
	unit := detectIndentUnit(data)
	if mc := findChild(entry.valNode, "models"); mc != nil {
		repl := []byte(formatModels(models, mc.valNode.depth, unit, order...))
		return splice(data, mc.valStart, mc.valEnd, repl), nil
	}
	val := formatModels(models, entry.valNode.depth+1, unit, order...)
	return insertMember(data, entry.valNode, "models", []byte(val)), nil
}

// applyProviderEdit inserts a provider entry (used when creating the local
// Ollama provider). It is a no-op when the key already exists.
func applyProviderEdit(data []byte, key string, entry map[string]any) ([]byte, error) {
	root, _, err := parseNode(data, 0, 0)
	if err != nil {
		return nil, fmt.Errorf("opencode config: %w", err)
	}
	unit := detectIndentUnit(data)
	if provCh := findChild(root, "provider"); provCh != nil {
		if findChild(provCh.valNode, key) != nil {
			return data, nil
		}
		val := formatValue(entry, provCh.valNode.depth+1, unit)
		return insertMember(data, provCh.valNode, key, []byte(val)), nil
	}
	val := formatValue(map[string]any{key: entry}, 1, unit)
	return insertMember(data, root, "provider", []byte(val)), nil
}

// insertMember inserts "key: value" into the object node, right after the
// last member or, for an empty object, right after the opening brace.
func insertMember(data []byte, obj *jnode, key string, value []byte) []byte {
	unit := detectIndentUnit(data)
	d := obj.depth
	member := append([]byte(jsonEncodeString(key)+": "), value...)
	if len(obj.children) > 0 {
		last := &obj.children[len(obj.children)-1]
		sep := append([]byte(",\n"), []byte(strings.Repeat(unit, d+1))...)
		ins := append(sep, member...)
		return splice(data, last.valEnd, last.valEnd, ins)
	}
	ins := append([]byte("\n"), []byte(strings.Repeat(unit, d+1))...)
	ins = append(ins, member...)
	ins = append(ins, []byte("\n"+strings.Repeat(unit, d))...)
	return splice(data, obj.start+1, obj.start+1, ins)
}

// formatModels renders a models map as a tab-aligned block with one model per
// line and inline per-model objects (matching the common opencode style).
// When order is provided, models are emitted in that sequence.
func formatModels(models map[string]any, depth int, unit string, order ...[]string) string {
	if len(models) == 0 {
		return "{}"
	}
	var b strings.Builder
	b.WriteString("{\n")

	var keys []string
	if len(order) > 0 && len(order[0]) > 0 {
		seen := make(map[string]bool, len(order[0]))
		for _, k := range order[0] {
			if _, ok := models[k]; ok && !seen[k] {
				seen[k] = true
				keys = append(keys, k)
			}
		}
		remaining := sortedMapKeys(models)
		for _, k := range remaining {
			if !seen[k] {
				keys = append(keys, k)
			}
		}
	} else {
		keys = sortedMapKeys(models)
	}

	for i, k := range keys {
		b.WriteString(strings.Repeat(unit, depth+1))
		b.WriteString(jsonEncodeString(k))
		b.WriteString(": ")
		b.WriteString(inlineJSON(models[k]))
		if i < len(keys)-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(strings.Repeat(unit, depth))
	b.WriteString("}")
	return b.String()
}

// formatValue renders an arbitrary value as JSON, opening brackets inline and
// indenting members one level deeper than the value.
func formatValue(v any, depth int, unit string) string {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		var b strings.Builder
		b.WriteString("{\n")
		keys := sortedMapKeys(t)
		for i, k := range keys {
			b.WriteString(strings.Repeat(unit, depth+1))
			b.WriteString(jsonEncodeString(k))
			b.WriteString(": ")
			b.WriteString(formatValue(t[k], depth+1, unit))
			if i < len(keys)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat(unit, depth))
		b.WriteString("}")
		return b.String()
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteString("[\n")
		for i, el := range t {
			b.WriteString(strings.Repeat(unit, depth+1))
			b.WriteString(formatValue(el, depth+1, unit))
			if i < len(t)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		b.WriteString(strings.Repeat(unit, depth))
		b.WriteString("]")
		return b.String()
	default:
		raw, _ := json.Marshal(v)
		return string(raw)
	}
}

func jsonEncodeString(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// inlineJSON renders a value as compact single-line JSON with ": " separators
// (e.g. {"name": "tag"}), matching the common opencode config style.
func inlineJSON(v any) string {
	switch t := v.(type) {
	case map[string]any:
		if len(t) == 0 {
			return "{}"
		}
		var b strings.Builder
		b.WriteByte('{')
		keys := sortedMapKeys(t)
		for i, k := range keys {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(jsonEncodeString(k))
			b.WriteString(": ")
			b.WriteString(inlineJSON(t[k]))
		}
		b.WriteByte('}')
		return b.String()
	case []any:
		if len(t) == 0 {
			return "[]"
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, el := range t {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(inlineJSON(el))
		}
		b.WriteByte(']')
		return b.String()
	default:
		raw, _ := json.Marshal(v)
		return string(raw)
	}
}

func sortedMapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// detectIndentUnit decides whether the file is indented with tabs or spaces.
func detectIndentUnit(data []byte) string {
	hasTab, hasSpace := false, false
	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		i := 0
		for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
			i++
		}
		if i == 0 || i >= len(line) {
			continue
		}
		if strings.ContainsRune(line[:i], '\t') {
			hasTab = true
		} else {
			hasSpace = true
		}
	}
	if hasTab {
		return "\t"
	}
	if hasSpace {
		return " "
	}
	return "\t"
}
