package builder

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCanonicalFeedURL(t *testing.T) {
	valid := map[string]string{
		"http://m.youtube.com/@channel/videos?si=tracking":       "https://www.youtube.com/@channel",
		"https://youtube.com/watch?v=abc&list=PL_123-abc":        "https://www.youtube.com/playlist?list=PL_123-abc",
		"https://www.youtube.com/channel/UC123/videos":           "https://www.youtube.com/channel/UC123",
		"https://www.vimeo.com/channels/news/":                   "https://vimeo.com/channels/news",
		"https://soundcloud.com/user/sets/playlist?utm_source=x": "https://soundcloud.com/user/sets/playlist",
		"https://twitch.tv/Example":                              "https://www.twitch.tv/example",
	}
	for input, expected := range valid {
		actual, err := CanonicalFeedURL(input)
		require.NoError(t, err, input)
		require.Equal(t, expected, actual)
	}
	for _, input := range []string{
		"", "file:///etc/passwd", "https://127.0.0.1/@foo", "https://[::1]/@foo",
		"https://169.254.169.254/latest/meta-data", "https://youtube.com.evil.test/@foo",
		"https://evilyoutube.com/@foo", "https://youtube.com@evil.test/@foo",
		"https://user:pass@youtube.com/@foo", "https://youtube.com:443/@foo",
		"https://youtube.com/redirect?q=https://127.0.0.1", "https://youtube.com/watch?v=abc",
		"https://youtube.com/playlist?list=--exec%20foo", "https://youtube.com/@foo%2F..%2Fredirect",
		"https://youtube.com/channel/../redirect", "https://youtube.com/@foo/redirect",
		"https://soundcloud.com/user/sets/../../redirect", "javascript:alert(1)",
	} {
		_, err := CanonicalFeedURL(input)
		require.Error(t, err, input)
	}
}
