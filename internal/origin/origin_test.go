package origin

import (
	"reflect"
	"testing"
)

func TestBuildAllowedOriginsWithPublicOrigin(t *testing.T) {
	t.Run("single", func(t *testing.T) {
		got := BuildAllowedOrigins(":3000", "https://Example.com/path")
		want := []string{"https://example.com"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("BuildAllowedOrigins() = %#v, want %#v", got, want)
		}
	})

	t.Run("multiple", func(t *testing.T) {
		got := BuildAllowedOrigins(":3000", "https://foo.example, http://Bar.example:8080, ,")
		want := []string{"https://foo.example", "http://bar.example:8080"}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("BuildAllowedOrigins() = %#v, want %#v", got, want)
		}
	})
}

func TestBuildAllowedOriginsFallback(t *testing.T) {
	got := BuildAllowedOrigins(":9090", "")
	want := []string{
		DefaultOrigin,
		"http://localhost:9090",
		"http://127.0.0.1:9090",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildAllowedOrigins() = %#v, want %#v", got, want)
	}
}

func TestDeriveRPID(t *testing.T) {
	tests := map[string]struct {
		listen       string
		public       string
		expectedRPID string
	}{
		"public non localhost": {
			listen:       ":8080",
			public:       "https://app.example.com",
			expectedRPID: "app.example.com",
		},
		"public localhost": {
			listen:       ":8080",
			public:       "https://localhost:3000",
			expectedRPID: "localhost",
		},
		"fallback listen": {
			listen:       "0.0.0.0:8080",
			public:       "",
			expectedRPID: "localhost",
		},
		"listen host preserved": {
			listen:       "example.com:8080",
			public:       "",
			expectedRPID: "example.com",
		},
	}

	for name, tc := range tests {
		tc := tc
		t.Run(name, func(t *testing.T) {
			if got := DeriveRPID(tc.listen, tc.public); got != tc.expectedRPID {
				t.Fatalf("DeriveRPID(%q, %q) = %q, want %q", tc.listen, tc.public, got, tc.expectedRPID)
			}
		})
	}
}

func TestParseOriginList(t *testing.T) {
	got := parseOriginList(" https://one.example ,http://two.example;\nhttps://three.example\t")
	want := []string{
		"https://one.example",
		"http://two.example",
		"https://three.example",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseOriginList() = %#v, want %#v", got, want)
	}

	if got := parseOriginList("   "); got != nil {
		t.Fatalf("parseOriginList empty = %#v, want nil", got)
	}
}

func TestNormalizeOrigin(t *testing.T) {
	tests := map[string]string{
		"https://app.example":         "https://app.example",
		"http://LOCALHOST:8080/path/": "http://localhost:8080",
		"   https://app.example  ":    "https://app.example",
		"":                            "",
		"ftp://":                      "",
		"://missing":                  "",
		"http://invalid host":         "",
	}

	for input, want := range tests {
		if got := normalizeOrigin(input); got != want {
			t.Fatalf("normalizeOrigin(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestOriginsFromListen(t *testing.T) {
	got := originsFromListen("example.com:9000")
	want := []string{
		"http://localhost:9000",
		"http://127.0.0.1:9000",
		"http://example.com:9000",
	}

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("originsFromListen() = %#v, want %#v", got, want)
	}

	if len(originsFromListen("")) != 0 {
		t.Fatalf("originsFromListen empty should be nil or empty slice")
	}
}

func TestHostFromOrigin(t *testing.T) {
	if got := hostFromOrigin("https://api.example.com/account"); got != "api.example.com" {
		t.Fatalf("hostFromOrigin() = %q, want %q", got, "api.example.com")
	}

	if got := hostFromOrigin("https://192.168.1.10:8080"); got != "localhost" {
		t.Fatalf("hostFromOrigin() IP = %q, want localhost", got)
	}

	if got := hostFromOrigin("://bad"); got != "" {
		t.Fatalf("hostFromOrigin invalid = %q, want empty string", got)
	}
}

func TestHostFromListen(t *testing.T) {
	tests := map[string]string{
		"":              "localhost",
		":9090":         "localhost",
		"0.0.0.0:3000":  "localhost",
		"example.com:1": "example.com",
		"[::1]:8080":    "localhost",
	}

	for listen, want := range tests {
		if got := hostFromListen(listen); got != want {
			t.Fatalf("hostFromListen(%q) = %q, want %q", listen, got, want)
		}
	}
}

func TestNormalizeRPIDHost(t *testing.T) {
	tests := map[string]string{
		"":          "",
		"  ":        "",
		"0.0.0.0":   "localhost",
		"::":        "localhost",
		"127.0.0.1": "localhost",
		"api.foo":   "api.foo",
	}

	for input, want := range tests {
		if got := normalizeRPIDHost(input); got != want {
			t.Fatalf("normalizeRPIDHost(%q) = %q, want %q", input, got, want)
		}
	}
}
