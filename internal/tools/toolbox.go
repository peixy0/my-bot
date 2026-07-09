package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"my-bot/internal/config"
	"my-bot/internal/runtime"
)

const maxHTTPBodySize = 1 << 20

type DefaultToolset struct {
	rt        runtime.Runtime
	skills    *SkillLoader
	cfg       *config.Config
	webSearch *WebSearch
	fetcher   *Fetcher
}

func NewDefaultToolset(rt runtime.Runtime, skills *SkillLoader, cfg *config.Config) *DefaultToolset {
	d := &DefaultToolset{rt: rt, skills: skills, cfg: cfg}
	if cfg.Tool.WebSearchAPI != "" {
		d.webSearch = NewWebSearch(cfg.Tool.WebSearchAPI)
	}
	d.fetcher = NewFetcher(d.rt, cfg.Tool.FetchProxy, cfg.Tool.MaxOutputChars)
	return d
}

func (d *DefaultToolset) Register(r *Registry) {
	if d.cfg.Tool.WebSearchAPI != "" {
		d.registerWebSearch(r)
	}
	d.registerFetch(r)
	d.registerReadFile(r)
	d.registerWriteFile(r)
	d.registerAppendFile(r)
	d.registerEditFile(r)

	d.registerGrep(r)
	d.registerGlob(r)
	if d.cfg.LLM.Vision {
		d.registerReadImage(r)
	}
	registerSkillTool(r, d.skills)
}

func (d *DefaultToolset) registerWebSearch(r *Registry) {
	r.Register(ToolSchema{
		Name:        "web_search",
		Description: "Search the web for up-to-date information.\n\nUse this tool when you need current information, facts, documentation, or\nanything not in your training data. Returns results with title, URL,\nand a snippet - use fetch() to retrieve the full content of a promising URL.",
		Parallel:    true,
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{
					"type":        "string",
					"description": "A concise search query, as you would type into a search engine. Avoid overly long queries; 3-8 words usually works best.",
				},
				"page": map[string]any{
					"type":        "integer",
					"default":     1,
					"description": "Page number (default: 1)",
				},
			},
			"required": []string{"query"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Query string `json:"query"`
			Page  int    `json:"page"`
		}
		p.Page = 1
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse web_search args: %w", err)
		}
		results, err := d.webSearch.Search(ctx, p.Query, p.Page)
		if err != nil {
			return ErrorResult(fmt.Errorf("web_search query %q page %d: %w", p.Query, p.Page, err)), nil
		}
		return TextResult(MarshalResult(results)), nil
	})
}

func (d *DefaultToolset) registerFetch(r *Registry) {
	r.Register(ToolSchema{
		Name:        "fetch",
		Description: "Fetch a web page and return its content as clean Markdown text.\n\nUse this when you have a specific URL and need its full content - e.g., to read\ndocumentation, an article, or a search result from web_search(). Returns the\nentire page body; for large pages, focus on the section relevant to your task.",
		Parallel:    true,
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The full URL to fetch, including scheme (e.g. https://example.com/page). Must be a valid HTTP/HTTPS URL.",
				},
			},
			"required": []string{"url"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			URL string `json:"url"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse fetch args: %w", err)
		}

		return d.fetcher.Fetch(ctx, p.URL)
	})
}

func (d *DefaultToolset) registerReadFile(r *Registry) {
	r.Register(ToolSchema{
		Name:        "read_file",
		Description: "Read a text file from the workspace, with optional line-range pagination.\n\nALWAYS use this instead of running cat/head/tail/sed via run_command(). Returns content with 1-based line numbers so you can reference exact locations when using edit_file().\n\nFor large files, paginate with start_line and limit. Do NOT use this for binary files.",
		Parallel:    true,
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "Path to the file, relative to the cwd or use absolute path.",
				},
				"start_line": map[string]any{
					"type":        "integer",
					"default":     1,
					"description": "1-based line number to start reading from (default: 1). Use this to paginate through large files by passing the next unread line number.",
				},
				"limit": map[string]any{
					"type":        "integer",
					"default":     500,
					"description": "Maximum number of lines to return per call (default: 500). Reduce for very wide files; increase only if you need more context in one shot.",
				},
			},
			"required": []string{"filename"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Filename  string `json:"filename"`
			StartLine int    `json:"start_line"`
			Limit     int    `json:"limit"`
		}
		p.StartLine = 1
		p.Limit = 500
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse read_file args: %w", err)
		}
		res, err := d.rt.ReadFile(ctx, p.Filename, p.StartLine, p.Limit)
		if err != nil {
			return ErrorResult(fmt.Errorf("read file %s: %w", p.Filename, err)), nil
		}
		return TextResult(formatReadFileResult(p.Filename, res)), nil
	})
}

func (d *DefaultToolset) registerWriteFile(r *Registry) {
	r.Register(ToolSchema{
		Name:        "write_file",
		Description: "Create or fully overwrite a file with the given content.\n\nUse this to create new files or when you need to replace the entire contents of a file. Parent directories are created automatically if they do not exist.\n\nFor targeted edits to an existing file, prefer edit_file(). ALWAYS call read_file() first before overwriting an existing file so you do not accidentally discard content you have not seen.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "Path to the file, relative to the cwd or use absolute path. Parent directories are created automatically.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The complete new content of the file. This replaces the entire file - include every line you want to keep.",
				},
			},
			"required": []string{"filename", "content"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Filename string `json:"filename"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse write_file args: %w", err)
		}
		if err := d.rt.WriteFile(ctx, p.Filename, p.Content); err != nil {
			return ErrorResult(fmt.Errorf("write file %s: %w", p.Filename, err)), nil
		}
		return TextResult(fmt.Sprintf("wrote %s", p.Filename)), nil
	})
}

func (d *DefaultToolset) registerAppendFile(r *Registry) {
	r.Register(ToolSchema{
		Name:        "append_file",
		Description: "Append given content to the end of a file.\n\nFile will be created if it doesn't exist.\n\nUse this to add new content while preserving the original content of a file.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "Path to the file, relative to the cwd or use absolute path. Parent directories are created automatically.",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The content to be appended.",
				},
			},
			"required": []string{"filename", "content"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Filename string `json:"filename"`
			Content  string `json:"content"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse append_file args: %w", err)
		}
		if err := d.rt.AppendFile(ctx, p.Filename, p.Content); err != nil {
			return ErrorResult(fmt.Errorf("append file %s: %w", p.Filename, err)), nil
		}
		return TextResult(fmt.Sprintf("append %s", p.Filename)), nil
	})
}

func (d *DefaultToolset) registerEditFile(r *Registry) {
	r.Register(ToolSchema{
		Name:        "edit_file",
		Description: "Surgically edit a file by applying one or more search-and-replace operations.\n\nALWAYS use this instead of sed/awk/perl -i via run_command(). Prefer this over write_file() for any modification to an existing file - it is safer and produces a minimal diff.\n\nYou MUST call read_file() before editing so search strings can be copied verbatim.",
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "Path to the file to edit, relative to the cwd or use absolute path.",
				},
				"edits": map[string]any{
					"type":        "array",
					"description": "An ordered list of search-and-replace operations. Applied sequentially - if a later block depends on an earlier change, write it accordingly.",
					"items": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"search": map[string]any{
								"type":        "string",
								"description": "The exact text to find, copied verbatim from read_file() output including all whitespace and indentation. Must be unique within the file; add more surrounding lines if needed.",
							},
							"replace": map[string]any{
								"type":        "string",
								"description": "The new text to substitute in place of the search block. Use an empty string to delete the matched block.",
							},
						},
						"required": []string{"search", "replace"},
					},
				},
			},
			"required": []string{"filename", "edits"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Filename string `json:"filename"`
			Edits    []struct {
				Search  string `json:"search"`
				Replace string `json:"replace"`
			} `json:"edits"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse edit_file args: %w", err)
		}
		edits := make([]runtime.Edit, len(p.Edits))
		for i, e := range p.Edits {
			edits[i] = runtime.Edit{Search: e.Search, Replace: e.Replace}
		}
		if err := d.rt.EditFile(ctx, p.Filename, edits); err != nil {
			return ErrorResult(fmt.Errorf("edit file %s: %w", p.Filename, err)), nil
		}
		return TextResult(fmt.Sprintf("edited %s", p.Filename)), nil
	})
}

func (d *DefaultToolset) registerGrep(r *Registry) {
	r.Register(ToolSchema{
		Name:        "grep",
		Description: "Search for a regex pattern across files and return matching lines with context.\n\nALWAYS use this instead of grep/rg via run_command(). For finding files by name/path pattern, use glob().",
		Parallel:    true,
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Extended regex (ERE) pattern to search for, e.g. 'def\\s+my_func' or 'TODO|FIXME'. Special regex characters must be escaped.",
				},
				"path": map[string]any{
					"type":        "string",
					"description": "Directory or file to search in, relative to workspace root (default: '.' searches everywhere). Narrow this to a subdirectory to speed up large repos.",
				},
				"surrounding_lines": map[string]any{
					"type":        "integer",
					"default":     2,
					"description": "Lines of context to include before and after each match (default: 2). Increase to 5-10 when you need to understand the full function signature or block.",
				},
				"include": map[string]any{
					"type":        "string",
					"description": "Restrict search to files matching this glob, e.g. '*.py' or '*.{ts,tsx}'. Omit to search all text files.",
				},
				"case_sensitive": map[string]any{
					"type":        "boolean",
					"default":     true,
					"description": "Whether the pattern is case-sensitive (default: true). Set false to match regardless of case.",
				},
			},
			"required": []string{"pattern"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Pattern          string `json:"pattern"`
			Path             string `json:"path"`
			SurroundingLines int    `json:"surrounding_lines"`
			Include          string `json:"include"`
			CaseSensitive    *bool  `json:"case_sensitive"`
		}
		p.SurroundingLines = 2
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse grep args: %w", err)
		}
		var sb strings.Builder
		sb.WriteString("rg -n")
		if p.CaseSensitive != nil && !*p.CaseSensitive {
			sb.WriteString(" -i")
		}
		if p.SurroundingLines > 0 {
			fmt.Fprintf(&sb, " -C %d", p.SurroundingLines)
		}
		if p.Include != "" {
			fmt.Fprintf(&sb, " --glob %q", p.Include)
		}
		fmt.Fprintf(&sb, " %q", p.Pattern)
		if p.Path != "" {
			fmt.Fprintf(&sb, " %q", p.Path)
		}
		res, err := d.rt.Execute(ctx, sb.String())
		if err != nil {
			return ErrorResult(fmt.Errorf("run grep command: %w", err)), nil
		}
		if res.ReturnCode == 1 {
			return TextResult("no matches"), nil
		}
		if res.ReturnCode != 0 {
			return ErrorResult(fmt.Errorf("%s", res.Stderr)), nil
		}
		return TextResult(res.Stdout), nil
	})
}

func (d *DefaultToolset) registerGlob(r *Registry) {
	r.Register(ToolSchema{
		Name:        "glob",
		Description: "List workspace files whose paths match a glob pattern.\n\nALWAYS use this instead of find/ls via run_command(). Supports ** for recursive matching.",
		Parallel:    true,
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"pattern": map[string]any{
					"type":        "string",
					"description": "Glob pattern relative to the workspace root, e.g. 'src/**/*.py' (all Python files under src/) or 'tests/test_*.py'. Use ** for recursive matching across subdirectories.",
				},
			},
			"required": []string{"pattern"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Pattern string `json:"pattern"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse glob args: %w", err)
		}
		result, err := d.rt.Glob(ctx, p.Pattern)
		if err != nil {
			return ErrorResult(fmt.Errorf("glob pattern %q: %w", p.Pattern, err)), nil
		}
		return TextResult(MarshalResult(result)), nil
	})
}

func (d *DefaultToolset) registerReadImage(r *Registry) {
	r.Register(ToolSchema{
		Name:        "read_image",
		Description: "Read an image file and return it as a vision content block.\n\nUse this to inspect screenshots, diagrams, or visual assets. Supported formats: PNG, JPEG, GIF, WebP.",
		Parallel:    true,
		ParameterDesc: (map[string]any{
			"type": "object",
			"properties": map[string]any{
				"filename": map[string]any{
					"type":        "string",
					"description": "Path to the image file, relative to the workspace root. Supported extensions: .png, .jpg, .jpeg, .gif, .webp.",
				},
			},
			"required": []string{"filename"},
		}),
	}, func(ctx context.Context, args []byte) (ToolResult, error) {
		var p struct {
			Filename string `json:"filename"`
		}
		if err := json.Unmarshal(args, &p); err != nil {
			return ToolResult{}, fmt.Errorf("parse read_image args: %w", err)
		}
		data, err := d.rt.ReadRawBytes(ctx, p.Filename)
		if err != nil {
			return ErrorResult(fmt.Errorf("read image %s: %w", p.Filename, err)), nil
		}
		if len(data) > d.cfg.Context.MaxImageBytes {
			return ErrorResult(fmt.Errorf("image too large: %d bytes", len(data))), nil
		}
		mimeType := detectImageMIME(data)
		b64 := base64.StdEncoding.EncodeToString(data)
		return ImageResult([]map[string]any{
			{
				"type": "image_url",
				"image_url": map[string]any{
					"url":    fmt.Sprintf("data:%s;base64,%s", mimeType, b64),
					"detail": "auto",
				},
			},
		}), nil
	})
}

func detectImageMIME(data []byte) string {
	if len(data) < 4 {
		return "image/jpeg"
	}
	switch {
	case data[0] == 0x89 && data[1] == 'P' && data[2] == 'N' && data[3] == 'G':
		return "image/png"
	case data[0] == 'G' && data[1] == 'I' && data[2] == 'F':
		return "image/gif"
	case data[0] == 'R' && data[1] == 'I' && data[2] == 'F' && data[3] == 'F':
		return "image/webp"
	default:
		return "image/jpeg"
	}
}
