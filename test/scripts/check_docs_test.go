package scripts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCheckDocs ports every case of the bash harness ci/test-check-docs.sh,
// driving ci/check-docs.sh with throwaway fixture trees and asserting the same
// exit codes and output substrings. The checker takes ROOT as $1.
func TestCheckDocs(t *testing.T) {
	t.Parallel()
	for _, tc := range checkDocsCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := checkDocsFixture(t)
			if tc.mutate != nil {
				tc.mutate(t, root)
			}
			result := runCheckDocs(t, root)
			assertStatus(t, result, tc.wantStatus)
			if tc.wantMsg != "" {
				assertContains(t, "output", checkDocsOutput(result), tc.wantMsg)
			}
		})
	}
}

// checkDocsCase is one fixture mutation plus the checker's expected decision.
type checkDocsCase struct {
	name       string
	mutate     func(t *testing.T, root string)
	wantStatus int
	wantMsg    string
}

// checkDocsCases mirrors the bash harness case-by-case, in order.
var checkDocsCases = []checkDocsCase{
	{"valid tree", nil, 0, "all internal links are valid"},
	{
		"root docs markdown link",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "README.md"), "\n[Install](docs/install.md)\n")
		},
		1, "must be a rendered html link",
	},
	{
		"root docs leading-slash link",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "README.md"), "\n[Install](/docs/install.md)\n")
		},
		1, "must be a rendered html link",
	},
	{
		"root broken html link",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Missing](https://inoio.github.io/agents-sandbox/nope.html)\n",
			)
		},
		1, "does not exist",
	},
	{
		"root broken html dir link",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Missing](https://inoio.github.io/agents-sandbox/nope/)\n",
			)
		},
		1, "no source index page",
	},
	{
		"root anchored html link resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Install](https://inoio.github.io/agents-sandbox/install.html#install)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"root anchored html link dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Install](https://inoio.github.io/agents-sandbox/install.html#the-base-image)\n",
			)
		},
		1, "fragment 'the-base-image' matches no heading in docs/install.md",
	},
	{
		"root anchored broken html link",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Missing](https://inoio.github.io/agents-sandbox/nope.html#the-base-image)\n",
			)
		},
		1, "docs html link 'nope.md' does not exist",
	},
	{
		"root titled link",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Titled](https://inoio.github.io/agents-sandbox/nope.html \"a title\")\n",
			)
		},
		1, "docs html link 'nope.md' does not exist",
	},
	{
		"root reference link",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Commands][ref]\n\n[ref]: https://inoio.github.io/agents-sandbox/nope.html\n",
			)
		},
		1, "docs html link 'nope.md' does not exist",
	},
	{
		"root reference dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Commands][ref]\n\n[ref]: https://inoio.github.io/agents-sandbox/install.html#no-such-heading\n",
			)
		},
		1, "fragment 'no-such-heading' matches no heading in docs/install.md",
	},
	{
		"root reference angle-bracket fragment resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Commands][ref]\n\n[ref]: <https://inoio.github.io/agents-sandbox/install.html#install>\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"docs same-page reference fragment resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\n[Top][here]\n\n[here]: #introduction\n")
		},
		0, "all internal links are valid",
	},
	{
		"docs same-page reference dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n[Top][here]\n\n[here]: #no-such-heading\n",
			)
		},
		1, "fragment 'no-such-heading' matches no heading in docs/introduction.md",
	},
	{
		"root broken relative link",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "README.md"), "\n[Gone](MISSING.md)\n")
		},
		1, "link target 'MISSING.md' does not exist",
	},
	{
		"root balanced-paren link",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "README.md"), "\n[Paren](foo(1).md)\n")
		},
		1, "link target 'foo(1).md' does not exist",
	},
	{
		"docs plain relative link",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\n[Plain](install.md)\n")
		},
		1, "must use a Jekyll {% link %} tag",
	},
	{
		"docs broken jekyll link",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\n[Missing]({% link nope.md %})\n")
		},
		1, "{% link %} target 'nope.md' does not exist",
	},
	{
		"docs leading-slash jekyll link",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\n[Bad]({% link /install.md %})\n")
		},
		1, "must be relative",
	},
	{
		"docs diagram svg link",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n![diagram]({% link diagrams/arch.svg %})\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"external links ignored",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[ext](https://opencode.ai) [mail](mailto:x@example.com) [top](#top)\n",
			)
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\n[ext](https://opencode.ai)\n")
		},
		0, "all internal links are valid",
	},
	{
		"docs _site build output ignored",
		func(t *testing.T, root string) {
			checkDocsWrite(
				t,
				checkDocsFile(root, "docs/_site/superpowers/plans/note.md"),
				"[Missing]({% link nope.md %})\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"docs vendor dependency ignored",
		func(t *testing.T, root string) {
			checkDocsWrite(t, checkDocsFile(root, "docs/vendor/bundle/README.md"), "[Missing](missing.md)\n")
		},
		0, "all internal links are valid",
	},
	{
		"terminology harness",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\nThe harness starts the agent.\n")
		},
		1, `terminology: "harness"`,
	},
	{
		"terminology harness in code/url ignored",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\nThe `harness` name and [x](https://example.com/harness) are fine.\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"terminology fenced code ignored",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n```text\nharness simply above\n```\n\n> ```console\n> harness\n> ```\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"terminology nested fence ignored",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n````markdown\n```console\nharness simply above\n```\n````\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"terminology legacy prefix flagged",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\nSet OPENCODE_SANDBOX_FOO.\n")
		},
		1, "terminology: the OPENCODE_SANDBOX_ prefix",
	},
	{
		"terminology legacy prefix allowed on deprecations",
		func(t *testing.T, root string) {
			checkDocsWrite(
				t,
				checkDocsFile(root, "docs/deprecations.md"),
				"# Deprecations\n\nThe old prefix OPENCODE_SANDBOX_* still works.\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"terminology legacy prefix allowed on upgrading",
		func(t *testing.T, root string) {
			checkDocsWrite(
				t,
				checkDocsFile(root, "docs/install/upgrading.md"),
				"# Upgrading\n\nThe old prefix OPENCODE_SANDBOX_* still works.\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"terminology banned word",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\nThis is simply done.\n")
		},
		1, "terminology: avoid the filler words",
	},
	{
		"terminology lets",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\nLet's begin.\n")
		},
		1, "terminology: avoid the filler words",
	},
	{
		"terminology above/below",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\nSee the note above.\n")
		},
		1, "terminology: do not use positional",
	},
	{
		"fenced-hash not a heading",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n```console\n# not a heading\n```\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Bad](https://inoio.github.io/agents-sandbox/install.html#not-a-heading)\n",
			)
		},
		1, "fragment 'not-a-heading' matches no heading in docs/install.md",
	},
	{
		"backticked heading slug",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## `agents-sandbox doctor` fails\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Doctor](https://inoio.github.io/agents-sandbox/install.html#agents-sandbox-doctor-fails)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"duplicate heading suffix",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## Foo\n\n## Foo\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Second](https://inoio.github.io/agents-sandbox/install.html#foo-1)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"third duplicate heading suffix",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## Foo\n\n## Foo\n\n## Foo\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Third](https://inoio.github.io/agents-sandbox/install.html#foo-2)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"over-counted duplicate heading suffix rejected",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## Foo\n\n## Foo\n\n## Foo\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Fourth](https://inoio.github.io/agents-sandbox/install.html#foo-3)\n",
			)
		},
		1, "fragment 'foo-3' matches no heading in docs/install.md",
	},
	{
		"declared heading id resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## Foo {#bar}\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Declared](https://inoio.github.io/agents-sandbox/install.html#bar)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"declared heading id suppresses derived slug",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## Foo {#bar}\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Derived](https://inoio.github.io/agents-sandbox/install.html#foo)\n",
			)
		},
		1, "fragment 'foo' matches no heading in docs/install.md",
	},
	{
		"explicit html anchor resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/install.md"),
				"\n## Container engine not running\n\n<a id=\"docker-not-running\"></a>\n",
			)
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Legacy anchor](https://inoio.github.io/agents-sandbox/install.html#docker-not-running)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"derived heading anchor beside html anchor resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/install.md"),
				"\n## Container engine not running\n\n<a id=\"docker-not-running\"></a>\n",
			)
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Derived beside html](https://inoio.github.io/agents-sandbox/install.html#container-engine-not-running)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"html anchor in fenced code ignored",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n```\n<a id=\"ghost\"></a>\n```\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Ghost](https://inoio.github.io/agents-sandbox/install.html#ghost)\n",
			)
		},
		1, "fragment 'ghost' matches no heading in docs/install.md",
	},
	{
		"front-matter hash not a heading",
		func(t *testing.T, root string) {
			checkDocsWrite(
				t,
				checkDocsFile(root, "docs/install.md"),
				"---\ntitle: Install\n# install-fragment\n---\n\n# Install\n",
			)
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Hidden](https://inoio.github.io/agents-sandbox/install.html#install-fragment)\n",
			)
		},
		1, "fragment 'install-fragment' matches no heading in docs/install.md",
	},
	{
		"dash-led heading slug resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## -foo bar\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Dash](https://inoio.github.io/agents-sandbox/install.html#-foo-bar)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"digit-led heading slug",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## 1. Turn off host-config fallback\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Step](https://inoio.github.io/agents-sandbox/install.html#1-turn-off-host-config-fallback)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"digit-led heading stripped form rejected",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## 1. Turn off host-config fallback\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Step](https://inoio.github.io/agents-sandbox/install.html#turn-off-host-config-fallback)\n",
			)
		},
		1, "fragment 'turn-off-host-config-fallback' matches no heading in docs/install.md",
	},
	{
		"intraword underscore heading slug",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n## foo_bar baz\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Hook](https://inoio.github.io/agents-sandbox/install.html#foo_bar-baz)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"six-hash heading recognized",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n###### Six\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Six](https://inoio.github.io/agents-sandbox/install.html#six)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"seven-hash line not a heading",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/install.md"), "\n####### Seven\n")
			checkDocsAppend(
				t,
				checkDocsFile(root, "README.md"),
				"\n[Seven](https://inoio.github.io/agents-sandbox/install.html#seven)\n",
			)
		},
		1, "fragment 'seven' matches no heading in docs/install.md",
	},
	{
		"docs in-page dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "docs/introduction.md"), "\n[Gone](#no-such-heading)\n")
		},
		1, "fragment 'no-such-heading' matches no heading in docs/introduction.md",
	},
	{
		"docs cross-page dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n[Gone]({% link install.md %}#no-such-heading)\n",
			)
		},
		1, "fragment 'no-such-heading' matches no heading in docs/install.md",
	},
	{
		"diagram fragment ignored",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n![diagram]({% link diagrams/arch.svg %}#whatever)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"docs rendered html link dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n[Rendered](https://inoio.github.io/agents-sandbox/install.html#no-such-heading)\n",
			)
		},
		1, "fragment 'no-such-heading' matches no heading in docs/install.md",
	},
	{
		"docs rendered html link resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "docs/introduction.md"),
				"\n[Rendered](https://inoio.github.io/agents-sandbox/install.html#install)\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"go docslink dead fragment",
		func(t *testing.T, root string) {
			checkDocsWrite(t, checkDocsFile(root, "internal/fixture/fixture.go"),
				strings.ReplaceAll(checkDocsFixtureGo, "docker-not-running", "dead-anchor"))
		},
		1, "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md",
	},
	{
		"go docslink missing page",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "internal/fixture/fixture.go"),
				"\nvar _ = docslink.SectionURL(\"nope\", \"whatever\")\n",
			)
		},
		1, "doc link points at missing page docs/nope.md",
	},
	{
		"go literal url dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "internal/fixture/fixture.go"),
				"\nconst literal = \"https://inoio.github.io/agents-sandbox/troubleshooting.html#dead-anchor\"\n",
			)
		},
		1, "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md",
	},
	{
		"go docslink non-troubleshooting page",
		func(t *testing.T, root string) {
			checkDocsWrite(
				t,
				checkDocsFile(root, "internal/fixture/fixture.go"),
				"package fixture\n\nimport \"github.com/inoio/agents-sandbox/internal/docslink\"\n\nvar _ = docslink.SectionURL(\"install\", \"install\")\n",
			)
		},
		0, "all internal links are valid",
	},
	{
		"go unqualified SectionURL extracted",
		func(t *testing.T, root string) {
			checkDocsWrite(t, checkDocsFile(root, "internal/docslink/fixture.go"),
				"package docslink\n\nvar _ = SectionURL(\"troubleshooting\", \"dead-anchor\")\n")
		},
		1, "fragment 'dead-anchor' matches no heading in docs/troubleshooting.md",
	},
	{
		"go mySectionURL not extracted",
		func(t *testing.T, root string) {
			checkDocsWrite(t, checkDocsFile(root, "internal/fixture/fixture.go"),
				"package fixture\n\nvar _ = mySectionURL(\"troubleshooting\", \"dead-anchor\")\n")
		},
		0, "all internal links are valid",
	},
	{
		"go multiline SectionURL dead fragment",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "internal/fixture/fixture.go"),
				"\nvar _ = docslink.SectionURL(\n\t\"troubleshooting\",\n\t\"dead-anchor\",\n)\n")
		},
		1, "internal/fixture/fixture.go:7: fragment 'dead-anchor'",
	},
	{
		"go multiline SectionURL resolves",
		func(t *testing.T, root string) {
			checkDocsAppend(t, checkDocsFile(root, "internal/fixture/fixture.go"),
				"\nvar _ = docslink.SectionURL(\n\t\"troubleshooting\",\n\t\"docker-not-running\",\n)\n")
		},
		0, "all internal links are valid",
	},
	{
		"go two SectionURLs on one line",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "internal/fixture/fixture.go"),
				"\nvar _ = []string{docslink.SectionURL(\"troubleshooting\", \"docker-not-running\"), docslink.SectionURL(\"troubleshooting\", \"dead-anchor\")}\n",
			)
		},
		1, "internal/fixture/fixture.go:7: fragment 'dead-anchor'",
	},
	{
		"go two literal urls on one line",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "internal/fixture/fixture.go"),
				"\nvar _ = []string{\"https://inoio.github.io/agents-sandbox/troubleshooting.html#docker-not-running\", \"https://inoio.github.io/agents-sandbox/troubleshooting.html#dead-anchor\"}\n",
			)
		},
		1, "internal/fixture/fixture.go:7: fragment 'dead-anchor'",
	},
	{
		"go column-zero call keeps later line numbers",
		func(t *testing.T, root string) {
			checkDocsAppend(
				t,
				checkDocsFile(root, "internal/fixture/fixture.go"),
				"\nSectionURL(\"troubleshooting\", \"docker-not-running\")\nvar _ = docslink.SectionURL(\"troubleshooting\", \"dead-anchor\")\n",
			)
		},
		1, "internal/fixture/fixture.go:8: fragment 'dead-anchor'",
	},
}

// checkDocsFixtureGo is the base Go fixture the bash harness wrote, reused by the
// dead-fragment case (which rewrites the anchor) and the append cases (which
// extend it, pinning the reported line numbers).
const checkDocsFixtureGo = `package fixture

import "github.com/inoio/agents-sandbox/internal/docslink"

var _ = docslink.SectionURL("troubleshooting", "docker-not-running")
`

// checkDocsFixture builds the throwaway tree the bash harness's new_fixture made:
// a root README whose docs links are rendered HTML, docs pages that link with
// Jekyll {% link %} tags, a .puml diagram, a Go file calling
// docslink.SectionURL, and a troubleshooting page.
func checkDocsFixture(tb testing.TB) string {
	tb.Helper()
	root := tb.TempDir()
	checkDocsWrite(tb, checkDocsFile(root, "README.md"), `# Root

- [Intro](https://inoio.github.io/agents-sandbox/introduction.html)
- [Configuration](https://inoio.github.io/agents-sandbox/configuration/)
- [Contributing](CONTRIBUTING.md)
`)
	checkDocsWrite(tb, checkDocsFile(root, "CONTRIBUTING.md"), "")
	checkDocsWrite(tb, checkDocsFile(root, "docs/introduction.md"), `# Introduction

See [Install]({% link install.md %}) and [Launcher]({% link configuration/launcher.md %}).

- [top](#introduction)
- [Install section]({% link install.md %}#install)
`)
	checkDocsWrite(tb, checkDocsFile(root, "docs/install.md"), "# Install\n")
	checkDocsWrite(tb, checkDocsFile(root, "docs/configuration/index.md"), "# Configuration\n")
	checkDocsWrite(tb, checkDocsFile(root, "docs/configuration/launcher.md"), "# Launcher\n")
	checkDocsWrite(tb, checkDocsFile(root, "docs/diagrams/arch.puml"), "@startuml\n@enduml\n")
	checkDocsWrite(tb, checkDocsFile(root, "internal/fixture/fixture.go"), checkDocsFixtureGo)
	checkDocsWrite(tb, checkDocsFile(root, "docs/troubleshooting.md"), `# Troubleshooting

## Docker not running
`)
	return root
}

// runCheckDocs runs ci/check-docs.sh with root as its $1 argument.
func runCheckDocs(t *testing.T, root string) ScriptResult {
	t.Helper()
	script := filepath.Join(repositoryRoot(t), "ci", "check-docs.sh")
	return RunScript(t, root, nil, script, root)
}

// checkDocsOutput is the checker's combined output, matching the bash harness's
// `2>&1` capture.
func checkDocsOutput(result ScriptResult) string {
	return result.Stdout + result.Stderr
}

// checkDocsFile joins a fixture-relative slash path onto root.
func checkDocsFile(root, rel string) string {
	return filepath.Join(root, filepath.FromSlash(rel))
}

// checkDocsWrite writes content to path, creating its parent directories.
func checkDocsWrite(tb testing.TB, path, content string) {
	tb.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		tb.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		tb.Fatalf("write %s: %v", path, err)
	}
}

// checkDocsAppend appends content to path, which must already exist.
func checkDocsAppend(tb testing.TB, path, content string) {
	tb.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		tb.Fatalf("open %s: %v", path, err)
	}
	if _, err := file.WriteString(content); err != nil {
		tb.Fatalf("append %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		tb.Fatalf("close %s: %v", path, err)
	}
}
