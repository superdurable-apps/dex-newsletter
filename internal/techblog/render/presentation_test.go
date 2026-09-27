package render

import (
	"testing"

	"github.com/superdurable-apps/dex-newsletter/internal/techblog/config"
	"github.com/superdurable-apps/dex-newsletter/internal/techblog/model"
)

func TestBlogPresentationFromConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		configuration config.BlogConfiguration
		want          BlogPresentation
	}{
		{
			name:          "default configuration",
			configuration: config.Default().Blog,
			want:          BlogPresentation{SiteName: "Dex Engineering Blog", Author: "The Dex team"},
		},
		{
			name: "fields trimmed and non-presentation fields ignored",
			configuration: config.BlogConfiguration{
				SiteName:          "  Site  ",
				Author:            "\tAuthor\n",
				StyleGuide:        "ignored",
				PublicBaseURL:     " https://blog.example.com/ ",
				ArtifactDirectory: "ignored",
			},
			want: BlogPresentation{SiteName: "Site", Author: "Author", PublicBaseURL: "https://blog.example.com/"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := BlogPresentationFromConfiguration(test.configuration); got != test.want {
				t.Fatalf("BlogPresentationFromConfiguration = %#v, want %#v", got, test.want)
			}
		})
	}
}

func TestPublishedBlogURL(t *testing.T) {
	post := model.BlogPost{Title: "Hello", Slug: "hello-world"}
	tests := []struct {
		name string
		base string
		post model.BlogPost
		want string
	}{
		{name: "empty base", base: "", post: post, want: ""},
		{name: "blank base", base: "   ", post: post, want: ""},
		{name: "base without slash", base: "https://blog.example.com", post: post, want: "https://blog.example.com/hello-world.html"},
		{name: "base with trailing slashes", base: "https://blog.example.com/posts///", post: post, want: "https://blog.example.com/posts/hello-world.html"},
		{name: "http base", base: "http://localhost:8080/blog/", post: post, want: "http://localhost:8080/blog/hello-world.html"},
		{name: "base trimmed", base: "  https://blog.example.com/  ", post: post, want: "https://blog.example.com/hello-world.html"},
		{name: "unnormalized slug sanitized", base: "https://blog.example.com", post: model.BlogPost{Title: "T", Slug: "../../Admin Panel"}, want: "https://blog.example.com/admin-panel.html"},
		{name: "slug derived from title", base: "https://blog.example.com", post: model.BlogPost{Title: "Retries Landed!"}, want: "https://blog.example.com/retries-landed.html"},
		{name: "javascript base rejected", base: "javascript:alert(1)//", post: post, want: ""},
		{name: "relative base rejected", base: "/blog", post: post, want: ""},
		{name: "scheme only base rejected", base: "https://", post: post, want: ""},
		{name: "base with query rejected", base: "https://blog.example.com/?page=1", post: post, want: ""},
		{name: "base with fragment rejected", base: "https://blog.example.com/#top", post: post, want: ""},
		{name: "base with userinfo rejected", base: "https://user@blog.example.com", post: post, want: ""},
		{name: "base with quote rejected", base: "https://blog.example.com/\"onmouseover=", post: post, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := PublishedBlogURL(test.post, BlogPresentation{PublicBaseURL: test.base}); got != test.want {
				t.Fatalf("PublishedBlogURL = %q, want %q", got, test.want)
			}
		})
	}
}

func TestBlogArtifactFileName(t *testing.T) {
	tests := []struct {
		name string
		post model.BlogPost
		want string
	}{
		{name: "normalized slug", post: sampleBlogPost(), want: "connector-retries-sdk.html"},
		{name: "traversal slug", post: model.BlogPost{Title: "T", Slug: "../../../etc/passwd"}, want: "etc-passwd.html"},
		{name: "separator only slug uses title", post: model.BlogPost{Title: "My Post", Slug: "/"}, want: "my-post.html"},
		{name: "control characters in title", post: model.BlogPost{Title: "a\x00b c"}, want: "ab-c.html"},
		{name: "nothing usable", post: model.BlogPost{}, want: fallbackBlogSlug + ".html"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := BlogArtifactFileName(test.post); got != test.want {
				t.Fatalf("BlogArtifactFileName = %q, want %q", got, test.want)
			}
		})
	}
}
