package builder

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/mxpv/podsync/pkg/model"
)

var webFeedPart = regexp.MustCompile(`^[\p{L}\p{N}_.@-]+$`)
var webPlaylistID = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// CanonicalFeedURL accepts only known provider resources. Never fetch a supplied
// URL to validate it: reconstruct an HTTPS URL from a host and resource allowlist.
func CanonicalFeedURL(link string) (string, error) {
	invalid := fmt.Errorf("%w: enter a supported channel or playlist URL", model.ErrInvalidFeed)
	if len(link) > 2048 {
		return "", invalid
	}
	u, err := url.Parse(strings.TrimSpace(link))
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") ||
		u.Port() != "" || u.RawPath != "" {
		return "", invalid
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for _, part := range parts {
		if !webFeedPart.MatchString(part) || part == "." || part == ".." {
			return "", invalid
		}
	}
	var host, path, query string
	switch strings.ToLower(u.Host) {
	case "youtube.com", "www.youtube.com", "m.youtube.com":
		host = "www.youtube.com"
		switch {
		case len(parts) == 1 && (parts[0] == "playlist" || parts[0] == "watch"):
			id := u.Query().Get("list")
			if !webPlaylistID.MatchString(id) {
				return "", invalid
			}
			path, query = "/playlist", "list="+id
		case (parts[0] == "channel" || parts[0] == "user") && len(parts) >= 2 && len(parts) <= 3:
			if len(parts) == 3 && parts[2] != "videos" && parts[2] != "podcasts" {
				return "", invalid
			}
			path = "/" + parts[0] + "/" + parts[1]
		case strings.HasPrefix(parts[0], "@") && len(parts[0]) > 1 && len(parts) <= 2:
			if len(parts) == 2 && parts[1] != "videos" && parts[1] != "podcasts" {
				return "", invalid
			}
			path = "/" + parts[0]
		default:
			return "", invalid
		}
	case "vimeo.com", "www.vimeo.com":
		host = "vimeo.com"
		if len(parts) != 1 && (len(parts) != 2 || (parts[0] != "channels" && parts[0] != "groups")) {
			return "", invalid
		}
		path = "/" + strings.Join(parts, "/")
	case "soundcloud.com", "www.soundcloud.com":
		host = "soundcloud.com"
		if len(parts) != 3 || parts[1] != "sets" {
			return "", invalid
		}
		path = "/" + strings.Join(parts, "/")
	case "twitch.tv", "www.twitch.tv":
		host = "www.twitch.tv"
		if len(parts) != 1 {
			return "", invalid
		}
		path = "/" + strings.ToLower(parts[0])
	default:
		return "", invalid
	}
	canonical := (&url.URL{Scheme: "https", Host: host, Path: path, RawQuery: query}).String()
	if _, err := ParseURL(canonical); err != nil {
		return "", invalid
	}
	return canonical, nil
}
