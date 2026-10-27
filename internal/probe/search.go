package probe

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// SearchBrave adds discovery candidates, never update findings. Search only
// contributes URLs: all version claims require independently fetched evidence.
func SearchBrave(ctx context.Context, r *Report) error {
	key := strings.TrimSpace(os.Getenv("BRAVE_SEARCH_API_KEY"))
	if key == "" {
		return errors.New("BRAVE_SEARCH_API_KEY not set")
	}
	// A bounded, app-specific search: avoid uploading installed paths or bundle metadata.
	query := fmt.Sprintf("%s macOS application official release notes download", r.Application.Name)
	u := "https://api.search.brave.com/res/v1/web/search?q=" + url.QueryEscape(query) + "&count=5"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", key)
	resp, err := safeClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if ct := strings.ToLower(resp.Header.Get("Content-Type")); !strings.Contains(ct, "json") {
		return errors.New("unexpected search content type")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 256*1024 {
		return errors.New("search response too large")
	}
	return addBraveResults(r, data)
}

func addBraveResults(r *Report, data []byte) error {
	var response struct {
		Web struct {
			Results []struct {
				URL   string `json:"url"`
				Title string `json:"title"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return fmt.Errorf("parse search results: %w", err)
	}
	for i, result := range response.Web.Results {
		if i >= 5 {
			break
		}
		// Search rank is not authority. Score only prioritises search hits above
		// weak, unrelated metadata; embedded updater URLs remain higher.
		score := 68 - i
		title := strings.ToLower(result.Title)
		if strings.Contains(title, "release") || strings.Contains(title, "changelog") || strings.Contains(title, "download") {
			score += 5
		}
		add(r, result.URL, "search:brave", score)
	}
	return nil
}
