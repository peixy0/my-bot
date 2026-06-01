package tools

import (
	"strings"
)

func ParseFrontmatter(content string) (map[string]string, string) {
	fm := make(map[string]string)
	if !strings.HasPrefix(content, "---") {
		return fm, content
	}
	rest := strings.TrimPrefix(content, "---")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return fm, content
	}
	header := rest[:idx]
	body := strings.TrimPrefix(rest[idx+4:], "\n")

	lines := strings.Split(header, "\n")
	var currentKey string
	var multiLines []string
	multiMode := ""

	flush := func() {
		if currentKey == "" {
			return
		}
		switch multiMode {
		case "|":
			fm[currentKey] = strings.Join(multiLines, "\n")
		case ">":
			fm[currentKey] = strings.Join(multiLines, " ")
		}
		currentKey = ""
		multiLines = nil
		multiMode = ""
	}

	for _, line := range lines {
		if currentKey != "" {
			if strings.HasPrefix(line, "  ") || strings.HasPrefix(line, "\t") {
				multiLines = append(multiLines, strings.TrimSpace(line))
				continue
			}
			flush()
		}
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		if val == "|" || val == ">" {
			currentKey = key
			multiMode = val
		} else {
			fm[key] = val
		}
	}
	flush()
	return fm, body
}
