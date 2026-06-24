package base_core

import (
	"os"
	"testing"
)

// === getEnv / getEnvInt 测试 ===

func TestGetEnv_WithEnvSet(t *testing.T) {
	os.Setenv("TEST_GET_ENV_KEY", "hello")
	defer os.Unsetenv("TEST_GET_ENV_KEY")

	got := getEnv("TEST_GET_ENV_KEY", "default")
	if got != "hello" {
		t.Errorf("getEnv = %q, want %q", got, "hello")
	}
}

func TestGetEnv_WithEnvEmpty(t *testing.T) {
	os.Setenv("TEST_GET_ENV_KEY", "")
	defer os.Unsetenv("TEST_GET_ENV_KEY")

	got := getEnv("TEST_GET_ENV_KEY", "default")
	if got != "default" {
		t.Errorf("getEnv = %q, want %q", got, "default")
	}
}

func TestGetEnv_WithoutEnvSet(t *testing.T) {
	os.Unsetenv("TEST_GET_ENV_KEY")
	got := getEnv("TEST_GET_ENV_KEY", "default")
	if got != "default" {
		t.Errorf("getEnv = %q, want %q", got, "default")
	}
}

func TestGetEnvInt_WithValidValue(t *testing.T) {
	os.Setenv("TEST_GET_INT", "8080")
	defer os.Unsetenv("TEST_GET_INT")

	got := getEnvInt("TEST_GET_INT", 80)
	if got != 8080 {
		t.Errorf("getEnvInt = %d, want 8080", got)
	}
}

func TestGetEnvInt_WithInvalidValue(t *testing.T) {
	os.Setenv("TEST_GET_INT", "abc")
	defer os.Unsetenv("TEST_GET_INT")

	got := getEnvInt("TEST_GET_INT", 80)
	if got != 80 {
		t.Errorf("getEnvInt = %d, want 80 (default on invalid)", got)
	}
}

func TestGetEnvInt_WithoutEnvSet(t *testing.T) {
	os.Unsetenv("TEST_GET_INT")
	got := getEnvInt("TEST_GET_INT", 80)
	if got != 80 {
		t.Errorf("getEnvInt = %d, want 80", got)
	}
}

// === IsValidDomain 测试 ===

func TestIsValidDomain_Valid(t *testing.T) {
	cases := []string{
		"example.com",
		"sub.domain.example.com",
		"my-host.domain.org",
		"a.b",
		"test123.example.com",
	}
	for _, d := range cases {
		if !IsValidDomain(d) {
			t.Errorf("IsValidDomain(%q) = false, want true", d)
		}
	}
}

func TestIsValidDomain_Invalid(t *testing.T) {
	cases := []struct {
		domain string
		reason string
	}{
		{"", "empty"},
		{"localhost", "single label"},
		{"exa mple.com", "contains space"},
		{"example.com.", "trailing dot"},
		{".example.com", "leading dot"},
		{"exa!mple.com", "contains special char"},
		{"example.com:8080", "contains port"},
	}
	for _, c := range cases {
		if IsValidDomain(c.domain) {
			t.Errorf("IsValidDomain(%q) = true, want false (%s)", c.domain, c.reason)
		}
	}
}

func TestIsValidDomain_LengthLimits(t *testing.T) {
	// Total length > 253
	longDomain := ""
	for i := 0; i < 30; i++ {
		longDomain += "abcdefghij."
	}
	if IsValidDomain(longDomain) {
		t.Error("IsValidDomain(long domain) = true, want false")
	}

	// Label length > 63
	longLabel := ""
	for i := 0; i < 7; i++ {
		longLabel += "abcdefghij"
	}
	if IsValidDomain(longLabel + ".com") {
		t.Error("IsValidDomain(label > 63) = true, want false")
	}
}

// === LoadConfigFromEnv 测试 ===

func TestLoadConfigFromEnv_Defaults(t *testing.T) {
	// 确保环境变量不存在
	os.Unsetenv("NSSH_SERVER_HOST")
	os.Unsetenv("NSSH_SERVER_PORT")
	os.Unsetenv("NSSH_USERNAME")
	os.Unsetenv("NSSH_PASSWORD")
	os.Unsetenv("NSSH_SSH_KEY")
	os.Unsetenv("NSSH_LOCAL_HOST")
	os.Unsetenv("NSSH_LOCAL_PORT")
	os.Unsetenv("NSSH_REMOTE_PORT")

	config := LoadConfigFromEnv()

	if config.ServerHost != "" {
		t.Errorf("ServerHost = %q, want empty", config.ServerHost)
	}
	if config.ServerPort != 0 {
		t.Errorf("ServerPort = %d, want 0", config.ServerPort)
	}
	if config.LocalHost != "localhost" {
		t.Errorf("LocalHost = %q, want localhost", config.LocalHost)
	}
	if config.ReconnectDelay != 60 {
		t.Errorf("ReconnectDelay = %d, want 60", config.ReconnectDelay)
	}
}

func TestLoadConfigFromEnv_WithEnvValues(t *testing.T) {
	os.Setenv("NSSH_SERVER_HOST", "example.com")
	os.Setenv("NSSH_SERVER_PORT", "2222")
	os.Setenv("NSSH_USERNAME", "testuser")
	os.Setenv("NSSH_PASSWORD", "secret")
	defer func() {
		os.Unsetenv("NSSH_SERVER_HOST")
		os.Unsetenv("NSSH_SERVER_PORT")
		os.Unsetenv("NSSH_USERNAME")
		os.Unsetenv("NSSH_PASSWORD")
	}()

	config := LoadConfigFromEnv()

	if config.ServerHost != "example.com" {
		t.Errorf("ServerHost = %q, want example.com", config.ServerHost)
	}
	if config.ServerPort != 2222 {
		t.Errorf("ServerPort = %d, want 2222", config.ServerPort)
	}
	if config.Username != "testuser" {
		t.Errorf("Username = %q, want testuser", config.Username)
	}
	if config.Password != "secret" {
		t.Errorf("Password = %q, want secret", config.Password)
	}
}

// === MaxLifetime 测试 ===

func TestLoadConfigFromEnv_MaxLifetimeDefault(t *testing.T) {
	os.Unsetenv("MAX_LIFETIME")
	config := LoadConfigFromEnv()
	if config.MaxLifetime != 172800 {
		t.Errorf("MaxLifetime default = %d, want 172800 (48h)", config.MaxLifetime)
	}
}

func TestLoadConfigFromEnv_MaxLifetimeFromEnv(t *testing.T) {
	os.Setenv("MAX_LIFETIME", "86400")
	defer os.Unsetenv("MAX_LIFETIME")

	config := LoadConfigFromEnv()
	if config.MaxLifetime != 86400 {
		t.Errorf("MaxLifetime = %d, want 86400", config.MaxLifetime)
	}
}

func TestLoadConfigFromEnv_MaxLifetimeDisabled(t *testing.T) {
	os.Setenv("MAX_LIFETIME", "0")
	defer os.Unsetenv("MAX_LIFETIME")

	config := LoadConfigFromEnv()
	if config.MaxLifetime != 0 {
		t.Errorf("MaxLifetime = %d, want 0 (disabled)", config.MaxLifetime)
	}
}
