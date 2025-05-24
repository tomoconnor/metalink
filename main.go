package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

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

// oEmbedResponse models JSON from YouTube's oEmbed endpoint
type oEmbedResponse struct {
	Title        string `json:"title"`
	AuthorName   string `json:"author_name"`
	ThumbnailURL string `json:"thumbnail_url"`
	Provider     string `json:"provider_name"`
}

// youtubeAPIResponse models JSON from YouTube Data API v3
type youtubeAPIResponse struct {
	Items []struct {
		Snippet struct {
			Title        string `json:"title"`
			Description  string `json:"description"`
			ChannelTitle string `json:"channelTitle"`
			Thumbnails   map[string]struct {
				URL string `json:"url"`
			} `json:"thumbnails"`
		} `json:"snippet"`
	} `json:"items"`
}

func main() {
	// Load API keys
	svcAPIKey := os.Getenv("API_KEY")
	ytAPIKey := os.Getenv("YT_API_KEY")
	if svcAPIKey == "" {
		log.Fatal("API_KEY environment variable not set")
	}

	e := echo.New()
	e.Use(middleware.Logger())
	e.Use(middleware.Recover())
	e.Use(apiKeyAuthMiddleware(svcAPIKey))

	e.GET("/metadata", getMetadataHandler(ytAPIKey))

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := e.Start(":" + port); err != nil {
		log.Fatalf("failed to start server: %v", err)
	}
}

// apiKeyAuthMiddleware checks for a valid X-API-Key header
func apiKeyAuthMiddleware(expectedKey string) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			key := c.Request().Header.Get("X-API-Key")
			if key == "" || key != expectedKey {
				return c.JSON(http.StatusUnauthorized, map[string]string{"error": "invalid or missing API key"})
			}
			return next(c)
		}
	}
}

// getMetadataHandler returns a handler that can use an optional YouTube API key
func getMetadataHandler(ytAPIKey string) echo.HandlerFunc {
	return func(c echo.Context) error {
		rawURL := c.QueryParam("url")
		if rawURL == "" {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "missing url parameter"})
		}
		parsedURL, err := url.ParseRequestURI(rawURL)
		if err != nil {
			return c.JSON(http.StatusBadRequest, map[string]string{"error": "invalid url"})
		}

		// YouTube special case
		if isYouTubeURL(parsedURL) {
			// Try oEmbed
			meta, err := fetchYouTubeOEmbed(rawURL)
			if err == nil {
				return c.JSON(http.StatusOK, meta)
			}
			// Fallback to Data API if key provided
			if ytAPIKey != "" {
				videoID := extractYouTubeID(parsedURL)
				if idMeta, err := fetchYouTubeDataAPI(videoID, ytAPIKey); err == nil {
					return c.JSON(http.StatusOK, idMeta)
				}
			}
			// Fall through to generic scraper if both fail
		}

		// Generic HTML scraping
		req, err := http.NewRequest("GET", parsedURL.String(), nil)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to create request"})
		}
		req.Header.Set("User-Agent",
			"Mozilla/5.0 (Windows NT 10.0; Win64; x64) "+
				"AppleWebKit/537.36 (KHTML, like Gecko) "+
				"Chrome/113.0.0.0 Safari/537.36")
		req.Header.Set("Accept-Language", "en-GB,en;q=0.9")
		req.Header.Set("Cache-Control", "no-cache")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return c.JSON(http.StatusBadGateway, map[string]string{"error": "failed to fetch target"})
		}
		defer resp.Body.Close()

		doc, err := goquery.NewDocumentFromReader(resp.Body)
		if err != nil {
			return c.JSON(http.StatusInternalServerError, map[string]string{"error": "failed to parse html"})
		}

		meta := Metadata{URL: rawURL}
		meta.PageName = getFirstContent(doc, "meta[property='og:site_name']")
		if meta.PageName == "" {
			meta.PageName = parsedURL.Host
		}
		meta.Title = getFirstContent(doc, "meta[property='og:title']")
		if meta.Title == "" {
			meta.Title = strings.TrimSpace(doc.Find("title").Text())
		}
		meta.Description = getFirstContent(doc, "meta[property='og:description']")
		if meta.Description == "" {
			meta.Description = getFirstContent(doc, "meta[name='description']")
		}
		doc.Find("meta[property='og:image']").Each(func(i int, s *goquery.Selection) {
			if img, ok := s.Attr("content"); ok {
				meta.Images = append(meta.Images, img)
			}
		})
		if len(meta.Images) == 0 {
			doc.Find("img").Each(func(i int, s *goquery.Selection) {
				if src, ok := s.Attr("src"); ok {
					if u, err := url.Parse(src); err == nil {
						meta.Images = append(meta.Images, parsedURL.ResolveReference(u).String())
					}
				}
			})
		}

		return c.JSON(http.StatusOK, meta)
	}
}

// isYouTubeURL returns true if the host is YouTube or youtu.be
func isYouTubeURL(u *url.URL) bool {
	h := u.Hostname()
	return strings.Contains(h, "youtube.com") || strings.Contains(h, "youtu.be")
}

// extractYouTubeID pulls the video ID from the URL
func extractYouTubeID(u *url.URL) string {
	if strings.Contains(u.Host, "youtu.be") {
		return strings.TrimPrefix(u.Path, "/")
	}
	return u.Query().Get("v")
}

// fetchYouTubeOEmbed queries YouTube's oEmbed endpoint
func fetchYouTubeOEmbed(videoURL string) (*Metadata, error) {
	oeURL := fmt.Sprintf(
		"https://www.youtube.com/oembed?url=%s&format=json",
		url.QueryEscape(videoURL),
	)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(oeURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("oEmbed error: %v", err)
	}
	defer resp.Body.Close()

	var oe oEmbedResponse
	if err := json.NewDecoder(resp.Body).Decode(&oe); err != nil {
		return nil, err
	}

	return &Metadata{
		URL:         videoURL,
		PageName:    oe.Provider,
		Title:       oe.Title,
		Description: oe.AuthorName,
		Images:      []string{oe.ThumbnailURL},
	}, nil
}

// fetchYouTubeDataAPI queries the YouTube Data API v3
func fetchYouTubeDataAPI(videoID, apiKey string) (*Metadata, error) {
	apiURL := fmt.Sprintf(
		"https://www.googleapis.com/youtube/v3/videos?part=snippet&id=%s&key=%s",
		videoID, apiKey,
	)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(apiURL)
	if err != nil || resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("YouTube API error: %v", err)
	}
	defer resp.Body.Close()

	var data youtubeAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	if len(data.Items) == 0 {
		return nil, fmt.Errorf("no video found for ID %s", videoID)
	}

	snip := data.Items[0].Snippet
	thumb := snip.Thumbnails["high"].URL

	return &Metadata{
		URL:         videoID,
		PageName:    snip.ChannelTitle,
		Title:       snip.Title,
		Description: snip.Description,
		Images:      []string{thumb},
	}, nil
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
