// Package htmlparse достаёт из HTML-страницы SEO-теги, ссылки и ассеты.
package htmlparse

import (
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Типы ассетов.
const (
	AssetImage  = "image"
	AssetScript = "script"
	AssetStyle  = "style"
)

// SEO — базовые SEO-теги страницы; текст очищен от лишних пробелов, HTML-сущности раскодированы.
type SEO struct {
	HasTitle       bool
	Title          string
	HasDescription bool
	Description    string
	HasH1          bool
}

// Asset — ресурс страницы: абсолютный адрес и тип.
type Asset struct {
	URL  string
	Type string
}

// Document — то, что краулеру нужно от страницы.
type Document struct {
	SEO    SEO
	Links  []string // абсолютные http(s)-адреса из <a href>, без повторов, в порядке появления
	Assets []Asset  // картинки, затем скрипты, затем стили; без повторов
}

// assetSelectors задаёт порядок ассетов в отчёте.
var assetSelectors = []struct {
	selector, attr, kind string
}{
	{"img[src]", "src", AssetImage},
	{"script[src]", "src", AssetScript},
	{"link[href]", "href", AssetStyle},
}

// Parse разбирает страницу с адресом base.
func Parse(r io.Reader, base *url.URL) (Document, error) {
	dom, err := goquery.NewDocumentFromReader(r)
	if err != nil {
		return Document{}, fmt.Errorf("parse html: %w", err)
	}

	return Document{SEO: seo(dom), Links: links(dom, base), Assets: assets(dom, base)}, nil
}

func seo(dom *goquery.Document) SEO {
	var result SEO

	if title := dom.Find("title").First(); title.Length() > 0 {
		result.HasTitle = true
		result.Title = cleanText(title.Text())
	}

	dom.Find("meta[name]").EachWithBreak(func(_ int, meta *goquery.Selection) bool {
		if !strings.EqualFold(meta.AttrOr("name", ""), "description") {
			return true
		}

		result.HasDescription = true
		result.Description = cleanText(meta.AttrOr("content", ""))

		return false
	})

	result.HasH1 = dom.Find("h1").Length() > 0

	return result
}

func links(dom *goquery.Document, base *url.URL) []string {
	seen := map[string]bool{}

	var result []string

	dom.Find("a[href]").Each(func(_ int, a *goquery.Selection) {
		if link, ok := Resolve(base, a.AttrOr("href", "")); ok && !seen[link] {
			seen[link] = true
			result = append(result, link)
		}
	})

	return result
}

func assets(dom *goquery.Document, base *url.URL) []Asset {
	seen := map[string]bool{}

	var result []Asset

	for _, s := range assetSelectors {
		dom.Find(s.selector).Each(func(_ int, el *goquery.Selection) {
			if s.kind == AssetStyle && !isStylesheet(el) {
				return
			}

			if link, ok := Resolve(base, el.AttrOr(s.attr, "")); ok && !seen[link] {
				seen[link] = true
				result = append(result, Asset{URL: link, Type: s.kind})
			}
		})
	}

	return result
}

func isStylesheet(link *goquery.Selection) bool {
	for rel := range strings.FieldsSeq(link.AttrOr("rel", "")) {
		if strings.EqualFold(rel, "stylesheet") {
			return true
		}
	}

	return false
}

// Resolve приводит ссылку со страницы base к абсолютному адресу. Пустые ссылки и схемы кроме http(s)
// (mailto:, javascript:, tel:…) отбрасываются, якорь убирается.
func Resolve(base *url.URL, href string) (string, bool) {
	href = strings.TrimSpace(href)
	if href == "" {
		return "", false
	}

	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}

	resolved := base.ResolveReference(ref)
	if resolved.Scheme != "http" && resolved.Scheme != "https" || resolved.Host == "" {
		return "", false
	}

	return Normalize(resolved), true
}

// Normalize — единый вид адреса: без якоря, корень сайта без завершающего слеша.
func Normalize(u *url.URL) string {
	normalized := *u
	normalized.Fragment = ""
	normalized.RawFragment = ""

	if normalized.Path == "/" && normalized.RawQuery == "" {
		normalized.Path = ""
	}

	return normalized.String()
}

// cleanText схлопывает пробельные символы.
func cleanText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}
