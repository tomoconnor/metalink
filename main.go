package main

import (
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// Metadata represents the scraped metadata from a webpage
type Metadata struct {
	URL         string   `json:"url"`
	PageName    string   `json:"page_name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Images      []string `json:"images"`
}

func main() {
	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())

	e.GET("/metadata", getMetadataHandler)

	// Start server
	if err := e.Start(":8080"); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

// getMetadataHandler handles GET /metadata?url={url}
func getMetadataHandler(c echo.Context) error {
	rawURL := c.QueryParam("url")
	if rawURL == "" {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing url parameter"})
	}

	parsedURL, err := url.ParseRequestURI(rawURL)
	if err != nil {
		return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid url"})
	}

	// Fetch the page
	req, err := http.NewRequest("GET", parsedURL.String(), nil)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create request"})
	}
	req.Header.Set("User-Agent", "MetadataScraper/1.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to fetch url"})
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return c.JSON(http.StatusBadGateway, map[string]string{"error": "non-2xx response from target"})
	}

	// Parse HTML
	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to parse html"})
	}

	// Extract metadata
	meta := Metadata{URL: parsedURL.String()}

	// PageName / site_name
	meta.PageName = getFirstContent(doc, "meta[property='og:site_name']")
	if meta.PageName == "" {
		meta.PageName = parsedURL.Host
	}

	// Title
	meta.Title = getFirstContent(doc, "meta[property='og:title']")
	if meta.Title == "" {
		meta.Title = strings.TrimSpace(doc.Find("title").Text())
	}

	// Description
	meta.Description = getFirstContent(doc, "meta[property='og:description']")
	if meta.Description == "" {
		meta.Description = getFirstContent(doc, "meta[name='description']")
	}

	// Images (og:image)
	doc.Find("meta[property='og:image']").Each(func(i int, s *goquery.Selection) {
		if img, exists := s.Attr("content"); exists && img != "" {
			meta.Images = append(meta.Images, img)
		}
	})

	// Fallback: collect all img[src] if no og:image found
	if len(meta.Images) == 0 {
		doc.Find("img").Each(func(i int, s *goquery.Selection) {
			if src, exists := s.Attr("src"); exists && src != "" {
				// Resolve relative URLs
				u, err := url.Parse(src)
				if err == nil {
					meta.Images = append(meta.Images, parsedURL.ResolveReference(u).String())
				}
			}
		})
	}

	return c.JSON(http.StatusOK, meta)
}

// getFirstContent finds the first meta tag by selector and returns its content attribute
func getFirstContent(doc *goquery.Document, selector string) string {
	sel := doc.Find(selector)
	if sel != nil {
		if content, exists := sel.First().Attr("content"); exists {
			return strings.TrimSpace(content)
		}
	}
	return ""
}
