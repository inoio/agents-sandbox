// Package docslink composes links to the published documentation site.
package docslink

// BaseURL is the root of the published documentation site.
const BaseURL = "https://inoio.github.io/agents-sandbox"

// SectionURL returns the URL of a heading on a published docs page. page is the
// source page name without its .md suffix (e.g. "troubleshooting").
func SectionURL(page, anchor string) string {
	return BaseURL + "/" + page + ".html#" + anchor
}
