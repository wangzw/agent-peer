// Command site-parity checks that the English and Chinese trees of the website
// describe the same pages.
//
// The two versions of a page are translations of each other: the page sets
// match, every page has a title and a weight, and every pair has the same
// heading level sequence, the same code blocks, the same weight and the same
// set of internal links. The comparison ignores the text itself, because a
// translation is not a copy. Mermaid blocks are an exception: the two versions
// must place them the same way but may label the diagrams in their own
// language.
//
// A relref or ref shortcode renders a URL. It must therefore be used as a link
// target or as a parameter of another shortcode; written on its own it shows
// the path as text. An unclosed fence is reported instead of being folded into
// the comparison, and both content trees must contain at least one page.
package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	enDir := flag.String("en", "docs/site/content/en", "English content root")
	zhDir := flag.String("zh", "docs/site/content/zh", "Chinese content root")
	flag.Parse()

	problems, err := check(*enDir, *zhDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "site-parity: %v\n", err)
		os.Exit(2)
	}
	for _, problem := range problems {
		fmt.Fprintln(os.Stderr, "site-parity: "+problem)
	}
	if len(problems) > 0 {
		os.Exit(1)
	}
}

// check compares the two content trees and returns one message per difference.
func check(enDir, zhDir string) ([]string, error) {
	enFiles, err := listFiles(enDir)
	if err != nil {
		return nil, err
	}
	zhFiles, err := listFiles(zhDir)
	if err != nil {
		return nil, err
	}

	var problems []string
	if len(enFiles) == 0 {
		problems = append(problems, fmt.Sprintf("%s: no pages found", enDir))
	}
	if len(zhFiles) == 0 {
		problems = append(problems, fmt.Sprintf("%s: no pages found", zhDir))
	}

	paths := make([]string, 0, len(enFiles)+len(zhFiles))
	seen := make(map[string]bool, len(enFiles)+len(zhFiles))
	for path := range enFiles {
		seen[path] = true
		paths = append(paths, path)
	}
	for path := range zhFiles {
		if !seen[path] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)

	for _, path := range paths {
		enPath, inEn := enFiles[path]
		zhPath, inZh := zhFiles[path]
		switch {
		case !inZh:
			problems = append(problems, fmt.Sprintf("%s: present only in English", path))
		case !inEn:
			problems = append(problems, fmt.Sprintf("%s: present only in Chinese", path))
		default:
			fileProblems, err := compareFile(path, enPath, zhPath)
			if err != nil {
				return nil, err
			}
			problems = append(problems, fileProblems...)
		}
	}
	return problems, nil
}

// listFiles maps each regular file below root to its path on disk. Paths use
// forward slashes and are relative to root, so the two trees can be compared
// key by key.
func listFiles(root string) (map[string]string, error) {
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = path
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("list %s: %w", root, err)
	}
	return files, nil
}

// compareFile reports how one pair of pages differs.
func compareFile(rel, enPath, zhPath string) ([]string, error) {
	en, err := parsePage(enPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	zh, err := parsePage(zhPath)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}

	var problems []string
	problems = append(problems, requiredFrontMatter(rel, "English", en)...)
	problems = append(problems, requiredFrontMatter(rel, "Chinese", zh)...)
	if en.weight != "" && zh.weight != "" && en.weight != zh.weight {
		problems = append(problems, fmt.Sprintf(
			"%s: weight differs: English %q, Chinese %q", rel, en.weight, zh.weight))
	}
	if !equalInts(en.headings, zh.headings) {
		problems = append(problems, fmt.Sprintf(
			"%s: heading levels differ: English %v, Chinese %v", rel, en.headings, zh.headings))
	}
	problems = append(problems, compareCodeBlocks(rel, en.codeBlocks, zh.codeBlocks)...)
	problems = append(problems, compareLinks(rel, en.links, zh.links)...)
	for _, violation := range en.violations {
		problems = append(problems, fmt.Sprintf("%s: English: %s", rel, violation))
	}
	for _, violation := range zh.violations {
		problems = append(problems, fmt.Sprintf("%s: Chinese: %s", rel, violation))
	}
	return problems, nil
}

// requiredFrontMatter reports a page without the front matter the content
// conventions require.
func requiredFrontMatter(rel, language string, page parsedPage) []string {
	if !page.hasFrontMatter {
		return []string{fmt.Sprintf("%s: %s page has no YAML front matter", rel, language)}
	}
	var problems []string
	if page.title == "" {
		problems = append(problems, fmt.Sprintf("%s: %s page has no title", rel, language))
	}
	if page.weight == "" {
		problems = append(problems, fmt.Sprintf("%s: %s page has no weight", rel, language))
	}
	return problems
}

func compareCodeBlocks(rel string, en, zh []codeBlock) []string {
	if len(en) != len(zh) {
		return []string{fmt.Sprintf(
			"%s: code block count differs: English %d, Chinese %d", rel, len(en), len(zh))}
	}
	var problems []string
	for i := range en {
		if !strings.EqualFold(en[i].info, zh[i].info) {
			problems = append(problems, fmt.Sprintf(
				"%s: code block %d differs: English info %q, Chinese info %q",
				rel, i+1, en[i].info, zh[i].info))
			continue
		}
		// Mermaid diagrams are translated: only their position must match.
		// The exemption is the exact info word, so a block labelled differently
		// is still compared.
		if en[i].info == "mermaid" {
			continue
		}
		if en[i].text != zh[i].text {
			problems = append(problems, fmt.Sprintf(
				"%s: code block %d differs: English %q, Chinese %q",
				rel, i+1, en[i].text, zh[i].text))
		}
	}
	return problems
}

func compareLinks(rel string, en, zh []string) []string {
	enSet := make(map[string]bool, len(en))
	for _, link := range en {
		enSet[link] = true
	}
	zhSet := make(map[string]bool, len(zh))
	for _, link := range zh {
		zhSet[link] = true
	}

	var onlyEn, onlyZh []string
	for link := range enSet {
		if !zhSet[link] {
			onlyEn = append(onlyEn, link)
		}
	}
	for link := range zhSet {
		if !enSet[link] {
			onlyZh = append(onlyZh, link)
		}
	}
	if len(onlyEn) == 0 && len(onlyZh) == 0 {
		return nil
	}
	sort.Strings(onlyEn)
	sort.Strings(onlyZh)
	return []string{fmt.Sprintf(
		"%s: internal link sets differ: only in English %v, only in Chinese %v",
		rel, onlyEn, onlyZh)}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// codeBlock is one fenced block. info is the first word after the opening
// fence, and text is the block exactly as written, fences included.
type codeBlock struct {
	info string
	text string
}

// parsedPage is the part of a page that the translation must keep.
type parsedPage struct {
	hasFrontMatter bool
	title          string
	weight         string
	headings       []int
	codeBlocks     []codeBlock
	links          []string
	violations     []string
}

var (
	headingPattern = regexp.MustCompile(`^(#{1,6})(?:[ \t]+|$)`)
	fencePattern   = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})(.*)$")
	closingFence   = regexp.MustCompile("^[ \t]*(`{3,}|~{3,})[ \t]*$")
	listPrefix     = regexp.MustCompile(`^[ \t]*(?:[-*+]|\d{1,9}[.)])[ \t]+`)
	setextPattern  = regexp.MustCompile(`^ {0,3}[=-]+[ \t]*$`)
	refDefPattern  = regexp.MustCompile(`^ {0,3}\[[^\]]+\]:[ \t]*\S`)
	refUsePattern  = regexp.MustCompile(`\[[^\]]*\]\[[^\]]*\]`)
	markdownLink   = regexp.MustCompile(`!?\[[^\]]*\]\(\s*(<[^>]*>|[^)\s]+)`)
	htmlLink       = regexp.MustCompile(`(?i)(?:href|src)\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>"']+))`)
	shortcodeSpan  = regexp.MustCompile(`(?s)\{\{[<%].*?[>%]\}\}`)
	shortcodeLink  = regexp.MustCompile(`\blink\s*=\s*["']([^"']+)["']`)
	refShortcode   = regexp.MustCompile(`(?s)\{\{[<%]\s*(relref|ref)\b\s*(.*?)\s*[>%]\}\}`)
	pathParameter  = regexp.MustCompile(`\bpath\s*=\s*["']([^"']+)["']`)
	firstQuoted    = regexp.MustCompile(`["']([^"']+)["']`)
	urlScheme      = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)
)

// parsePage reads the front matter, headings, code blocks, internal links and
// forbidden syntax of one page. Headings and links inside fenced code blocks
// are ignored.
func parsePage(path string) (parsedPage, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return parsedPage{}, err
	}
	front, body, hasFrontMatter, frontOffset := splitFrontMatter(string(raw))

	result := parsedPage{hasFrontMatter: hasFrontMatter}
	if hasFrontMatter {
		var header struct {
			Title  any `yaml:"title"`
			Weight any `yaml:"weight"`
		}
		if err := yaml.Unmarshal([]byte(front), &header); err != nil {
			return parsedPage{}, fmt.Errorf("parse front matter: %w", err)
		}
		if header.Title != nil {
			result.title = strings.TrimSpace(fmt.Sprint(header.Title))
		}
		if header.Weight != nil {
			result.weight = fmt.Sprint(header.Weight)
		}
	}

	lines := strings.Split(body, "\n")
	// scannable keeps one entry per body line so that an offset in its joined
	// text maps to the same line number in the file. Lines inside code blocks
	// are left empty, which keeps their links out of the comparison.
	scannable := make([]string, len(lines))
	var (
		openFence     string
		openFenceLine int
		openFenceInfo string
		blockLines    []string
		prevLine      string
	)
	for index, line := range lines {
		lineNumber := index + 1
		if openFence != "" {
			blockLines = append(blockLines, line)
			if marker, ok := closingFenceMarker(line); ok && sameFence(openFence, marker) {
				result.codeBlocks = append(result.codeBlocks, codeBlock{
					info: openFenceInfo,
					text: strings.Join(blockLines, "\n"),
				})
				openFence = ""
				blockLines = nil
			}
			prevLine = line
			continue
		}
		if marker, info, ok := openingFence(line); ok {
			openFence = marker
			openFenceLine = lineNumber
			openFenceInfo = info
			blockLines = []string{strings.TrimRight(line, " \t")}
			prevLine = line
			continue
		}

		scannable[index] = line

		if setextPattern.MatchString(line) && strings.TrimSpace(prevLine) != "" {
			result.violations = append(result.violations,
				fmt.Sprintf("line %d: setext heading", lineNumber+frontOffset))
		}
		if refDefPattern.MatchString(line) {
			result.violations = append(result.violations,
				fmt.Sprintf("line %d: reference-style link definition", lineNumber+frontOffset))
		}
		if refUsePattern.MatchString(line) {
			result.violations = append(result.violations,
				fmt.Sprintf("line %d: reference-style link", lineNumber+frontOffset))
		}
		if match := headingPattern.FindStringSubmatch(line); match != nil {
			result.headings = append(result.headings, len(match[1]))
		}
		prevLine = line
	}
	if openFence != "" {
		// The rest of the page was read as part of the block, so nothing after
		// the opening fence was checked. Report it instead of silently
		// accepting the page.
		result.violations = append(result.violations,
			fmt.Sprintf("line %d: unclosed code fence", openFenceLine+frontOffset))
		result.codeBlocks = append(result.codeBlocks, codeBlock{
			info: openFenceInfo,
			text: strings.Join(blockLines, "\n"),
		})
	}

	text := strings.Join(scannable, "\n")
	result.links = append(result.links, refShortcodeLinks(text)...)
	for _, line := range scannable {
		for _, match := range markdownLink.FindAllStringSubmatch(line, -1) {
			if link, ok := internalLink(match[1]); ok {
				result.links = append(result.links, link)
			}
		}
		for _, match := range htmlLink.FindAllStringSubmatch(line, -1) {
			target := ""
			for _, group := range match[1:] {
				if group != "" {
					target = group
					break
				}
			}
			if link, ok := internalLink(target); ok {
				result.links = append(result.links, link)
			}
		}
	}
	for _, span := range shortcodeSpan.FindAllString(text, -1) {
		for _, match := range shortcodeLink.FindAllStringSubmatch(span, -1) {
			if link, ok := internalLink(match[1]); ok {
				result.links = append(result.links, link)
			}
		}
	}
	result.violations = append(result.violations, relrefViolations(text, frontOffset)...)

	result.links = sortedUnique(result.links)
	return result, nil
}

// splitFrontMatter returns the YAML front matter, the body that follows it,
// whether the file starts with a closed front matter block, and how many file
// lines the front matter occupies. The closing fence may be the last line of
// the file without a trailing newline.
func splitFrontMatter(text string) (front, body string, ok bool, offset int) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) == 0 || strings.TrimRight(lines[0], " \t") != "---" {
		return "", text, false, 0
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " \t") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), true, i + 1
		}
	}
	return "", text, false, 0
}

// stripFencePrefix removes blockquote and list-item markers so that a fence
// written inside them is still recognized. A fence may start on the same line
// as a list marker, as in "- ```".
func stripFencePrefix(line string) string {
	rest := line
	for {
		indent := len(rest) - len(strings.TrimLeft(rest, " \t"))
		if indent > 3 {
			break
		}
		trimmed := rest[indent:]
		if !strings.HasPrefix(trimmed, ">") {
			break
		}
		rest = trimmed[1:]
		if strings.HasPrefix(rest, " ") || strings.HasPrefix(rest, "\t") {
			rest = rest[1:]
		}
	}
	if match := listPrefix.FindString(rest); match != "" {
		rest = rest[len(match):]
	}
	return rest
}

// openingFence reports the fence marker and info word when line starts a
// fenced code block, including a fence inside a blockquote or on a list item.
func openingFence(line string) (marker, info string, ok bool) {
	match := fencePattern.FindStringSubmatch(stripFencePrefix(line))
	if match == nil {
		return "", "", false
	}
	fields := strings.Fields(match[2])
	if len(fields) > 0 {
		info = fields[0]
	}
	return match[1], info, true
}

// closingFenceMarker reports the fence marker when line closes a fenced block.
func closingFenceMarker(line string) (string, bool) {
	match := closingFence.FindStringSubmatch(stripFencePrefix(line))
	if match == nil {
		return "", false
	}
	return match[1], true
}

func sameFence(open, close string) bool {
	return open[0] == close[0] && len(close) >= len(open)
}

// refShortcodeLinks returns the targets of relref and ref shortcodes, wherever
// their parameters are written and in whatever order.
func refShortcodeLinks(text string) []string {
	var links []string
	for _, match := range refShortcode.FindAllStringSubmatch(text, -1) {
		args := match[2]
		target := ""
		if path := pathParameter.FindStringSubmatch(args); path != nil {
			target = path[1]
		} else if quoted := firstQuoted.FindStringSubmatch(args); quoted != nil {
			target = quoted[1]
		}
		if link, ok := internalLink(target); ok {
			links = append(links, link)
		}
	}
	return links
}

// relrefViolations reports a relref or ref shortcode that is neither the
// destination of a Markdown link nor a parameter of another shortcode: Hugo
// renders its URL as visible text in that position.
func relrefViolations(text string, lineOffset int) []string {
	spans := shortcodeSpan.FindAllStringIndex(text, -1)
	var violations []string
	for _, match := range refShortcode.FindAllStringSubmatchIndex(text, -1) {
		start, end := match[0], match[1]
		if strings.HasSuffix(strings.TrimRight(text[:start], " \t\r\n"), "](") {
			continue
		}
		nested := false
		for _, span := range spans {
			if span[0] < start && end <= span[1] {
				nested = true
				break
			}
		}
		if nested {
			continue
		}
		name := text[match[2]:match[3]]
		line := strings.Count(text[:start], "\n") + 1 + lineOffset
		violations = append(violations, fmt.Sprintf(
			"line %d: %s shortcode is not a link target or shortcode parameter", line, name))
	}
	return violations
}

// internalLink normalizes a Markdown or HTML link target and reports whether it
// points inside the site. A target that is itself a shortcode is handled as a
// relref or ref call instead.
func internalLink(target string) (string, bool) {
	target = strings.TrimSpace(target)
	if strings.HasPrefix(target, "<") && strings.HasSuffix(target, ">") {
		target = strings.TrimSpace(target[1 : len(target)-1])
	}
	if target == "" || strings.HasPrefix(target, "#") || strings.HasPrefix(target, "//") ||
		strings.HasPrefix(target, "{{") {
		return "", false
	}
	if urlScheme.MatchString(target) {
		return "", false
	}
	return target, true
}

func sortedUnique(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	unique := make([]string, 0, len(set))
	for value := range set {
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}
