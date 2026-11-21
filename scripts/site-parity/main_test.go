package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeTree creates a content tree below root from a map of relative paths to
// file contents.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create directory for %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
}

// checkTrees writes the two trees into a temporary directory and runs check.
func checkTrees(t *testing.T, en, zh map[string]string) []string {
	t.Helper()
	dir := t.TempDir()
	enDir := filepath.Join(dir, "en")
	zhDir := filepath.Join(dir, "zh")
	for _, root := range []string{enDir, zhDir} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("create %s: %v", root, err)
		}
	}
	writeTree(t, enDir, en)
	writeTree(t, zhDir, zh)
	problems, err := check(enDir, zhDir)
	if err != nil {
		t.Fatalf("check: %v", err)
	}
	return problems
}

func requireProblem(t *testing.T, problems []string, substring string) {
	t.Helper()
	for _, problem := range problems {
		if strings.Contains(problem, substring) {
			return
		}
	}
	t.Fatalf("no problem contains %q; got %v", substring, problems)
}

func requirePass(t *testing.T, problems []string) {
	t.Helper()
	if len(problems) != 0 {
		t.Fatalf("matching pages reported differences: %v", problems)
	}
}

// paired returns equivalent English and Chinese page bodies.
func paired(frontMatter, body string) (string, string) {
	return frontMatter + "\n" + body, frontMatter + "\n" + body
}

func TestCheckAcceptsMatchingPages(t *testing.T) {
	en, zh := paired(`---
title: Docs
weight: 1
---`, `
## One

See [the guide](../guide.md).

`+"```bash\npeers --help\n```\n")
	requirePass(t, checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh}))
}

func TestCheckReportsFileOnlyInOneLanguage(t *testing.T) {
	problems := checkTrees(t,
		map[string]string{
			"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n",
			"docs/extra.md":  "---\ntitle: Extra\nweight: 2\n---\n",
		},
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n"})
	requireProblem(t, problems, "docs/extra.md: present only in English")
}

func TestCheckReportsFileOnlyInChinese(t *testing.T) {
	problems := checkTrees(t,
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n"},
		map[string]string{
			"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n",
			"docs/extra.md":  "---\ntitle: Extra\nweight: 2\n---\n",
		})
	requireProblem(t, problems, "docs/extra.md: present only in Chinese")
}

func TestCheckReportsMissingFrontMatter(t *testing.T) {
	problems := checkTrees(t,
		map[string]string{"docs/_index.md": "No front matter.\n"},
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n"})
	requireProblem(t, problems, "docs/_index.md: English page has no YAML front matter")
}

func TestCheckReportsMissingTitle(t *testing.T) {
	problems := checkTrees(t,
		map[string]string{"docs/_index.md": "---\nweight: 1\n---\n"},
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n"})
	requireProblem(t, problems, "docs/_index.md: English page has no title")
}

func TestCheckReportsMissingWeight(t *testing.T) {
	problems := checkTrees(t,
		map[string]string{"docs/_index.md": "---\ntitle: Docs\n---\n"},
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n"})
	requireProblem(t, problems, "docs/_index.md: English page has no weight")
}

func TestCheckAcceptsFrontMatterEndingAtEOF(t *testing.T) {
	body := "---\ntitle: Docs\nweight: 1\n---"
	requirePass(t, checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body}))
}

func TestCheckReportsHeadingLevelMismatch(t *testing.T) {
	en, zh := `---
title: Docs
weight: 1
---

## Setup
`, `---
title: Docs
weight: 1
---

### Setup
`
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: heading levels differ")
}

func TestCheckReportsSetextHeading(t *testing.T) {
	body := "---\ntitle: Docs\nweight: 1\n---\n\nSetup\n=====\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body})
	requireProblem(t, problems, "setext heading")
}

func TestCheckReportsReferenceStyleLink(t *testing.T) {
	body := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide][guide].\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body})
	requireProblem(t, problems, "reference-style link")
}

func TestCheckReportsReferenceStyleLinkDefinition(t *testing.T) {
	body := "---\ntitle: Docs\nweight: 1\n---\n\n[guide]: ../guide.md\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body})
	requireProblem(t, problems, "reference-style link definition")
}

func TestCheckReportsCodeBlockMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\npeers --help\n```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\npeers --version\n```\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block 1 differs")
}

func TestCheckReportsCodeBlockCountMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\none\n```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\none\n```\n\n```bash\ntwo\n```\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block count differs")
}

func TestCheckReportsMermaidCountMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n```\n\n```mermaid\ngraph LR\n```\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block count differs")
}

func TestCheckAcceptsTranslatedMermaidContent(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n  A[Broker]\n```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n  A[代理]\n```\n"
	requirePass(t, checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh}))
}

func TestCheckReportsWeightMismatch(t *testing.T) {
	problems := checkTrees(t,
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n"},
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 2\n---\n"})
	requireProblem(t, problems, "docs/_index.md: weight differs")
}

func TestCheckReportsLinkMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide](../guide.md).\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide](../concepts.md).\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckReportsRelrefMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n{{< relref \"docs/guide\" >}}\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n{{< relref \"docs/concepts\" >}}\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckReportsRelrefPathMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n{{< relref path=\"docs/guide\" >}}\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n{{< relref path=\"docs/concepts\" >}}\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckReportsCardLinkMismatch(t *testing.T) {
	en := "---\ntitle: Home\nweight: 1\n---\n\n{{< card link=\"docs/guide\" title=\"Guide\" >}}\n"
	zh := "---\ntitle: Home\nweight: 1\n---\n\n{{< card link=\"docs/concepts\" title=\"指南\" >}}\n"
	problems := checkTrees(t, map[string]string{"_index.md": en}, map[string]string{"_index.md": zh})
	requireProblem(t, problems, "_index.md: internal link sets differ")
}

func TestCheckIgnoresHeadingsAndLinksInCodeBlocks(t *testing.T) {
	// The mermaid blocks differ inside, which is allowed, so removing the
	// skip logic would collect their headings and links and fail here.
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n# Not a heading\n[not a link](first)\n```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n## Also not a heading\n[also not a link](second)\n```\n"
	requirePass(t, checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh}))
}

// The difference after the closing fence is only compared when the fence is
// recognized as closed. If it is not, the following heading joins the block and
// the heading levels look equal.
func TestCheckClosesIndentedFence(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n- Step\n\n    ```bash\n    one\n    ```\n\n## Setup\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n- Step\n\n    ```bash\n    one\n    ```\n\n### Setup\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: heading levels differ")
}

func TestCheckReportsTildeFenceContentMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n~~~bash\none\n~~~\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n~~~bash\ntwo\n~~~\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block 1 differs")
}

func TestCheckReportsUnclosedFenceContentMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\none\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\ntwo\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block 1 differs")
}

// A longer closing fence still closes the block. As in the indented case, the
// heading after the fence is the only difference, so it is visible only when
// the fence is recognized as closed.
func TestCheckClosesLongerFence(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\none\n````\n\n## Setup\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```bash\none\n````\n\n### Setup\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: heading levels differ")
}

// A missing closing fence must not swallow the rest of the page. The two
// mermaid blocks differ only in the heading after the opening fence, which is
// exempt as diagram text; the unclosed fence itself must be reported.
func TestCheckReportsUnclosedMermaidFence(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n## One\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```mermaid\ngraph TD\n### One\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: English: line 6: unclosed code fence")
	requireProblem(t, problems, "docs/_index.md: Chinese: line 6: unclosed code fence")
}

func TestCheckReportsBlockquoteFenceContentMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n> ```bash\n> one\n> ```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n> ```bash\n> two\n> ```\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block 1 differs")
}

func TestCheckReportsListFenceContentMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n- ```bash\n  one\n  ```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n- ```bash\n  two\n  ```\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block 1 differs")
}

func TestCheckReportsHTMLLinkMismatch(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n<a href=\"docs/guide.md\">Guide</a>\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n<a href=\"docs/concepts.md\">指南</a>\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckIgnoresExternalLinks(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n[site](https://example.com/a) [mail](mailto:a@example.com) [top](#top)\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n[site](https://example.org/b) [mail](mailto:b@example.org) [top](#other)\n"
	requirePass(t, checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh}))
}

func TestCheckReportsStandaloneRelref(t *testing.T) {
	body := "---\ntitle: Docs\nweight: 1\n---\n\nSee {{< relref \"docs/guide\" >}} for details.\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body})
	requireProblem(t, problems, "relref shortcode is not a link target or shortcode parameter")
}

func TestCheckAcceptsRelrefAsLinkTarget(t *testing.T) {
	body := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide]({{< relref \"docs/guide\" >}}).\n"
	requirePass(t, checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body}))
}

func TestCheckAcceptsRelrefInShortcodeParameter(t *testing.T) {
	body := `---
title: Docs
weight: 1
---

{{< card link="{{< relref "docs/guide" >}}" title="Guide" >}}
`
	requirePass(t, checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body}))
}

func TestCheckCollectsRefShortcodeLinks(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide]({{< ref \"docs/guide\" >}}).\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide]({{< ref \"docs/concepts\" >}}).\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckCollectsRelrefLangParameter(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide]({{< relref lang=\"zh\" path=\"docs/guide\" >}}).\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\nSee [the guide]({{< relref lang=\"zh\" path=\"docs/concepts\" >}}).\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckCollectsMultilineShortcodeParameter(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n{{< card\n  link=\"docs/guide\"\n  title=\"Guide\" >}}\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n{{< card\n  link=\"docs/concepts\"\n  title=\"Guide\" >}}\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckCollectsSingleQuotedHTMLLink(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n<a href='docs/guide.md'>Guide</a>\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n<a href='docs/concepts.md'>Guide</a>\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckCollectsUnquotedHTMLLink(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n<a href=docs/guide.md>Guide</a>\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n<a href=docs/concepts.md>Guide</a>\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: internal link sets differ")
}

func TestCheckComparesCapitalizedMermaidBlock(t *testing.T) {
	en := "---\ntitle: Docs\nweight: 1\n---\n\n```Mermaid\ngraph TD\n```\n"
	zh := "---\ntitle: Docs\nweight: 1\n---\n\n```Mermaid\ngraph LR\n```\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": en}, map[string]string{"docs/_index.md": zh})
	requireProblem(t, problems, "docs/_index.md: code block 1 differs")
}

func TestCheckReportsViolationLineAfterFrontMatter(t *testing.T) {
	body := "---\ntitle: Docs\nweight: 1\n---\n\nSetup\n=====\n"
	problems := checkTrees(t, map[string]string{"docs/_index.md": body}, map[string]string{"docs/_index.md": body})
	requireProblem(t, problems, "line 7: setext heading")
}

func TestCheckReportsEmptyTrees(t *testing.T) {
	problems := checkTrees(t, map[string]string{}, map[string]string{})
	requireProblem(t, problems, "no pages found")
}

func TestCheckReportsEmptyChineseTree(t *testing.T) {
	problems := checkTrees(t,
		map[string]string{"docs/_index.md": "---\ntitle: Docs\nweight: 1\n---\n"},
		map[string]string{})
	requireProblem(t, problems, "no pages found")
}
