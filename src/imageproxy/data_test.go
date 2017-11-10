package imageproxy

import (
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

var emptyOptions = Options{}

// Width, Height, Fit, Rotate, FlipVertical, FlipHorizontal, Quality, Format
// go test imageproxy -v -run "TestOptionsToString"
func TestOptionsToString(t *testing.T) {
	fmt.Printf("TestOptionsToString\n")
	tests := []struct {
		Options Options
		String  string
	}{
		{
			emptyOptions,
			"",
		},
		{
			Options{1, 2, true, 90, true, true, 80, ""},
			"1x2,fit,r90,fv,fh,q80",
		},
		{
			Options{0.15, 1.3, false, 45, false, false, 95, ""},
			"0.15x1.3,r45,q95",
		},
	}

	for i, tt := range tests {
		fmt.Printf("Index: %d\n", i)
		if got, want := tt.Options.String(), tt.String; got != want {
			t.Errorf("%d. Options.String returned %v, want %v", i, got, want)
		}
	}
}

// go test imageproxy -v -run "TestRegexParse"
func TestRegexParse(t *testing.T) {
	// path 格式可能为:
	//
	// /tools/im/150/production/improxy/6a/82e2c962fb727886aa6d7cce7107d7.jpeg/ts10000

	// host之后要么就是没有Options的URL; 要么带有Options

	urls := []string{
		"/tools/im/150/production/improxy/6a/82e2c962fb727886aa6d7cce7107d7.jpeg?ts=100000",
		"/tools/im/150/production/improxy/6a/82e2c962fb727886aa6d7cce7107d7.jpeg/ts10000",
	}
	for i := 0; i < len(urls); i++ {
		pathURL, _ := url.Parse(urls[i])
		path := pathURL.Path
		forceTs := ""
		lastIdx := strings.LastIndex(path, "/")
		if lastIdx != -1 {
			lastComponent := path[lastIdx+1:]
			reg, _ := regexp.Compile("^ts\\d+$")
			if reg.MatchString(lastComponent) {
				forceTs = lastComponent[2:]
			}
		}

		fmt.Printf("%s --> forceTs: %s\n", urls[i], forceTs)
	}

}

// go test imageproxy -v -run "TestParseOptions"
func TestParseOptions(t *testing.T) {
	tests := []struct {
		Input   string
		Options Options
	}{
		{"", emptyOptions},
		{"x", emptyOptions},
		{"r", emptyOptions},
		{"0", emptyOptions},
		{",,,,", emptyOptions},

		// size variations
		{"1x", Options{Width: 1}},
		{"x1", Options{Height: 1}},
		{"1x2", Options{Width: 1, Height: 2}},
		{"-1x-2", Options{Width: -1, Height: -2}},
		{"0.1x0.2", Options{Width: 0.1, Height: 0.2}},
		{"1", Options{Width: 1, Height: 1}},
		{"0.1", Options{Width: 0.1, Height: 0.1}},

		// additional flags
		{"fit", Options{Fit: true}},
		{"r90", Options{Rotate: 90}},
		{"fv", Options{FlipVertical: true}},
		{"fh", Options{FlipHorizontal: true}},

		// duplicate flags (last one wins)
		{"1x2,3x4", Options{Width: 3, Height: 4}},
		{"1x2,3", Options{Width: 3, Height: 3}},
		{"1x2,0x3", Options{Width: 0, Height: 3}},
		{"1x,x2", Options{Width: 1, Height: 2}},
		{"r90,r270", Options{Rotate: 270}},

		// mix of valid and invalid flags
		{"FOO,1,BAR,r90,BAZ", Options{Width: 1, Height: 1, Rotate: 90}},

		// all flags, in different orders
		{"q70,1x2,fit,r90,fv,fh", Options{1, 2, true, 90, true, true, 70, ""}},

		// // Width, Height, Fit, Rotate, FlipVertical, FlipHorizontal, Quality, Format
		{"r90,fh,q90,1x2,fv,fit", Options{1, 2, true, 90, true, true, 90, ""}},
	}

	for _, tt := range tests {
		if got, want := ParseOptions(tt.Input, false), tt.Options; got != want {
			t.Errorf("ParseOptions(%q) returned %#v, want %#v", tt.Input, got, want)
		}
	}
}

// Test that request URLs are properly parsed into Options and RemoteURL.  This
// test verifies that invalid remote URLs throw errors, and that valid
// combinations of Options and URL are accept.  This does not exhaustively test
// the various Options that can be specified; see TestParseOptions for that.
// go test imageproxy -v -run "TestNewRequest"
func TestNewRequest(t *testing.T) {
	tests := []struct {
		URL         string  // input URL to parse as an imageproxy request
		RemoteURL   string  // expected URL of remote image parsed from input
		Options     Options // expected options parsed from input
		ExpectError bool    // whether an error is expected from NewRequest
	}{
		// invalid URLs
		{"http://localhost/", "", emptyOptions, true},
		{"http://localhost/1/", "", emptyOptions, true},
		{"http://localhost//example.com/foo", "", emptyOptions, true},
		{"http://localhost//ftp://example.com/foo", "", emptyOptions, true},

		// invalid options.  These won't return errors, but will not fully parse the options
		{
			"http://localhost/tools/im/s/http://example.com/", "http://example.com/", emptyOptions, false,
		},
		{
			"http://localhost/tools/im/1xs/http://example.com/", "http://example.com/", Options{Width: 1}, false,
		},

		// valid URLs
		{
			"http://localhost/tools/im/http://example.com/foo", "http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/tools/im//http://example.com/foo", "http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/tools/im//https://example.com/foo", "https://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/tools/im/1x2/http://example.com/foo", "http://example.com/foo", Options{Width: 1, Height: 2}, false,
		},
		{
			"http://localhost/tools/im//http://example.com/foo?bar", "http://example.com/foo?bar", emptyOptions, false,
		},
		{
			"http://localhost/tools/im/http:/example.com/foo", "http://example.com/foo", emptyOptions, false,
		},
		{
			"http://localhost/tools/im/http:///example.com/foo", "http://example.com/foo", emptyOptions, false,
		},
	}

	awsUrl, _ := url.Parse("http://awss3")
	for index, tt := range tests {
		fmt.Printf("Processing index: %d\n", index)
		req, err := http.NewRequest("GET", tt.URL, nil)
		if err != nil {
			t.Errorf("http.NewRequest(%q) returned error: %v", tt.URL, err)
			continue
		}

		r, err := NewRequest(req, awsUrl)
		if tt.ExpectError {
			if err == nil {
				t.Errorf("NewRequest(%v) did not return expected error", req)
			}
			continue
		} else if err != nil {
			t.Errorf("NewRequest(%v) return unexpected error: %v", req, err)
			continue
		}

		if got, want := r.URL.String(), tt.RemoteURL; got != want {
			t.Errorf("NewRequest(%q) request URL = %v, want %v", tt.URL, got, want)
		}

		// Options的解析
		if got, want := r.Options, tt.Options; got != want {
			t.Errorf("NewRequest(%q) request options = %v, want %v", tt.URL, got, want)
		}
	}
}
